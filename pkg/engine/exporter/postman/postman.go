// Package postman exports collections and environments as Postman
// Collection v2.1 and environment JSON, so work can move back to Postman
// (or any tool that reads its format). Pyro-only features are converted
// where Postman has an equivalent (assertions and extractors become test
// script code) and reported otherwise. See docs/import-export.md.
package postman

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/codeen15/pyro/pkg/engine/importer"
	"github.com/codeen15/pyro/pkg/engine/model"
)

const schemaV21 = "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"

type item struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Item        []*item  `json:"item,omitempty"`
	Request     *request `json:"request,omitempty"`
	Auth        *auth    `json:"auth,omitempty"`
	Event       []event  `json:"event,omitempty"`
	Response    []resp   `json:"response,omitempty"`
	isFolder    bool
}

type resp struct {
	Name            string   `json:"name"`
	OriginalRequest *request `json:"originalRequest,omitempty"`
	Status          string   `json:"status,omitempty"`
	Code            int      `json:"code,omitempty"`
	Header          []kv     `json:"header"`
	Body            string   `json:"body"`
}

// MarshalJSON keeps "item": [] on empty folders (that's how Postman tells
// folders from requests).
func (it *item) MarshalJSON() ([]byte, error) {
	type plain item
	if it.isFolder && it.Item == nil {
		return json.Marshal(struct {
			*plain
			Item []*item `json:"item"`
		}{(*plain)(it), []*item{}})
	}
	return json.Marshal((*plain)(it))
}

type request struct {
	Method      string `json:"method"`
	Header      []kv   `json:"header"`
	Body        *body  `json:"body,omitempty"`
	URL         url    `json:"url"`
	Auth        *auth  `json:"auth,omitempty"`
	Description string `json:"description,omitempty"`
}

type kv struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Type     string `json:"type,omitempty"`
	Src      string `json:"src,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
}

type url struct {
	Raw      string   `json:"raw"`
	Protocol string   `json:"protocol,omitempty"`
	Host     []string `json:"host,omitempty"`
	Port     string   `json:"port,omitempty"`
	Path     []string `json:"path,omitempty"`
	Query    []kv     `json:"query,omitempty"`
}

type body struct {
	Mode       string `json:"mode"`
	Raw        string `json:"raw,omitempty"`
	URLEncoded []kv   `json:"urlencoded,omitempty"`
	FormData   []kv   `json:"formdata,omitempty"`
	File       *struct {
		Src string `json:"src"`
	} `json:"file,omitempty"`
	GraphQL *struct {
		Query     string `json:"query"`
		Variables string `json:"variables,omitempty"`
	} `json:"graphql,omitempty"`
	Options map[string]any `json:"options,omitempty"`
}

type auth struct {
	Type   string `json:"type"`
	params []kv
}

func (a *auth) MarshalJSON() ([]byte, error) {
	m := map[string]any{"type": a.Type}
	if a.Type != "noauth" {
		m[a.Type] = a.params
	}
	return json.Marshal(m)
}

type event struct {
	Listen string `json:"listen"`
	Script struct {
		Type string   `json:"type"`
		Exec []string `json:"exec"`
	} `json:"script"`
}

type variable struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Type     string `json:"type,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
}

