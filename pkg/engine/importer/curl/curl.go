// Package curl imports cURL commands (as copied from browser dev tools,
// API docs or a terminal). Several commands in one input become one
// collection. Supported options are listed in docs/import-export.md.
package curl

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/codeen15/pyro/pkg/engine/importer"
	"github.com/codeen15/pyro/pkg/engine/model"
)

// Looks reports whether data looks like one or more cURL commands.
func Looks(data []byte) bool {
	s := strings.TrimSpace(string(data))
	return strings.HasPrefix(s, "curl ") || strings.HasPrefix(s, "curl.exe ") || strings.HasPrefix(s, "$ curl ")
}

// Parse converts every cURL command in data into requests of one collection
// named name.
func Parse(name string, data []byte) (*importer.Bundle, error) {
	cmds, err := split(string(data))
	if err != nil {
		return nil, err
	}
	if len(cmds) == 0 {
		return nil, errors.New("no curl command found")
	}
	if name == "" {
		name = "cURL import"
	}
	b := &importer.Bundle{
		Collection: &model.Collection{Name: name},
		Slug:       importer.Slugify(name),
		Report:     importer.Report{Format: "curl", Collection: name, Warnings: []importer.Warning{}},
	}
	taken := map[string]bool{}
	var order []string
	for i, args := range cmds {
		r, warns, err := convert(args)
		where := fmt.Sprintf("command %d", i+1)
		if err != nil {
			b.Report.Warn(where, "curl.invalid", "skipped: "+err.Error())
			continue
		}
		slug := importer.Unique(importer.Slugify(r.Name), taken)
		order = append(order, slug)
		for _, w := range warns {
			b.Report.Warn(slug, w[0], w[1])
		}
		b.Requests = append(b.Requests, importer.Request{Path: slug, Request: r})
		b.Report.Requests++
	}
	if len(b.Requests) == 0 {
		return nil, fmt.Errorf("no curl command could be imported: %s", b.Report.Warnings[0].Detail)
	}
	if len(order) > 1 {
		b.Collection.Order = order
	}
	b.Report.Slug = b.Slug
	return b, nil
}

// split tokenizes the input like a POSIX shell (quotes, backslash escapes,
// line continuations, $'…' strings) and splits it into commands at each
// "curl" word that starts a line.
func split(s string) ([][]string, error) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	// Windows cmd continuation (^ at end of line) and PowerShell backtick.
	s = strings.ReplaceAll(s, "^\n", " ")
	s = strings.ReplaceAll(s, "`\n", " ")
	var (
		cmds    [][]string
		cur     []string
		tok     strings.Builder
		inTok   bool
		lineNew = true
	)
	flush := func() {
		if inTok {
			word := tok.String()
			if lineNew && (word == "curl" || word == "curl.exe") && len(cur) > 0 {
				cmds = append(cmds, cur)
				cur = nil
			}
			if !(len(cur) == 0 && word == "$") {
				cur = append(cur, word)
			}
			tok.Reset()
			inTok = false
			lineNew = false
		}
	}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '\\' && i+1 < len(rs) && rs[i+1] == '\n':
			i++ // continuation
		case c == '\n':
			flush()
			lineNew = true
		case c == ' ' || c == '\t':
			flush()
		case c == '\'':
			inTok = true
			j := i + 1
			for j < len(rs) && rs[j] != '\'' {
				tok.WriteRune(rs[j])
				j++
			}
			if j >= len(rs) {
				return nil, errors.New("unterminated single quote")
			}
			i = j
		case c == '$' && i+1 < len(rs) && rs[i+1] == '\'':
			inTok = true
			j := i + 2
			for ; j < len(rs) && rs[j] != '\''; j++ {
				if rs[j] == '\\' && j+1 < len(rs) {
					j++
					switch rs[j] {
					case 'n':
						tok.WriteRune('\n')
					case 't':
						tok.WriteRune('\t')
					case 'r':
						tok.WriteRune('\r')
					default:
						tok.WriteRune(rs[j])
					}
					continue
				}
				tok.WriteRune(rs[j])
			}
			if j >= len(rs) {
				return nil, errors.New("unterminated $'…' string")
			}
			i = j
		case c == '"':
			inTok = true
			j := i + 1
			for ; j < len(rs) && rs[j] != '"'; j++ {
				if rs[j] == '\\' && j+1 < len(rs) && strings.ContainsRune("\"\\$`\n", rs[j+1]) {
					j++
					if rs[j] == '\n' {
						continue
					}
				}
				tok.WriteRune(rs[j])
			}
			if j >= len(rs) {
				return nil, errors.New("unterminated double quote")
			}
			i = j
		case c == '\\' && i+1 < len(rs):
			inTok = true
			i++
			tok.WriteRune(rs[i])
		default:
			inTok = true
			tok.WriteRune(c)
		}
	}
	flush()
	if len(cur) > 0 {
		cmds = append(cmds, cur)
	}
	// Drop anything before the first curl word (e.g. a stray prompt).
	out := cmds[:0]
	for _, c := range cmds {
		if len(c) > 0 && (c[0] == "curl" || c[0] == "curl.exe") {
			out = append(out, c[1:])
		}
	}
	return out, nil
}

