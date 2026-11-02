// Package docs renders a collection as a self-contained HTML reference:
// every request with its description, parameters, body, saved examples and
// a cURL command. Secret values never appear: auth shows its type and the
// variables it uses, and secret variables are masked. See docs/docs-and-mocks.md.
package docs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	"github.com/codeen15/pyro/pkg/engine/importer"
	"github.com/codeen15/pyro/pkg/engine/model"
)

// Markdown without raw HTML (goldmark's default): imported descriptions
// can't inject script into the page.
var md = goldmark.New(goldmark.WithExtensions(extension.GFM))

func markdown(s string) template.HTML {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := md.Convert([]byte(s), &buf); err != nil {
		return template.HTML(template.HTMLEscapeString(s))
	}
	return template.HTML(buf.String()) // goldmark escapes text and drops raw HTML
}

type page struct {
	Title, Description template.HTML
	Name               string
	Generated          string
	Variables          []model.Variable
	Auth               string
	Sections           []*section
	Count              int
}

type section struct {
	ID, Name    string
	Depth       int
	Description template.HTML
	Requests    []*entry
}

type entry struct {
	ID, Name, Method, Kind string
	URL                    template.HTML
	Description            template.HTML
	Auth                   string
	Query, Headers         []model.KeyValue
	Body                   *model.Body
	BodyText               string
	Examples               []example
	Curl                   string
}

type example struct {
	Name    string
	Status  int
	Headers []model.KeyValue
	Body    string
}

var placeholder = regexp.MustCompile(`\{\{\s*[^{}\s]+\s*\}\}`)

// highlight escapes s and marks {{variables}}.
func highlight(s string) template.HTML {
	esc := template.HTMLEscapeString(s)
	return template.HTML(placeholder.ReplaceAllStringFunc(esc, func(m string) string {
		return `<span class="var">` + m + `</span>`
	}))
}

// Render builds the page for a collection snapshot (store.Snapshot).
func Render(b *importer.Bundle) ([]byte, error) {
	if b.Collection == nil {
		return nil, fmt.Errorf("nothing to document")
	}
	c := b.Collection
	p := &page{Name: c.Name, Title: template.HTML(template.HTMLEscapeString(c.Name)), Description: markdown(c.Description),
		Generated: time.Now().Format("2 January 2006"), Auth: describeAuth(c.Auth, "")}
	for _, v := range c.Variables {
		if v.Disabled {
			continue
		}
		if v.Secret {
			v.Value = "••••••"
		}
		p.Variables = append(p.Variables, v)
	}

	sections := map[string]*section{}
	root := &section{ID: "requests", Name: "Requests"}
	p.Sections = append(p.Sections, root)
	sectionFor := func(dir string) *section {
		if dir == "" {
			return root
		}
		if s := sections[dir]; s != nil {
			return s
		}
		f := b.Folders[dir]
		s := &section{ID: "f-" + slug(dir), Name: lastSeg(dir), Depth: strings.Count(dir, "/")}
		if f != nil {
			if f.Name != "" {
				s.Name = f.Name
			}
			s.Description = markdown(f.Description)
		}
		sections[dir] = s
		p.Sections = append(p.Sections, s)
		return s
	}
	ids := map[string]bool{}
	for _, r := range b.Requests {
		req := r.Request
		dir := ""
		if i := strings.LastIndexByte(r.Path, '/'); i >= 0 {
			dir = r.Path[:i]
		}
		e := &entry{Name: req.Name, Method: strings.ToUpper(req.Method), Kind: string(req.Kind), URL: highlight(req.URL),
			Description: markdown(req.Description), Query: enabled(req.Query), Headers: enabled(req.Headers), Body: req.Body}
		if e.Method == "" {
			e.Method = "GET"
		}
		switch req.Kind {
		case model.KindWebSocket:
			e.Method = "WS"
		case model.KindSocketIO:
			e.Method = "SIO"
		}
		if strings.HasPrefix(string(req.Kind), "plugin:") {
			e.Method = strings.ToUpper(strings.TrimPrefix(string(req.Kind), "plugin:"))
		}
		id := "r-" + slug(r.Path)
		for i := 2; ids[id]; i++ {
			id = fmt.Sprintf("r-%s-%d", slug(r.Path), i)
		}
		ids[id] = true
		e.ID = id
		ea, from := effectiveAuth(req, b, dir)
		e.Auth = describeAuth(ea, from)
		if req.Body != nil {
			e.BodyText = bodyText(req.Body)
		}
		for _, ex := range req.Examples {
			e.Examples = append(e.Examples, example{Name: ex.Name, Status: ex.Status, Headers: ex.Headers, Body: pretty(ex.Body)})
		}
		if req.Kind == "" || req.Kind == model.KindHTTP {
			e.Curl = curl(req, ea)
		}
		sec := sectionFor(dir)
		sec.Requests = append(sec.Requests, e)
		p.Count++
	}
	if len(root.Requests) == 0 {
		p.Sections = p.Sections[1:]
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, p); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func enabled(kvs []model.KeyValue) []model.KeyValue {
	var out []model.KeyValue
	for _, kv := range kvs {
		if kv.Key != "" {
			out = append(out, kv)
		}
	}
	return out
}

// effectiveAuth returns the auth a request uses (own or inherited) and,
// when inherited, where from.
func effectiveAuth(req *model.Request, b *importer.Bundle, dir string) (*model.Auth, string) {
	if req.Auth != nil {
		return req.Auth, ""
	}
	for d := dir; d != ""; {
		if f := b.Folders[d]; f != nil && f.Auth != nil {
			name := f.Name
			if name == "" {
				name = lastSeg(d)
			}
			return f.Auth, "from folder " + name
		}
		i := strings.LastIndexByte(d, '/')
		if i < 0 {
			break
		}
		d = d[:i]
	}
	if b.Collection.Auth != nil {
		return b.Collection.Auth, "from the collection"
	}
	return nil, ""
}

// describeAuth names an auth type and the variables it uses, never values.
func describeAuth(a *model.Auth, from string) string {
	if a == nil {
		return ""
	}
	var s string
	switch a.Type {
	case "none", "noauth":
		s = "No auth"
	case "basic":
		s = "Basic auth"
	case "bearer":
		s = "Bearer token"
	case "apikey":
		where := "header"
		if a.Params["in"] == "query" {
			where = "query parameter"
		}
		s = fmt.Sprintf("API key in %s %s", where, a.Params["key"])
	case "digest":
		s = "Digest auth"
	case "oauth2":
		s = "OAuth 2.0"
		if g := a.Params["grant_type"]; g != "" {
			s += " (" + strings.ReplaceAll(g, "_", " ") + ")"
		}
		if sc := a.Params["scope"]; sc != "" {
			s += ", scope " + sc
		}
	default:
		s = a.Type
	}
	var vars []string
	seen := map[string]bool{}
	keys := make([]string, 0, len(a.Params))
	for k := range a.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, m := range placeholder.FindAllString(a.Params[k], -1) {
			if !seen[m] {
				seen[m] = true
				vars = append(vars, m)
			}
		}
	}
	if len(vars) > 0 && a.Type != "none" {
		s += " using " + strings.Join(vars, ", ")
	}
	if from != "" {
		s += " (" + from + ")"
	}
	return s
}