// Export converts a collection snapshot (requests in run order; Folders
// holds every folder's settings by path) to Postman v2.1 JSON.
func Export(b *importer.Bundle) ([]byte, *importer.Report, error) {
	if b.Collection == nil {
		return nil, nil, fmt.Errorf("nothing to export")
	}
	rep := &importer.Report{Format: "postman-v2.1", Collection: b.Collection.Name, Slug: b.Slug, Warnings: []importer.Warning{}}
	x := &exporter{rep: rep}
	out := struct {
		Info struct {
			Name        string `json:"name"`
			Description string `json:"description,omitempty"`
			Schema      string `json:"schema"`
		} `json:"info"`
		Item     []*item    `json:"item"`
		Auth     *auth      `json:"auth,omitempty"`
		Event    []event    `json:"event,omitempty"`
		Variable []variable `json:"variable,omitempty"`
	}{}
	out.Info.Name, out.Info.Description, out.Info.Schema = b.Collection.Name, b.Collection.Description, schemaV21
	out.Auth = x.auth("", b.Collection.Auth)
	out.Event = x.events(procs{b.Collection.Scripts, b.Collection.Pre, b.Collection.Post, b.Collection.Think, nil, nil}, "")
	for _, v := range b.Collection.Variables {
		pv := variable{Key: v.Key, Value: v.Value, Disabled: v.Disabled}
		if v.Secret {
			pv.Type = "secret"
		}
		out.Variable = append(out.Variable, pv)
	}

	root := &item{isFolder: true}
	folders := map[string]*item{"": root}
	var folder func(p string) *item
	folder = func(p string) *item {
		if f, ok := folders[p]; ok {
			return f
		}
		dir, base := splitPath(p)
		parent := folder(dir)
		f := &item{Name: base, isFolder: true}
		if s := b.Folders[p]; s != nil {
			if s.Name != "" {
				f.Name = s.Name
			}
			f.Description = s.Description
			f.Auth = x.auth(p, s.Auth)
			f.Event = x.events(procs{s.Scripts, s.Pre, s.Post, s.Think, nil, nil}, p)
			if len(s.Variables) > 0 {
				rep.Warn(p, "export.folder-variables", "Postman folders have no variables; they were left out")
			}
		}
		parent.Item = append(parent.Item, f)
		folders[p] = f
		rep.Folders++
		return f
	}
	for _, r := range b.Requests {
		dir, _ := splitPath(r.Path)
		it := x.request(r.Path, r.Request)
		if it == nil {
			continue
		}
		parent := folder(dir)
		parent.Item = append(parent.Item, it)
		rep.Requests++
	}
	// Folders with no requests still exist in Postman.
	var empty []string
	for p := range b.Folders {
		if _, ok := folders[p]; !ok {
			empty = append(empty, p)
		}
	}
	sort.Strings(empty)
	for _, p := range empty {
		folder(p)
	}
	out.Item = root.Item
	if out.Item == nil {
		out.Item = []*item{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "\t")
	if err := enc.Encode(out); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), rep, nil
}

// ExportEnvironment converts an environment to Postman's environment JSON.
func ExportEnvironment(env *model.Environment) ([]byte, error) {
	type value struct {
		Key     string `json:"key"`
		Value   string `json:"value"`
		Type    string `json:"type"`
		Enabled bool   `json:"enabled"`
	}
	out := struct {
		Name   string  `json:"name"`
		Values []value `json:"values"`
		Scope  string  `json:"_postman_variable_scope"`
	}{Name: env.Name, Values: []value{}, Scope: "environment"}
	for _, v := range env.Variables {
		t := "default"
		if v.Secret {
			t = "secret"
		}
		out.Values = append(out.Values, value{Key: v.Key, Value: v.Value, Type: t, Enabled: !v.Disabled})
	}
	return json.MarshalIndent(out, "", "\t")
}

type exporter struct{ rep *importer.Report }

func splitPath(p string) (dir, base string) {
	i := strings.LastIndexByte(p, '/')
	if i < 0 {
		return "", p
	}
	return p[:i], p[i+1:]
}

func (x *exporter) request(p string, r *model.Request) *item {
	switch {
	case r.Kind == model.KindWebSocket || r.Kind == model.KindSocketIO:
		x.rep.Warn(p, "export.kind", fmt.Sprintf("%s requests can't be stored in a Postman v2.1 collection; skipped", r.Kind))
		return nil
	case r.Kind != "" && r.Kind != model.KindHTTP:
		x.rep.Warn(p, "export.kind", fmt.Sprintf("plugin request type %s has no Postman equivalent; skipped", r.Kind))
		return nil
	}
	method := r.Method
	if method == "" {
		method = "GET"
	}
	pr := &request{Method: strings.ToUpper(method), Header: []kv{}, URL: splitURL(r.URL, r.Query), Description: r.Description}
	for _, h := range r.Headers {
		pr.Header = append(pr.Header, kv{Key: h.Key, Value: h.Value, Disabled: h.Disabled})
	}
	pr.Body = x.body(p, r.Body)
	pr.Auth = x.auth(p, r.Auth)
	it := &item{Name: r.Name, Request: pr, Event: x.events(procs{r.Scripts, r.Pre, r.Post, r.Think, r.Assertions, r.Extract}, p)}
	for _, ex := range r.Examples {
		rp := resp{Name: ex.Name, OriginalRequest: pr, Code: ex.Status, Status: http.StatusText(ex.Status), Header: []kv{}, Body: ex.Body}
		for _, h := range ex.Headers {
			rp.Header = append(rp.Header, kv{Key: h.Key, Value: h.Value})
		}
		it.Response = append(it.Response, rp)
	}
	return it
}

