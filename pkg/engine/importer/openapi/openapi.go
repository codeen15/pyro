// Package openapi imports OpenAPI 3.0/3.1 and Swagger 2.0 definitions (JSON
// or YAML) as a collection: one folder per tag, one request per operation,
// with example bodies generated from schemas. See docs/import-export.md.
package openapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/codeen15/pyro/pkg/engine/importer"
	"github.com/codeen15/pyro/pkg/engine/model"
)

// Looks reports whether data is an OpenAPI 3 or Swagger 2 document.
func Looks(data []byte) bool {
	var probe struct {
		OpenAPI string `yaml:"openapi"`
		Swagger string `yaml:"swagger"`
	}
	if yaml.Unmarshal(data, &probe) != nil {
		return false
	}
	return strings.HasPrefix(probe.OpenAPI, "3") || probe.Swagger == "2.0"
}

var methods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

type converter struct {
	root    *yaml.Node
	v2      bool
	b       *importer.Bundle
	vars    map[string]bool
	schemes map[string]*yaml.Node
	consume []string // v2 global consumes
}

// Parse converts an OpenAPI document.
func Parse(data []byte) (*importer.Bundle, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("not valid JSON or YAML: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("not an OpenAPI document")
	}
	root := doc.Content[0]
	c := &converter{root: root, vars: map[string]bool{}, schemes: map[string]*yaml.Node{}}
	format := ""
	switch {
	case strings.HasPrefix(str(get(root, "openapi")), "3"):
		format = "openapi-" + str(get(root, "openapi"))
	case str(get(root, "swagger")) == "2.0":
		format, c.v2 = "swagger-2.0", true
	default:
		return nil, errors.New("not an OpenAPI 3 or Swagger 2.0 document")
	}
	info := get(root, "info")
	name := str(get(info, "title"))
	if name == "" {
		name = "OpenAPI import"
	}
	coll := &model.Collection{Name: name, Description: str(get(info, "description"))}
	c.b = &importer.Bundle{
		Collection: coll,
		Slug:       importer.Slugify(name),
		Folders:    map[string]*model.Collection{},
		Report:     importer.Report{Format: format, Collection: name, Warnings: []importer.Warning{}},
	}
	c.addVar("baseUrl", c.baseURL())

	if c.v2 {
		for _, k := range keys(get(root, "securityDefinitions")) {
			c.schemes[k] = get(get(root, "securityDefinitions"), k)
		}
		for _, v := range seq(get(root, "consumes")) {
			c.consume = append(c.consume, str(v))
		}
	} else {
		comps := get(get(root, "components"), "securitySchemes")
		for _, k := range keys(comps) {
			c.schemes[k] = c.deref(get(comps, k))
		}
	}
	coll.Auth = c.securityAuth(get(root, "security"), "")

	// Folders by first tag, in the order of the top-level tags list, then
	// first use.
	type folder struct {
		slug  string
		taken map[string]bool
		order []string
	}
	folders := map[string]*folder{}
	rootTaken := map[string]bool{}
	var rootOrder []string
	tagDesc := map[string]string{}
	for _, t := range seq(get(root, "tags")) {
		tag := str(get(t, "name"))
		tagDesc[tag] = str(get(t, "description"))
		slug := importer.Unique(importer.Slugify(tag), rootTaken)
		folders[tag] = &folder{slug: slug, taken: map[string]bool{}}
	}
	used := map[string]bool{}

	paths := get(root, "paths")
	for _, p := range keys(paths) {
		item := c.deref(get(paths, p))
		shared := seq(get(item, "parameters"))
		for _, m := range methods {
			op := get(item, m)
			if op == nil {
				continue
			}
			r, slugBase := c.operation(p, m, op, shared)
			tag := ""
			if tags := seq(get(op, "tags")); len(tags) > 0 {
				tag = str(tags[0])
			}
			var rel string
			if tag == "" {
				slug := importer.Unique(importer.Slugify(slugBase), rootTaken)
				rootOrder = append(rootOrder, slug)
				rel = slug
			} else {
				f := folders[tag]
				if f == nil {
					f = &folder{slug: importer.Unique(importer.Slugify(tag), rootTaken), taken: map[string]bool{}}
					folders[tag] = f
				}
				if !used[tag] {
					used[tag] = true
					rootOrder = append(rootOrder, f.slug)
				}
				slug := importer.Unique(importer.Slugify(slugBase), f.taken)
				f.order = append(f.order, slug)
				rel = f.slug + "/" + slug
			}
			c.b.Requests = append(c.b.Requests, importer.Request{Path: rel, Request: r})
			c.b.Report.Requests++
		}
	}
	if len(c.b.Requests) == 0 {
		return nil, errors.New("the document has no operations under paths")
	}
	// Folders in the tags-list order first, then by first use.
	var tagged []string
	for _, t := range seq(get(root, "tags")) {
		if tag := str(get(t, "name")); used[tag] {
			tagged = append(tagged, folders[tag].slug)
		}
	}
	order := tagged
	for _, s := range rootOrder {
		if !contains(order, s) {
			order = append(order, s)
		}
	}
	if len(order) > 1 {
		coll.Order = order
	}
	for tag, f := range folders {
		if !used[tag] {
			continue
		}
		fc := &model.Collection{Name: tag, Description: tagDesc[tag]}
		if len(f.order) > 1 {
			fc.Order = f.order
		}
		c.b.Folders[f.slug] = fc
		c.b.Report.Folders++
	}
	if get(root, "webhooks") != nil {
		c.b.Report.Warn("", "openapi.webhooks", "webhooks describe requests the API sends; they weren't imported")
	}
	c.b.Report.Slug = c.b.Slug
	return c.b, nil
}

