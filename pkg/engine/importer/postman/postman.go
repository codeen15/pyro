// Package postman imports Postman Collection v2.0/v2.1 and environment exports.
// See docs/postman-import.md for the mapping.
package postman

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/codeen15/pyro/pkg/engine/importer"
	"github.com/codeen15/pyro/pkg/engine/model"
)

// Parse detects whether data is a collection or an environment export and converts it.
func Parse(data []byte) (*importer.Bundle, error) {
	var probe struct {
		Info     json.RawMessage `json:"info"`
		Values   json.RawMessage `json:"values"`
		Requests json.RawMessage `json:"requests"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("not a valid JSON file: %w", err)
	}
	switch {
	case probe.Info != nil:
		return parseCollection(data)
	case probe.Values != nil:
		return parseEnvironment(data)
	case probe.Requests != nil:
		return nil, errors.New("Postman v1 collections are not supported: re-export the collection as v2.1 from Postman")
	}
	return nil, errors.New("not a Postman collection or environment export")
}

// --- Postman JSON shapes -------------------------------------------------

type pmCollection struct {
	Info struct {
		Name        string      `json:"name"`
		Schema      string      `json:"schema"`
		Description description `json:"description"`
	} `json:"info"`
	Item     []pmItem     `json:"item"`
	Auth     *pmAuth      `json:"auth"`
	Event    []pmEvent    `json:"event"`
	Variable []pmVariable `json:"variable"`
}

type pmItem struct {
	Name        string          `json:"name"`
	Description description     `json:"description"`
	Item        []pmItem        `json:"item"`
	Request     json.RawMessage `json:"request"`
	Auth        *pmAuth         `json:"auth"`
	Event       []pmEvent       `json:"event"`
	Response    []pmResponse    `json:"response"`
	Protocol    map[string]any  `json:"protocolProfileBehavior"`
}

type pmResponse struct {
	Name   string          `json:"name"`
	Code   int             `json:"code"`
	Header json.RawMessage `json:"header"`
	Body   string          `json:"body"`
}

type pmRequest struct {
	Method      string          `json:"method"`
	Header      json.RawMessage `json:"header"`
	Body        *pmBody         `json:"body"`
	URL         json.RawMessage `json:"url"`
	Auth        *pmAuth         `json:"auth"`
	Description description     `json:"description"`
}

type pmURL struct {
	Raw      string       `json:"raw"`
	Query    []pmKV       `json:"query"`
	Variable []pmVariable `json:"variable"`
}

type pmKV struct {
	Key         string          `json:"key"`
	Value       json.RawMessage `json:"value"`
	Disabled    bool            `json:"disabled"`
	Type        string          `json:"type"`
	Src         json.RawMessage `json:"src"`
	Description description     `json:"description"`
}

type pmBody struct {
	Mode       string                `json:"mode"`
	Raw        string                `json:"raw"`
	URLEncoded []pmKV                `json:"urlencoded"`
	FormData   []pmKV                `json:"formdata"`
	File       *struct{ Src string } `json:"file"`
	GraphQL    *struct {
		Query     string          `json:"query"`
		Variables json.RawMessage `json:"variables"`
	} `json:"graphql"`
	Options struct {
		Raw struct {
			Language string `json:"language"`
		} `json:"raw"`
	} `json:"options"`
	Disabled bool `json:"disabled"`
}

type pmAuth struct {
	Type   string
	Params map[string]string
}

// UnmarshalJSON accepts both v2.1 ({"type":"basic","basic":[{key,value}]})
// and v2.0 ({"type":"basic","basic":{"username":…}}) auth shapes.
func (a *pmAuth) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if err := json.Unmarshal(raw["type"], &a.Type); err != nil {
		return fmt.Errorf("auth.type: %w", err)
	}
	a.Params = map[string]string{}
	params := raw[a.Type]
	if params == nil {
		return nil
	}
	var list []pmKV
	if json.Unmarshal(params, &list) == nil {
		for _, kv := range list {
			a.Params[kv.Key] = scalar(kv.Value)
		}
		return nil
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(params, &obj) == nil {
		for k, v := range obj {
			a.Params[k] = scalar(v)
		}
	}
	return nil
}

type pmEvent struct {
	Listen string `json:"listen"`
	Script struct {
		Exec json.RawMessage `json:"exec"`
	} `json:"script"`
	Disabled bool `json:"disabled"`
}

type pmVariable struct {
	Key      string          `json:"key"`
	Value    json.RawMessage `json:"value"`
	Type     string          `json:"type"`
	Disabled bool            `json:"disabled"`
	Enabled  *bool           `json:"enabled"` // environments use enabled instead of disabled
}

// description is either a string or {"content": "...", "type": "text/markdown"}.
type description string

func (d *description) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*d = description(s)
		return nil
	}
	var obj struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return nil // unknown shape: ignore rather than fail the import
	}
	*d = description(obj.Content)
	return nil
}

// scalar renders a JSON value (string, number, bool, null) as a string.
func scalar(v json.RawMessage) string {
	if len(v) == 0 || string(v) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	return string(bytes.TrimSpace(v))
}

// --- Collections ----------------------------------------------------------

type converter struct {
	b       *importer.Bundle
	scripts int
}

func parseCollection(data []byte) (*importer.Bundle, error) {
	var pc pmCollection
	if err := json.Unmarshal(data, &pc); err != nil {
		return nil, fmt.Errorf("invalid Postman collection: %w", err)
	}
	format := "postman-v2.1"
	switch {
	case strings.Contains(pc.Info.Schema, "v2.0"):
		format = "postman-v2.0"
	case strings.Contains(pc.Info.Schema, "v2.1"), pc.Info.Schema == "":
	default:
		return nil, fmt.Errorf("unsupported Postman schema %q", pc.Info.Schema)
	}
	name := pc.Info.Name
	if name == "" {
		name = "Imported collection"
	}

	c := &converter{b: &importer.Bundle{
		Slug:    importer.Slugify(name),
		Folders: map[string]*model.Collection{},
		Report:  importer.Report{Format: format, Collection: name, Warnings: []importer.Warning{}},
	}}
	coll := &model.Collection{
		Name:        name,
		Description: string(pc.Info.Description),
		Variables:   variables(pc.Variable),
		Auth:        c.auth("", pc.Auth),
		Scripts:     c.scriptsFrom("", pc.Event),
	}
	c.b.Collection = coll
	if coll.Order = c.items("", pc.Item); len(coll.Order) < 2 {
		coll.Order = nil
	}
	c.b.Report.Slug = c.b.Slug

	if c.scripts > 0 {
		c.b.Report.Warn("", "script.deferred",
			fmt.Sprintf("%d script(s) were imported; script execution arrives in the next Pyro release", c.scripts))
	}
	return c.b, nil
}

// items converts one level of the tree and returns the slugs in source order.
func (c *converter) items(dir string, items []pmItem) []string {
	taken := map[string]bool{}
	var order []string
	for _, it := range items {
		slug := importer.Unique(importer.Slugify(it.Name), taken)
		order = append(order, slug)
		p := path.Join(dir, slug)
		if it.Request == nil {
			// A folder (Postman marks folders by having "item", possibly empty).
			c.b.Report.Folders++
			f := &model.Collection{
				Name:        it.Name,
				Description: string(it.Description),
				Auth:        c.auth(p, it.Auth),
				Scripts:     c.scriptsFrom(p, it.Event),
			}
			if f.Order = c.items(p, it.Item); len(f.Order) < 2 {
				f.Order = nil
			}
			if f.Description != "" || f.Auth != nil || f.Scripts != nil || f.Name != slug || f.Order != nil {
				c.b.Folders[p] = f
			}
			continue
		}
		if r := c.request(p, it); r != nil {
			c.b.Requests = append(c.b.Requests, importer.Request{Path: p, Request: r})
			c.b.Report.Requests++
		}
	}
	return order
}

func (c *converter) request(p string, it pmItem) *model.Request {
	var pr pmRequest
	var rawURL string
	if json.Unmarshal(it.Request, &rawURL) == nil {
		pr.URL, _ = json.Marshal(rawURL) // v2.1 shorthand: "request": "https://…"
		pr.Method = "GET"
	} else if err := json.Unmarshal(it.Request, &pr); err != nil {
		c.b.Report.Warn(p, "request.invalid", "skipped: "+err.Error())
		return nil
	}

	r := &model.Request{
		Kind:        model.KindHTTP,
		Name:        it.Name,
		Description: string(pr.Description),
		Method:      strings.ToUpper(pr.Method),
		Auth:        c.auth(p, pr.Auth),
		Scripts:     c.scriptsFrom(p, it.Event),
	}
	if r.Description == "" {
		r.Description = string(it.Description)
	}
	if r.Method == "" {
		r.Method = "GET"
	}
	r.URL, r.Query = c.url(p, pr.URL)
	r.Headers = c.headers(p, pr.Header)
	r.Body = c.body(p, pr.Body)

	for _, ex := range it.Response {
		r.Examples = append(r.Examples, model.Example{Name: ex.Name, Status: ex.Code, Headers: c.headers(p, ex.Header), Body: ex.Body})
	}
	if v, ok := it.Protocol["followRedirects"]; ok && v == false {
		c.b.Report.Warn(p, "behavior.redirects", "Postman setting 'follow redirects: off' is not per-request in Pyro yet")
	}
	return r
}

var pathVar = regexp.MustCompile(`/:([A-Za-z_][A-Za-z0-9_-]*)`)

// url converts a Postman URL (string or object). Query parameters move into
// the structured list, keeping disabled ones, and :pathVariables are
// substituted with their saved values.
func (c *converter) url(p string, raw json.RawMessage) (string, []model.KeyValue) {
	if raw == nil {
		return "", nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, nil
	}
	var u pmURL
	if err := json.Unmarshal(raw, &u); err != nil {
		c.b.Report.Warn(p, "url.invalid", "could not read URL: "+err.Error())
		return "", nil
	}
	out := u.Raw
	var query []model.KeyValue
	if len(u.Query) > 0 {
		if i := strings.IndexByte(out, '?'); i >= 0 {
			out = out[:i]
		}
		for _, q := range u.Query {
			query = append(query, model.KeyValue{Key: q.Key, Value: scalar(q.Value), Disabled: q.Disabled})
		}
	}
	if len(u.Variable) > 0 {
		vals := map[string]string{}
		for _, v := range u.Variable {
			vals[v.Key] = scalar(v.Value)
		}
		out = pathVar.ReplaceAllStringFunc(out, func(m string) string {
			name := m[2:]
			if v, ok := vals[name]; ok && v != "" {
				return "/" + v
			}
			c.b.Report.Warn(p, "url.pathvar", fmt.Sprintf("path variable :%s has no value; left in the URL", name))
			return m
		})
	}
	return out, query
}

func (c *converter) headers(p string, raw json.RawMessage) []model.KeyValue {
	if raw == nil {
		return nil
	}
	var list []pmKV
	if err := json.Unmarshal(raw, &list); err != nil {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return parseHeaderBlock(s)
		}
		c.b.Report.Warn(p, "header.invalid", "could not read headers")
		return nil
	}
	out := make([]model.KeyValue, 0, len(list))
	for _, h := range list {
		out = append(out, model.KeyValue{Key: h.Key, Value: scalar(h.Value), Disabled: h.Disabled})
	}
	return out
}

// parseHeaderBlock handles v2.0's "Key: value\nKey2: value2" header string.
func parseHeaderBlock(s string) []model.KeyValue {
	var out []model.KeyValue
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(k) != "" {
			out = append(out, model.KeyValue{Key: strings.TrimSpace(k), Value: strings.TrimSpace(v)})
		}
	}
	return out
}

func (c *converter) body(p string, b *pmBody) *model.Body {
	if b == nil || b.Mode == "" || b.Disabled {
		return nil
	}
	switch b.Mode {
	case "raw":
		if b.Raw == "" {
			return nil
		}
		t := model.BodyText
		switch strings.ToLower(b.Options.Raw.Language) {
		case "json":
			t = model.BodyJSON
		case "xml":
			t = model.BodyXML
		}
		return &model.Body{Type: t, Content: b.Raw}
	case "urlencoded":
		return &model.Body{Type: model.BodyForm, Fields: kvs(b.URLEncoded)}
	case "formdata":
		fields := make([]model.KeyValue, 0, len(b.FormData))
		for _, f := range b.FormData {
			kv := model.KeyValue{Key: f.Key, Value: scalar(f.Value), Disabled: f.Disabled}
			if f.Type == "file" {
				kv.File = true
				kv.Value = firstSrc(f.Src)
				if kv.Value == "" {
					c.b.Report.Warn(p, "body.file", fmt.Sprintf("form-data file field %q has no file selected", f.Key))
				} else {
					c.b.Report.Warn(p, "body.file", fmt.Sprintf("form-data file field %q refers to %s; copy the file into the workspace (files outside it are blocked by default)", f.Key, kv.Value))
				}
			}
			fields = append(fields, kv)
		}
		return &model.Body{Type: model.BodyMultipart, Fields: fields}
	case "file":
		src := ""
		if b.File != nil {
			src = b.File.Src
		}
		c.b.Report.Warn(p, "body.file", fmt.Sprintf("binary body refers to %q; copy the file into the workspace (files outside it are blocked by default)", src))
		return &model.Body{Type: model.BodyBinaryFile, File: src}
	case "graphql":
		if b.GraphQL == nil {
			return nil
		}
		vars := scalar(b.GraphQL.Variables)
		return &model.Body{Type: model.BodyGraphQL, Content: b.GraphQL.Query, Variables: vars}
	}
	c.b.Report.Warn(p, "body.unsupported", fmt.Sprintf("body mode %q not supported", b.Mode))
	return nil
}

// firstSrc handles formdata "src", which is a string, an array, or null.
func firstSrc(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil && len(list) > 0 {
		return list[0]
	}
	return ""
}

var supportedAuth = map[string]bool{"basic": true, "bearer": true, "apikey": true, "oauth2": true, "digest": true}

func (c *converter) auth(p string, a *pmAuth) *model.Auth {
	if a == nil || a.Type == "" || a.Type == "inherit" {
		return nil
	}
	if a.Type == "noauth" {
		return &model.Auth{Type: "none"}
	}
	out := &model.Auth{Type: a.Type, Params: map[string]string{}}
	switch a.Type {
	case "apikey":
		out.Params["key"], out.Params["value"] = a.Params["key"], a.Params["value"]
		out.Params["in"] = a.Params["in"]
		if out.Params["in"] == "" {
			out.Params["in"] = "header"
		}
	case "oauth2":
		// Keep every setting so token fetching can use them later.
		for k, v := range a.Params {
			if v != "" && k != "tokenType" {
				out.Params[k] = v
			}
		}
	default:
		for k, v := range a.Params {
			out.Params[k] = v
		}
	}
	if len(out.Params) == 0 {
		out.Params = nil
	}
	if !supportedAuth[a.Type] {
		c.b.Report.Warn(p, "auth.unsupported", fmt.Sprintf("%s auth was imported but Pyro can't send it yet", a.Type))
	} else if a.Type == "oauth2" && a.Params["accessToken"] == "" {
		c.b.Report.Warn(p, "auth.oauth2", "OAuth 2.0 has no saved access token; paste one before sending")
	}
	return out
}

// unsupportedScriptAPIs are flagged on import; see docs/postman-import.md.
var unsupportedScriptAPIs = []string{"pm.vault", "pm.visualizer", "pm.cookies.jar"}

func (c *converter) scriptsFrom(p string, events []pmEvent) *model.Scripts {
	var s model.Scripts
	for _, e := range events {
		if e.Disabled {
			continue
		}
		code := execString(e.Script.Exec)
		if strings.TrimSpace(code) == "" {
			continue
		}
		switch e.Listen {
		case "prerequest":
			s.Pre = code
		case "test":
			s.Post = code
		default:
			continue
		}
		c.scripts++
		for _, api := range unsupportedScriptAPIs {
			if strings.Contains(code, api) {
				c.b.Report.Warn(p, "script.api", api+" is not supported")
			}
		}
	}
	if s.Pre == "" && s.Post == "" {
		return nil
	}
	return &s
}

func execString(raw json.RawMessage) string {
	var lines []string
	if json.Unmarshal(raw, &lines) == nil {
		return strings.Join(lines, "\n")
	}
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func kvs(list []pmKV) []model.KeyValue {
	out := make([]model.KeyValue, 0, len(list))
	for _, kv := range list {
		out = append(out, model.KeyValue{Key: kv.Key, Value: scalar(kv.Value), Disabled: kv.Disabled})
	}
	return out
}

func variables(list []pmVariable) []model.Variable {
	if len(list) == 0 {
		return nil
	}
	out := make([]model.Variable, 0, len(list))
	for _, v := range list {
		disabled := v.Disabled || (v.Enabled != nil && !*v.Enabled)
		out = append(out, model.Variable{Key: v.Key, Value: scalar(v.Value), Secret: v.Type == "secret", Disabled: disabled})
	}
	return out
}

// --- Environments -----------------------------------------------------------

func parseEnvironment(data []byte) (*importer.Bundle, error) {
	var pe struct {
		Name   string       `json:"name"`
		Values []pmVariable `json:"values"`
		Scope  string       `json:"_postman_variable_scope"`
	}
	if err := json.Unmarshal(data, &pe); err != nil {
		return nil, fmt.Errorf("invalid Postman environment: %w", err)
	}
	name := pe.Name
	if name == "" {
		name = pe.Scope
	}
	if name == "" {
		name = "imported"
	}
	env := &model.Environment{Name: name, Variables: variables(pe.Values)}
	b := &importer.Bundle{
		Environments: []*model.Environment{env},
		Report: importer.Report{
			Format:       "postman-environment",
			Environments: []string{name},
			Warnings:     []importer.Warning{},
		},
	}
	if pe.Scope == "globals" {
		b.Report.Warn("", "globals.as-env", "Postman globals were imported as an environment; workspace globals arrive later")
	}
	return b, nil
}