// splitURL fills Postman's structured URL from the raw one; the raw string
// carries enabled query parameters, as Postman expects.
func splitURL(raw string, query []model.KeyValue) url {
	u := url{Raw: raw}
	rest := raw
	if i := strings.Index(rest, "://"); i >= 0 {
		u.Protocol, rest = rest[:i], rest[i+3:]
	}
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		rest = rest[:i] // a query left in the URL stays in raw
	}
	hostPart, pathPart, _ := strings.Cut(rest, "/")
	if h, port, ok := strings.Cut(hostPart, ":"); ok && !strings.Contains(hostPart, "{{") {
		hostPart, u.Port = h, port
	}
	if hostPart != "" {
		if strings.HasPrefix(hostPart, "{{") {
			u.Host = []string{hostPart}
		} else {
			u.Host = strings.Split(hostPart, ".")
		}
	}
	if pathPart != "" {
		u.Path = strings.Split(pathPart, "/")
	}
	var enabled []string
	for _, q := range query {
		u.Query = append(u.Query, kv{Key: q.Key, Value: q.Value, Disabled: q.Disabled})
		if !q.Disabled {
			enabled = append(enabled, q.Key+"="+q.Value)
		}
	}
	if len(enabled) > 0 {
		sep := "?"
		if strings.Contains(raw, "?") {
			sep = "&"
		}
		u.Raw += sep + strings.Join(enabled, "&")
	}
	return u
}

func (x *exporter) body(p string, b *model.Body) *body {
	if b == nil || b.Type == "" || b.Type == model.BodyNone {
		return nil
	}
	lang := func(l string) map[string]any { return map[string]any{"raw": map[string]string{"language": l}} }
	switch b.Type {
	case model.BodyJSON:
		return &body{Mode: "raw", Raw: b.Content, Options: lang("json")}
	case model.BodyXML:
		return &body{Mode: "raw", Raw: b.Content, Options: lang("xml")}
	case model.BodyText:
		return &body{Mode: "raw", Raw: b.Content, Options: lang("text")}
	case model.BodyForm:
		out := &body{Mode: "urlencoded", URLEncoded: []kv{}}
		for _, f := range b.Fields {
			out.URLEncoded = append(out.URLEncoded, kv{Key: f.Key, Value: f.Value, Type: "text", Disabled: f.Disabled})
		}
		return out
	case model.BodyMultipart:
		out := &body{Mode: "formdata", FormData: []kv{}}
		for _, f := range b.Fields {
			if f.File {
				out.FormData = append(out.FormData, kv{Key: f.Key, Src: f.Value, Type: "file", Disabled: f.Disabled})
			} else {
				out.FormData = append(out.FormData, kv{Key: f.Key, Value: f.Value, Type: "text", Disabled: f.Disabled})
			}
		}
		return out
	case model.BodyGraphQL:
		out := &body{Mode: "graphql"}
		out.GraphQL = &struct {
			Query     string `json:"query"`
			Variables string `json:"variables,omitempty"`
		}{b.Content, b.Variables}
		return out
	case model.BodyBinaryFile:
		out := &body{Mode: "file"}
		out.File = &struct {
			Src string `json:"src"`
		}{b.File}
		return out
	}
	x.rep.Warn(p, "export.body", fmt.Sprintf("body type %q has no Postman equivalent; left out", b.Type))
	return nil
}

func (x *exporter) auth(p string, a *model.Auth) *auth {
	if a == nil {
		return nil // inherit
	}
	if a.Type == "none" || a.Type == "" {
		return &auth{Type: "noauth"}
	}
	if strings.HasPrefix(a.Type, "plugin:") {
		x.rep.Warn(p, "export.auth", fmt.Sprintf("plugin auth type %s has no Postman equivalent; left as inherit", a.Type))
		return nil
	}
	keys := make([]string, 0, len(a.Params))
	for k := range a.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := &auth{Type: a.Type, params: []kv{}}
	for _, k := range keys {
		out.params = append(out.params, kv{Key: k, Value: a.Params[k], Type: "string"})
	}
	return out
}

// events converts scripts, plus assertions and extractors as test code.
// procs is everything that becomes a level's Postman scripts.
type procs struct {
	scripts   *model.Scripts
	pre, post []model.Processor
	think     *model.Timer
	as        []model.Assertion
	ex        []model.Extractor
}