func (c *converter) baseURL() string {
	if c.v2 {
		host := str(get(c.root, "host"))
		scheme := "https"
		if s := seq(get(c.root, "schemes")); len(s) > 0 {
			scheme = str(s[0])
		}
		if host == "" {
			c.b.Report.Warn("", "openapi.server", "no host given; set the baseUrl variable before sending")
			return "http://localhost" + str(get(c.root, "basePath"))
		}
		return scheme + "://" + host + strings.TrimRight(str(get(c.root, "basePath")), "/")
	}
	servers := seq(get(c.root, "servers"))
	if len(servers) == 0 {
		c.b.Report.Warn("", "openapi.server", "no servers listed; set the baseUrl variable before sending")
		return "http://localhost"
	}
	u := str(get(servers[0], "url"))
	vars := get(servers[0], "variables")
	for _, k := range keys(vars) {
		u = strings.ReplaceAll(u, "{"+k+"}", str(get(get(vars, k), "default")))
	}
	if len(servers) > 1 {
		var others []string
		for _, s := range servers[1:] {
			others = append(others, str(get(s, "url")))
		}
		c.b.Report.Warn("", "openapi.servers", "baseUrl is the first server; others: "+strings.Join(others, ", "))
	}
	if !strings.Contains(u, "://") {
		c.b.Report.Warn("", "openapi.server", fmt.Sprintf("server URL %q is relative; prefix baseUrl with the API's host", u))
	}
	return strings.TrimRight(u, "/")
}

var pathParam = regexp.MustCompile(`\{([^}/]+)\}`)