func bodyText(b *model.Body) string {
	switch b.Type {
	case model.BodyJSON:
		return pretty(b.Content)
	case model.BodyForm, model.BodyMultipart:
		var lines []string
		for _, f := range b.Fields {
			if f.Disabled {
				continue
			}
			v := f.Value
			if f.File {
				v = "@" + v
			}
			lines = append(lines, f.Key+" = "+v)
		}
		return strings.Join(lines, "\n")
	case model.BodyGraphQL:
		s := b.Content
		if b.Variables != "" {
			s += "\n\n# variables\n" + pretty(b.Variables)
		}
		return s
	case model.BodyBinaryFile:
		return "file: " + b.File
	}
	return b.Content
}

func pretty(s string) string {
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return s
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if enc.Encode(v) != nil {
		return s
	}
	return strings.TrimRight(buf.String(), "\n")
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// curl builds a command with variables left as {{name}}; auth values that
// aren't variable references are replaced by <name>.
func curl(r *model.Request, a *model.Auth) string {
	method := strings.ToUpper(r.Method)
	if method == "" {
		method = "GET"
	}
	u := r.URL
	var q []string
	for _, kv := range r.Query {
		if !kv.Disabled && kv.Key != "" {
			q = append(q, kv.Key+"="+kv.Value)
		}
	}
	if len(q) > 0 {
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		u += sep + strings.Join(q, "&")
	}
	first := "curl "
	if method != "GET" {
		first += "-X " + method + " "
	}
	parts := []string{first + shellQuote(u)}
	for _, h := range r.Headers {
		if !h.Disabled && h.Key != "" {
			parts = append(parts, "-H "+shellQuote(h.Key+": "+h.Value))
		}
	}
	if a != nil {
		val := func(k string) string {
			v := a.Params[k]
			if placeholder.MatchString(v) && placeholder.FindString(v) == strings.TrimSpace(v) {
				return v
			}
			return "<" + k + ">"
		}
		switch a.Type {
		case "basic", "digest":
			flag := "-u "
			if a.Type == "digest" {
				flag = "--digest -u "
			}
			parts = append(parts, flag+shellQuote(val("username")+":"+val("password")))
		case "bearer":
			parts = append(parts, "-H "+shellQuote("Authorization: Bearer "+val("token")))
		case "apikey":
			if a.Params["in"] != "query" {
				parts = append(parts, "-H "+shellQuote(a.Params["key"]+": "+val("value")))
			}
		case "oauth2":
			parts = append(parts, "-H "+shellQuote("Authorization: Bearer <access token>"))
		}
	}
	if b := r.Body; b != nil {
		switch b.Type {
		case model.BodyJSON, model.BodyText, model.BodyXML:
			if b.Content != "" {
				parts = append(parts, "--data-raw "+shellQuote(b.Content))
			}
		case model.BodyForm:
			for _, f := range b.Fields {
				if !f.Disabled {
					parts = append(parts, "--data-urlencode "+shellQuote(f.Key+"="+f.Value))
				}
			}
		case model.BodyMultipart:
			for _, f := range b.Fields {
				if f.Disabled {
					continue
				}
				v := f.Value
				if f.File {
					v = "@" + v
				}
				parts = append(parts, "-F "+shellQuote(f.Key+"="+v))
			}
		case model.BodyBinaryFile:
			parts = append(parts, "--data-binary "+shellQuote("@"+b.File))
		case model.BodyGraphQL:
			payload, _ := json.Marshal(map[string]any{"query": b.Content})
			parts = append(parts, "--data-raw "+shellQuote(string(payload)))
		}
	}
	return strings.Join(parts, " \\\n  ")
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

func lastSeg(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}