func (x *exporter) events(l procs, p string) []event {
	var out []event
	add := func(listen string, parts []string) {
		code := strings.TrimSpace(strings.Join(parts, "\n\n"))
		if code == "" {
			return
		}
		e := event{Listen: listen}
		e.Script.Type = "text/javascript"
		e.Script.Exec = strings.Split(code, "\n")
		out = append(out, e)
	}
	var pre, post []string
	if l.scripts != nil {
		pre, post = []string{l.scripts.Pre}, []string{l.scripts.Post}
	}
	if l.think != nil && l.think.Type != "" && l.think.Type != "none" {
		x.rep.Warn(p, "export.think", "think time has no Postman equivalent; left out")
	}
	pre = append(pre, x.processors(l.pre, p)...)
	// Postman runs one test script: converted scripts and variables first,
	// then every assertion and extractor.
	as, ex := l.as, l.ex
	for _, pr := range l.post {
		if pr.Disabled {
			continue
		}
		switch {
		case pr.Type == model.ProcAssert && pr.Assert != nil:
			as = append(as, *pr.Assert)
		case pr.Type == model.ProcExtract && pr.Extract != nil:
			ex = append(ex, *pr.Extract)
		default:
			post = append(post, x.processors([]model.Processor{pr}, p)...)
		}
	}
	post = append(post, x.checks(as, ex, p))
	add("prerequest", pre)
	add("test", post)
	return out
}

var padFormat = regexp.MustCompile(`^%0(\d+)d$`)

// processors converts scripts, variables and counters to pm.* code.
func (x *exporter) processors(list []model.Processor, p string) []string {
	var out []string
	for _, pr := range list {
		if pr.Disabled {
			continue
		}
		switch pr.Type {
		case model.ProcScript:
			out = append(out, pr.Script)
		case model.ProcVars:
			var b strings.Builder
			for _, kv := range pr.Vars {
				if !kv.Disabled && kv.Key != "" {
					fmt.Fprintf(&b, "pm.variables.set(%s, pm.variables.replaceIn(%s));\n", js(kv.Key), js(kv.Value))
				}
			}
			out = append(out, b.String())
		case model.ProcCounter:
			c := pr.Counter
			if c == nil || c.Var == "" {
				continue
			}
			step := c.Step
			if step == 0 {
				step = 1
			}
			val := "String(n)"
			if m := padFormat.FindStringSubmatch(c.Format); m != nil {
				val = fmt.Sprintf("String(n).padStart(%s, \"0\")", m[1])
			} else if c.Format != "" {
				x.rep.Warn(p, "export.counter", fmt.Sprintf("counter format %q can't be converted; the plain number is used", c.Format))
			}
			wrap := ""
			if c.End != 0 {
				cmp := ">"
				if step < 0 {
					cmp = "<"
				}
				wrap = fmt.Sprintf(" if (n %s %d) n = %d;", cmp, c.End, c.Start)
			}
			out = append(out, fmt.Sprintf("{ // counter %[1]s\n  const cur = pm.variables.get(%[2]s);\n  let n = cur === undefined ? %[3]d : Number(cur) + %[4]d;%[5]s\n  pm.variables.set(%[2]s, String(n));\n  pm.variables.set(%[1]s, %[6]s);\n}",
				js(c.Var), js("__counter."+c.Var), c.Start, step, wrap, val))
		default:
			x.rep.Warn(p, "export.processor", fmt.Sprintf("processor %s has no Postman equivalent; left out", pr.Type))
		}
	}
	return out
}

var simpleKey = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// jsAccessor converts simple JSONPath ($.a.b[0]['c d']) to optional-chained
// JavaScript; ok is false for wildcards, filters and recursive descent.
func jsAccessor(path string) (string, bool) {
	if !strings.HasPrefix(path, "$") {
		return "", false
	}
	out := "pm.response.json()"
	rest := path[1:]
	for rest != "" {
		switch {
		case rest[0] == '.':
			rest = rest[1:]
			end := strings.IndexAny(rest, ".[")
			if end < 0 {
				end = len(rest)
			}
			name := rest[:end]
			if name == "" || name == "*" || !simpleKey.MatchString(name) {
				return "", false
			}
			out += "?." + name
			rest = rest[end:]
		case rest[0] == '[':
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				return "", false
			}
			inner := rest[1:end]
			rest = rest[end+1:]
			if n, err := strconv.Atoi(inner); err == nil && n >= 0 {
				out += fmt.Sprintf("?.[%d]", n)
				continue
			}
			if len(inner) >= 2 && (inner[0] == '\'' || inner[0] == '"') && inner[len(inner)-1] == inner[0] {
				k, _ := json.Marshal(inner[1 : len(inner)-1])
				out += "?.[" + string(k) + "]"
				continue
			}
			return "", false
		default:
			return "", false
		}
	}
	return out, true
}