func (c *converter) operation(p, method string, op *yaml.Node, shared []*yaml.Node) (*model.Request, string) {
	where := strings.ToUpper(method) + " " + p
	r := &model.Request{Kind: model.KindHTTP, Method: strings.ToUpper(method)}
	r.Name = firstNonEmpty(str(get(op, "summary")), str(get(op, "operationId")), where)
	slugBase := firstNonEmpty(str(get(op, "operationId")), str(get(op, "summary")), method+" "+p)
	r.Description = str(get(op, "description"))
	if get(op, "deprecated") != nil && str(get(op, "deprecated")) == "true" {
		r.Description = strings.TrimSpace("Deprecated. " + r.Description)
	}
	r.URL = "{{baseUrl}}" + pathParam.ReplaceAllString(p, "{{$1}}")

	// Operation parameters override path-level ones with the same name+in.
	params := map[string]*yaml.Node{}
	var order []string
	for _, list := range [][]*yaml.Node{shared, seq(get(op, "parameters"))} {
		for _, prm := range list {
			prm = c.deref(prm)
			k := str(get(prm, "in")) + ":" + str(get(prm, "name"))
			if _, ok := params[k]; !ok {
				order = append(order, k)
			}
			params[k] = prm
		}
	}
	var cookies []string
	var formParams []*yaml.Node
	for _, k := range order {
		prm := params[k]
		name, in := str(get(prm, "name")), str(get(prm, "in"))
		required := str(get(prm, "required")) == "true"
		val := c.paramValue(prm)
		switch in {
		case "path":
			c.addVar(name, val)
		case "query":
			r.Query = append(r.Query, model.KeyValue{Key: name, Value: val, Disabled: !required})
		case "header":
			if l := strings.ToLower(name); l == "accept" || l == "content-type" || l == "authorization" {
				continue // described by content and security instead
			}
			r.Headers = append(r.Headers, model.KeyValue{Key: name, Value: val, Disabled: !required})
		case "cookie":
			cookies = append(cookies, name+"="+val)
		case "body":
			if sch := get(prm, "schema"); sch != nil {
				r.Body = &model.Body{Type: model.BodyJSON, Content: c.exampleJSON(sch)}
			}
		case "formData":
			formParams = append(formParams, prm)
		}
	}
	if len(cookies) > 0 {
		r.Headers = append(r.Headers, model.KeyValue{Key: "Cookie", Value: strings.Join(cookies, "; ")})
	}
	if len(formParams) > 0 {
		r.Body = c.v2Form(op, formParams)
	}
	if rb := get(op, "requestBody"); rb != nil {
		r.Body = c.requestBody(where, c.deref(rb))
	}
	if sec := get(op, "security"); sec != nil {
		if a := c.securityAuth(sec, where); a != nil {
			r.Auth = a
		} else {
			r.Auth = &model.Auth{Type: "none"}
		}
	}
	r.Examples = c.responseExamples(op)
	if get(op, "callbacks") != nil {
		c.b.Report.Warn(where, "openapi.callbacks", "callbacks weren't imported")
	}
	return r, slugBase
}

// responseExamples turns documented JSON responses into examples: the
// media example, else the first named example, else one generated from the
// schema (v2: the response schema or its examples).
func (c *converter) responseExamples(op *yaml.Node) []model.Example {
	var out []model.Example
	resps := get(op, "responses")
	for _, code := range keys(resps) {
		status, err := strconv.Atoi(code)
		if err != nil {
			continue // "default", "2XX"
		}
		rs := c.deref(get(resps, code))
		name := strings.TrimSpace(code + " " + str(get(rs, "description")))
		var body, ctype string
		if c.v2 {
			if ex := get(get(rs, "examples"), "application/json"); ex != nil {
				body = nodeJSON(ex)
			} else if sch := get(rs, "schema"); sch != nil {
				body = c.exampleJSONFor(sch, false)
			}
			ctype = "application/json"
		} else {
			content := get(rs, "content")
			for _, mt := range keys(content) {
				if mediaRank(mt) != 0 {
					continue
				}
				media := get(content, mt)
				switch {
				case get(media, "example") != nil:
					body = nodeJSON(get(media, "example"))
				case get(media, "examples") != nil && len(keys(get(media, "examples"))) > 0:
					body = nodeJSON(get(c.deref(get(get(media, "examples"), keys(get(media, "examples"))[0])), "value"))
				default:
					body = c.exampleJSONFor(get(media, "schema"), false)
				}
				ctype = mt
				break
			}
		}
		if body == "" {
			continue
		}
		out = append(out, model.Example{Name: name, Status: status, Headers: []model.KeyValue{{Key: "Content-Type", Value: ctype}}, Body: body})
	}
	return out
}

// paramValue picks an example value for a parameter.
func (c *converter) paramValue(prm *yaml.Node) string {
	if ex := get(prm, "example"); ex != nil {
		return scalarOrJSON(ex)
	}
	if exs := get(prm, "examples"); exs != nil {
		if ks := keys(exs); len(ks) > 0 {
			if v := get(c.deref(get(exs, ks[0])), "value"); v != nil {
				return scalarOrJSON(v)
			}
		}
	}
	sch := get(prm, "schema")
	if sch == nil && c.v2 {
		sch = prm // v2 non-body params carry type/default/enum directly
	}
	if sch == nil {
		return ""
	}
	v := c.example(sch, 0, map[string]bool{}, false)
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if arr, ok := v.([]any); ok && len(arr) > 0 {
		v = arr[0]
	}
	b, _ := json.Marshal(v)
	return strings.Trim(string(b), `"`)
}