// ignored options take no value and don't change the request.
var ignored = map[string]bool{
	"-s": true, "--silent": true, "-S": true, "--show-error": true, "-v": true, "--verbose": true, "-i": true,
	"--include": true, "-L": true, "--location": true, "--compressed": true, "-k": true, "--insecure": true,
	"-f": true, "--fail": true, "-g": true, "--globoff": true, "--http1.1": true, "--http2": true, "-N": true,
	"--no-buffer": true, "-#": true, "--progress-bar": true, "-O": true, "--remote-name": true, "-J": true,
}

// ignoredWithValue options take a value we don't use.
var ignoredWithValue = map[string]bool{
	"-o": true, "--output": true, "-m": true, "--max-time": true, "--connect-timeout": true, "-w": true,
	"--write-out": true, "--retry": true, "-x": true, "--proxy": true, "--cacert": true, "--cert": true, "-E": true,
	"--key": true, "-c": true, "--cookie-jar": true, "--resolve": true, "--limit-rate": true,
}

func convert(args []string) (*model.Request, [][2]string, error) {
	r := &model.Request{Kind: model.KindHTTP}
	var (
		warns    [][2]string
		data     []string
		form     []model.KeyValue
		rawURL   string
		getMode  bool
		jsonFlag bool
		dataJSON bool
	)
	warn := func(code, detail string) { warns = append(warns, [2]string{code, detail}) }
	header := func(k, v string) { r.Headers = append(r.Headers, model.KeyValue{Key: k, Value: v}) }
	for i := 0; i < len(args); i++ {
		a := args[i]
		val := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", a)
			}
			i++
			return args[i], nil
		}
		// --opt=value form.
		if strings.HasPrefix(a, "--") {
			if k, v, ok := strings.Cut(a, "="); ok {
				args = append(args[:i+1], append([]string{v}, args[i+1:]...)...)
				a = k
			}
		}
		// -XPOST / -HContent-Type… attached short options.
		if len(a) > 2 && a[0] == '-' && a[1] != '-' && strings.ContainsRune("XHdFuAebT", rune(a[1])) {
			args = append(args[:i+1], append([]string{a[2:]}, args[i+1:]...)...)
			a = a[:2]
		}
		switch {
		case a == "-X" || a == "--request":
			v, err := val()
			if err != nil {
				return nil, nil, err
			}
			r.Method = strings.ToUpper(v)
		case a == "-H" || a == "--header":
			v, err := val()
			if err != nil {
				return nil, nil, err
			}
			k, hv, ok := strings.Cut(v, ":")
			if !ok {
				warn("curl.header", fmt.Sprintf("header %q has no value", v))
				continue
			}
			header(strings.TrimSpace(k), strings.TrimSpace(hv))
		case a == "-d" || a == "--data" || a == "--data-raw" || a == "--data-binary" || a == "--data-ascii" || a == "--data-urlencode" || a == "--json":
			v, err := val()
			if err != nil {
				return nil, nil, err
			}
			if strings.HasPrefix(v, "@") && a != "--data-raw" {
				warn("curl.data-file", fmt.Sprintf("body is read from file %s; paste its contents into the body", v[1:]))
				continue
			}
			if a == "--data-urlencode" {
				if k, rest, ok := strings.Cut(v, "="); ok {
					v = k + "=" + url.QueryEscape(rest)
				} else {
					v = url.QueryEscape(v)
				}
			}
			if a == "--json" {
				jsonFlag = true
			}
			data = append(data, v)
		case a == "-F" || a == "--form" || a == "--form-string":
			v, err := val()
			if err != nil {
				return nil, nil, err
			}
			k, fv, _ := strings.Cut(v, "=")
			kv := model.KeyValue{Key: k, Value: fv}
			if a != "--form-string" && strings.HasPrefix(fv, "@") {
				kv.File, kv.Value = true, strings.SplitN(fv[1:], ";", 2)[0]
				warn("body.file", fmt.Sprintf("form field %q uploads %s; check the path exists on this machine", k, kv.Value))
			} else if a != "--form-string" && strings.HasPrefix(fv, "<") {
				warn("body.file", fmt.Sprintf("form field %q is read from %s; paste its contents", k, fv[1:]))
			}
			form = append(form, kv)
		case a == "-u" || a == "--user":
			v, err := val()
			if err != nil {
				return nil, nil, err
			}
			user, pass, _ := strings.Cut(v, ":")
			r.Auth = &model.Auth{Type: "basic", Params: map[string]string{"username": user, "password": pass}}
		case a == "-b" || a == "--cookie":
			v, err := val()
			if err != nil {
				return nil, nil, err
			}
			if !strings.Contains(v, "=") {
				warn("curl.cookie-file", "cookies read from a file aren't imported")
				continue
			}
			header("Cookie", v)
		case a == "-A" || a == "--user-agent":
			v, err := val()
			if err != nil {
				return nil, nil, err
			}
			header("User-Agent", v)
		case a == "-e" || a == "--referer":
			v, err := val()
			if err != nil {
				return nil, nil, err
			}
			header("Referer", v)
		case a == "-G" || a == "--get":
			getMode = true
		case a == "-I" || a == "--head":
			r.Method = "HEAD"
		case a == "--url":
			v, err := val()
			if err != nil {
				return nil, nil, err
			}
			rawURL = v
		case a == "-T" || a == "--upload-file":
			v, err := val()
			if err != nil {
				return nil, nil, err
			}
			r.Body = &model.Body{Type: model.BodyBinaryFile, File: v}
			if r.Method == "" {
				r.Method = "PUT"
			}
			warn("body.file", fmt.Sprintf("uploads %s; check the path exists on this machine", v))
		case a == "--oauth2-bearer":
			v, err := val()
			if err != nil {
				return nil, nil, err
			}
			r.Auth = &model.Auth{Type: "bearer", Params: map[string]string{"token": v}}
		case ignored[a]:
		case ignoredWithValue[a]:
			if _, err := val(); err != nil {
				return nil, nil, err
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			warn("curl.option", fmt.Sprintf("option %s was ignored", a))
		default:
			if rawURL == "" {
				rawURL = a
			} else {
				warn("curl.url", fmt.Sprintf("extra URL %s was ignored (one request per command)", a))
			}
		}
	}
	if rawURL == "" {
		return nil, nil, errors.New("no URL")
	}
	if !strings.Contains(rawURL, "://") {
		rawURL = "http://" + rawURL
	}

	// Body.
	ctype := ""
	for _, h := range r.Headers {
		if strings.EqualFold(h.Key, "Content-Type") {
			ctype = strings.ToLower(h.Value)
		}
	}
	if jsonFlag {
		if ctype == "" {
			header("Content-Type", "application/json")
			ctype = "application/json"
		}
		if !hasHeader(r, "Accept") {
			header("Accept", "application/json")
		}
	}
	if len(data) > 0 && getMode {
		sep := "?"
		if strings.Contains(rawURL, "?") {
			sep = "&"
		}
		rawURL += sep + strings.Join(data, "&")
		data = nil
	}
	if len(data) > 0 {
		joined := strings.Join(data, "&")
		if jsonFlag {
			joined = strings.Join(data, "")
		}
		dataJSON = json.Valid([]byte(joined))
		switch {
		case strings.Contains(ctype, "json") || (ctype == "" && dataJSON):
			r.Body = &model.Body{Type: model.BodyJSON, Content: prettyJSON(joined)}
			if ctype == "" {
				header("Content-Type", "application/json")
			}
		case strings.Contains(ctype, "xml"):
			r.Body = &model.Body{Type: model.BodyXML, Content: joined}
		case ctype == "" || strings.Contains(ctype, "x-www-form-urlencoded"):
			fields, ok := formFields(joined)
			if ok {
				r.Body = &model.Body{Type: model.BodyForm, Fields: fields}
				removeHeader(r, "Content-Type")
			} else {
				r.Body = &model.Body{Type: model.BodyText, Content: joined}
			}
		default:
			r.Body = &model.Body{Type: model.BodyText, Content: joined}
		}
		if r.Method == "" {
			r.Method = "POST"
		}
	}
	if len(form) > 0 {
		r.Body = &model.Body{Type: model.BodyMultipart, Fields: form}
		removeHeader(r, "Content-Type") // the boundary is generated per send
		if r.Method == "" {
			r.Method = "POST"
		}
	}
	if r.Method == "" {
		r.Method = "GET"
	}

	// Credentials in the URL become basic auth; the query moves into params.
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid URL %q", rawURL)
	}
	if u.User != nil && r.Auth == nil {
		pass, _ := u.User.Password()
		r.Auth = &model.Auth{Type: "basic", Params: map[string]string{"username": u.User.Username(), "password": pass}}
		u.User = nil
	}
	if u.RawQuery != "" {
		for _, part := range strings.Split(u.RawQuery, "&") {
			if part == "" {
				continue
			}
			k, v, _ := strings.Cut(part, "=")
			ku, err1 := url.QueryUnescape(k)
			vu, err2 := url.QueryUnescape(v)
			if err1 != nil || err2 != nil {
				ku, vu = k, v
			}
			r.Query = append(r.Query, model.KeyValue{Key: ku, Value: vu})
		}
		u.RawQuery = ""
	}
	u.Fragment = ""
	r.URL = u.String()
	// An Authorization header with a bearer token becomes bearer auth.
	for i, h := range r.Headers {
		if strings.EqualFold(h.Key, "Authorization") && r.Auth == nil {
			if tok, ok := strings.CutPrefix(h.Value, "Bearer "); ok {
				r.Auth = &model.Auth{Type: "bearer", Params: map[string]string{"token": tok}}
				r.Headers = append(r.Headers[:i], r.Headers[i+1:]...)
			} else if enc, ok := strings.CutPrefix(h.Value, "Basic "); ok {
				if dec, err := base64.StdEncoding.DecodeString(enc); err == nil {
					user, pass, _ := strings.Cut(string(dec), ":")
					r.Auth = &model.Auth{Type: "basic", Params: map[string]string{"username": user, "password": pass}}
					r.Headers = append(r.Headers[:i], r.Headers[i+1:]...)
				}
			}
			break
		}
	}
	r.Name = r.Method + " " + displayPath(u)
	return r, warns, nil
}

func displayPath(u *url.URL) string {
	p := u.Path
	if p == "" {
		p = "/"
	}
	return u.Host + p
}

func hasHeader(r *model.Request, k string) bool {
	for _, h := range r.Headers {
		if strings.EqualFold(h.Key, k) {
			return true
		}
	}
	return false
}

func removeHeader(r *model.Request, k string) {
	out := r.Headers[:0]
	for _, h := range r.Headers {
		if !strings.EqualFold(h.Key, k) {
			out = append(out, h)
		}
	}
	r.Headers = out
	if len(r.Headers) == 0 {
		r.Headers = nil
	}
}

// formFields splits a=1&b=2 into fields; ok is false for data that isn't
// form-shaped (no '=').
func formFields(s string) ([]model.KeyValue, bool) {
	var out []model.KeyValue
	for _, part := range strings.Split(s, "&") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return nil, false
		}
		ku, err1 := url.QueryUnescape(k)
		vu, err2 := url.QueryUnescape(v)
		if err1 != nil || err2 != nil {
			ku, vu = k, v
		}
		out = append(out, model.KeyValue{Key: ku, Value: vu})
	}
	return out, len(out) > 0
}

func prettyJSON(s string) string {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return s
	}
	return string(b)
}