func js(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// checks renders Pyro assertions and extractors as pm.* code with the same
// semantics: values compare as strings; JSON values render like Pyro does.
func (x *exporter) checks(as []model.Assertion, ex []model.Extractor, p string) string {
	if len(as) == 0 && len(ex) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("// Converted from Pyro assertions and extractors.\n")
	b.WriteString("const pyroRender = (v) => (v === undefined ? undefined : typeof v === \"string\" ? v : JSON.stringify(v));\n")
	expr := func(typ, path string) (string, bool) {
		switch typ {
		case "status":
			return "String(pm.response.code)", true
		case "duration":
			return "String(pm.response.responseTime)", true
		case "header":
			return "pm.response.headers.get(" + js(path) + ")", true
		case "body":
			return "pm.response.text()", true
		case "jsonpath":
			acc, ok := jsAccessor(path)
			if !ok {
				return "", false
			}
			return "pyroRender(" + acc + ")", true
		}
		return "", false
	}
	for _, a := range as {
		e, ok := expr(a.Type, a.Path)
		if !ok {
			x.rep.Warn(p, "export.assertion", fmt.Sprintf("assertion %s %s can't be converted to a Postman test; left out", a.Type, a.Path))
			continue
		}
		op := a.Op
		if op == "" {
			op = "eq"
			if a.Value == "" && (a.Type == "jsonpath" || a.Type == "header") {
				op = "exists"
			}
		}
		name := strings.TrimSpace(strings.Join([]string{a.Type, a.Path, op, a.Value}, " "))
		name = strings.Join(strings.Fields(name), " ")
		want := js(a.Value)
		var check string
		switch op {
		case "exists":
			check = "pm.expect(actual, \"present\").to.not.equal(undefined);"
		case "notexists":
			check = "pm.expect(actual, \"present\").to.equal(undefined);"
		case "eq":
			check = "pm.expect(actual).to.eql(" + want + ");"
		case "neq":
			check = "pm.expect(actual).to.not.eql(" + want + ");"
		case "contains":
			check = "pm.expect(actual).to.include(" + want + ");"
		case "matches":
			check = "pm.expect(actual).to.match(new RegExp(" + want + "));"
		case "lt":
			check = "pm.expect(Number(actual)).to.be.below(Number(" + want + "));"
		case "lte":
			check = "pm.expect(Number(actual)).to.be.at.most(Number(" + want + "));"
		case "gt":
			check = "pm.expect(Number(actual)).to.be.above(Number(" + want + "));"
		case "gte":
			check = "pm.expect(Number(actual)).to.be.at.least(Number(" + want + "));"
		default:
			x.rep.Warn(p, "export.assertion", fmt.Sprintf("operator %q can't be converted; left out", op))
			continue
		}
		fmt.Fprintf(&b, "pm.test(%s, () => {\n  const actual = %s;\n", js(name), e)
		if op != "exists" && op != "notexists" {
			b.WriteString("  pm.expect(actual, \"present\").to.not.equal(undefined);\n")
		}
		fmt.Fprintf(&b, "  %s\n});\n", check)
	}
	for _, e := range ex {
		switch e.From {
		case "regex":
			fmt.Fprintf(&b, "{\n  const m = pm.response.text().match(new RegExp(%s));\n  if (m) pm.variables.set(%s, m.length > 1 ? m[1] : m[0]);\n}\n", js(e.Path), js(e.Var))
			continue
		case "status":
			fmt.Fprintf(&b, "pm.variables.set(%s, String(pm.response.code));\n", js(e.Var))
			continue
		}
		v, ok := expr(e.From, e.Path)
		if !ok {
			x.rep.Warn(p, "export.extractor", fmt.Sprintf("extractor %s (%s %s) can't be converted; left out", e.Var, e.From, e.Path))
			continue
		}
		fmt.Fprintf(&b, "{\n  const v = %s;\n  if (v !== undefined) pm.variables.set(%s, v);\n}\n", v, js(e.Var))
	}
	return strings.TrimRight(b.String(), "\n")
}