// mediaPreference ranks request body media types.
func mediaRank(mt string) int {
	switch {
	case mt == "application/json" || strings.HasSuffix(mt, "+json"):
		return 0
	case mt == "application/x-www-form-urlencoded":
		return 1
	case mt == "multipart/form-data":
		return 2
	case strings.Contains(mt, "xml"):
		return 3
	case strings.HasPrefix(mt, "text/"):
		return 4
	}
	return 5
}

func (c *converter) requestBody(where string, rb *yaml.Node) *model.Body {
	content := get(rb, "content")
	types := keys(content)
	if len(types) == 0 {
		return nil
	}
	sort.SliceStable(types, func(i, j int) bool { return mediaRank(types[i]) < mediaRank(types[j]) })
	mt := types[0]
	media := get(content, mt)
	sch := get(media, "schema")
	var ex *yaml.Node
	if e := get(media, "example"); e != nil {
		ex = e
	} else if exs := get(media, "examples"); exs != nil {
		if ks := keys(exs); len(ks) > 0 {
			ex = get(c.deref(get(exs, ks[0])), "value")
		}
	}
	switch mediaRank(mt) {
	case 0:
		if ex != nil {
			return &model.Body{Type: model.BodyJSON, Content: nodeJSON(ex)}
		}
		return &model.Body{Type: model.BodyJSON, Content: c.exampleJSON(sch)}
	case 1, 2:
		t := model.BodyForm
		if mediaRank(mt) == 2 {
			t = model.BodyMultipart
		}
		body := &model.Body{Type: t}
		s := c.deref(sch)
		props := get(s, "properties")
		required := map[string]bool{}
		for _, n := range seq(get(s, "required")) {
			required[str(n)] = true
		}
		for _, k := range keys(props) {
			ps := c.deref(get(props, k))
			kv := model.KeyValue{Key: k, Disabled: len(required) > 0 && !required[k]}
			if f := str(get(ps, "format")); f == "binary" || f == "base64" {
				kv.File = t == model.BodyMultipart
				c.b.Report.Warn(where, "body.file", fmt.Sprintf("form field %q is a file; pick it before sending", k))
			} else if v := c.example(ps, 0, map[string]bool{}, true); v != nil {
				kv.Value = fmt.Sprint(v)
			}
			body.Fields = append(body.Fields, kv)
		}
		return body
	case 3:
		content := ""
		if ex != nil {
			content = str(ex)
		}
		return &model.Body{Type: model.BodyXML, Content: content}
	default:
		content := ""
		if ex != nil {
			content = scalarOrJSON(ex)
		}
		if mediaRank(mt) == 5 {
			c.b.Report.Warn(where, "body.media", fmt.Sprintf("request body media type %s was imported as text", mt))
		}
		return &model.Body{Type: model.BodyText, Content: content}
	}
}

func (c *converter) v2Form(op *yaml.Node, params []*yaml.Node) *model.Body {
	consumes := c.consume
	if cs := seq(get(op, "consumes")); len(cs) > 0 {
		consumes = nil
		for _, v := range cs {
			consumes = append(consumes, str(v))
		}
	}
	t := model.BodyForm
	for _, prm := range params {
		if str(get(prm, "type")) == "file" {
			t = model.BodyMultipart
		}
	}
	if contains(consumes, "multipart/form-data") {
		t = model.BodyMultipart
	}
	body := &model.Body{Type: t}
	for _, prm := range params {
		kv := model.KeyValue{Key: str(get(prm, "name")), Disabled: str(get(prm, "required")) != "true"}
		if str(get(prm, "type")) == "file" {
			kv.File = true
		} else {
			kv.Value = c.paramValue(prm)
		}
		body.Fields = append(body.Fields, kv)
	}
	return body
}

// securityAuth maps the first scheme of the first requirement to an auth.
// It returns nil for "no auth" (an empty list or empty requirement).
func (c *converter) securityAuth(sec *yaml.Node, where string) *model.Auth {
	reqs := seq(sec)
	if len(reqs) == 0 {
		return nil
	}
	names := keys(reqs[0])
	if len(names) == 0 {
		return nil
	}
	if len(reqs) > 1 || len(names) > 1 {
		c.b.Report.Warn(where, "auth.alternatives", fmt.Sprintf("several security schemes allowed; imported %s", names[0]))
	}
	s := c.schemes[names[0]]
	if s == nil {
		c.b.Report.Warn(where, "auth.unknown", fmt.Sprintf("security scheme %q isn't defined", names[0]))
		return nil
	}
	typ := str(get(s, "type"))
	switch {
	case typ == "http" && strings.EqualFold(str(get(s, "scheme")), "bearer"):
		c.addSecret("bearerToken")
		return &model.Auth{Type: "bearer", Params: map[string]string{"token": "{{bearerToken}}"}}
	case (typ == "http" && strings.EqualFold(str(get(s, "scheme")), "basic")) || typ == "basic":
		c.addVar("username", "")
		c.addSecret("password")
		return &model.Auth{Type: "basic", Params: map[string]string{"username": "{{username}}", "password": "{{password}}"}}
	case typ == "apiKey":
		in := str(get(s, "in"))
		v := importer.Slugify(names[0])
		v = strings.ReplaceAll(v, "-", "_")
		c.addSecret(v)
		if in == "cookie" {
			c.b.Report.Warn(where, "auth.cookie", fmt.Sprintf("API key %s is sent as a cookie; imported as a Cookie header via apikey auth", str(get(s, "name"))))
			return &model.Auth{Type: "apikey", Params: map[string]string{"key": "Cookie", "value": str(get(s, "name")) + "={{" + v + "}}", "in": "header"}}
		}
		if in != "query" {
			in = "header"
		}
		return &model.Auth{Type: "apikey", Params: map[string]string{"key": str(get(s, "name")), "value": "{{" + v + "}}", "in": in}}
	case typ == "oauth2" || typ == "openIdConnect":
		c.addSecret("accessToken")
		c.b.Report.Warn(where, "auth.oauth2", "OAuth 2.0: Pyro doesn't fetch tokens yet; set the accessToken variable")
		return &model.Auth{Type: "bearer", Params: map[string]string{"token": "{{accessToken}}"}}
	}
	c.b.Report.Warn(where, "auth.unsupported", fmt.Sprintf("security scheme type %q isn't supported", typ))
	return nil
}

func (c *converter) addVar(name, value string) {
	if c.vars[name] {
		return
	}
	c.vars[name] = true
	c.b.Collection.Variables = append(c.b.Collection.Variables, model.Variable{Key: name, Value: value})
}

func (c *converter) addSecret(name string) {
	if c.vars[name] {
		return
	}
	c.vars[name] = true
	c.b.Collection.Variables = append(c.b.Collection.Variables, model.Variable{Key: name, Secret: true})
}

// --- $ref and examples ---------------------------------------------------------

// deref follows local $refs ("#/components/…"). External refs are reported
// and left unresolved.
func (c *converter) deref(n *yaml.Node) *yaml.Node {
	for i := 0; i < 32 && n != nil; i++ {
		ref := str(get(n, "$ref"))
		if ref == "" {
			return n
		}
		target := c.resolve(ref)
		if target == nil {
			return nil
		}
		n = target
	}
	return n
}

func (c *converter) resolve(ref string) *yaml.Node {
	if !strings.HasPrefix(ref, "#/") {
		c.b.Report.Warn("", "openapi.external-ref", fmt.Sprintf("external reference %s isn't followed", ref))
		return nil
	}
	n := c.root
	for _, part := range strings.Split(ref[2:], "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		n = get(n, part)
		if n == nil {
			c.b.Report.Warn("", "openapi.ref", fmt.Sprintf("reference %s not found", ref))
			return nil
		}
	}
	return n
}

func (c *converter) exampleJSON(sch *yaml.Node) string { return c.exampleJSONFor(sch, true) }

// exampleJSONFor renders a schema example for a request body (readOnly
// properties left out) or a response (all properties).
func (c *converter) exampleJSONFor(sch *yaml.Node, request bool) string {
	v := c.example(sch, 0, map[string]bool{}, request)
	if v == nil {
		return ""
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if enc.Encode(v) != nil {
		return ""
	}
	return strings.TrimRight(buf.String(), "\n")
}

// example builds a sample value from a schema, keeping property order.
// request skips readOnly properties.
func (c *converter) example(n *yaml.Node, depth int, seen map[string]bool, request bool) any {
	if n == nil || depth > 8 {
		return nil
	}
	if ref := str(get(n, "$ref")); ref != "" {
		if seen[ref] {
			return nil // recursive schema
		}
		seen[ref] = true
		defer delete(seen, ref)
		return c.example(c.resolve(ref), depth, seen, request)
	}
	if ex := get(n, "example"); ex != nil {
		return nodeValue(ex)
	}
	if exs := seq(get(n, "examples")); len(exs) > 0 { // 3.1 schema examples
		return nodeValue(exs[0])
	}
	if d := get(n, "default"); d != nil {
		return nodeValue(d)
	}
	if e := seq(get(n, "enum")); len(e) > 0 {
		return nodeValue(e[0])
	}
	if cst := get(n, "const"); cst != nil {
		return nodeValue(cst)
	}
	if all := seq(get(n, "allOf")); len(all) > 0 {
		merged := &ordered{vals: map[string]any{}}
		for _, part := range all {
			if o, ok := c.example(part, depth+1, seen, request).(*ordered); ok {
				for _, k := range o.keys {
					merged.set(k, o.vals[k])
				}
			}
		}
		if len(merged.keys) > 0 {
			return merged
		}
	}
	for _, alt := range []string{"oneOf", "anyOf"} {
		if opts := seq(get(n, alt)); len(opts) > 0 {
			return c.example(opts[0], depth+1, seen, request)
		}
	}
	typ := str(get(n, "type"))
	if t := seq(get(n, "type")); len(t) > 0 { // 3.1: type: [string, "null"]
		typ = str(t[0])
		if typ == "null" && len(t) > 1 {
			typ = str(t[1])
		}
	}
	if typ == "" && get(n, "properties") != nil {
		typ = "object"
	}
	switch typ {
	case "object":
		o := &ordered{vals: map[string]any{}}
		props := get(n, "properties")
		for _, k := range keys(props) {
			p := get(props, k)
			if request && str(get(c.deref(p), "readOnly")) == "true" {
				continue
			}
			o.set(k, c.example(p, depth+1, seen, request))
		}
		return o
	case "array":
		if item := c.example(get(n, "items"), depth+1, seen, request); item != nil {
			return []any{item}
		}
		return []any{}
	case "integer":
		return 0
	case "number":
		return 0
	case "boolean":
		return true
	case "string":
		switch str(get(n, "format")) {
		case "date-time":
			return "2026-01-01T00:00:00Z"
		case "date":
			return "2026-01-01"
		case "uuid":
			return "00000000-0000-0000-0000-000000000000"
		case "email":
			return "user@example.com"
		case "uri", "url":
			return "https://example.com"
		case "ipv4":
			return "192.0.2.1"
		}
		return "string"
	}
	return nil
}

// ordered is a JSON object that keeps insertion order.
type ordered struct {
	keys []string
	vals map[string]any
}

func (o *ordered) set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *ordered) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(o.vals[k])
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// --- yaml.Node helpers -----------------------------------------------------------

func get(n *yaml.Node, key string) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			v := n.Content[i+1]
			if v.Kind == yaml.AliasNode {
				return v.Alias
			}
			return v
		}
	}
	return nil
}

func keys(n *yaml.Node) []string {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	out := make([]string, 0, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		out = append(out, n.Content[i].Value)
	}
	return out
}

func seq(n *yaml.Node) []*yaml.Node {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	return n.Content
}

func str(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}

// nodeValue converts a node to plain values, keeping mapping order.
func nodeValue(n *yaml.Node) any {
	switch n.Kind {
	case yaml.AliasNode:
		return nodeValue(n.Alias)
	case yaml.MappingNode:
		o := &ordered{vals: map[string]any{}}
		for i := 0; i+1 < len(n.Content); i += 2 {
			o.set(n.Content[i].Value, nodeValue(n.Content[i+1]))
		}
		return o
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			out = append(out, nodeValue(c))
		}
		return out
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!int":
			if i, err := strconv.ParseInt(n.Value, 0, 64); err == nil {
				return i
			}
		case "!!float":
			if f, err := strconv.ParseFloat(n.Value, 64); err == nil {
				return f
			}
		case "!!bool":
			return n.Value == "true"
		case "!!null":
			return nil
		}
		return n.Value
	}
	return nil
}

func nodeJSON(n *yaml.Node) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if enc.Encode(nodeValue(n)) != nil {
		return ""
	}
	return strings.TrimRight(buf.String(), "\n")
}

func scalarOrJSON(n *yaml.Node) string {
	if n.Kind == yaml.ScalarNode {
		return n.Value
	}
	b, _ := json.Marshal(nodeValue(n))
	return string(b)
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
