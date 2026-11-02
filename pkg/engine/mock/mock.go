// Package mock serves a collection's saved examples as a mock API: each
// request with examples becomes a route (method + path, with {{var}},
// :var and {var} segments matching anything), answered with one of its
// examples. See docs/docs-and-mocks.md.
package mock

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/codeen15/pyro/pkg/engine/importer"
	"github.com/codeen15/pyro/pkg/engine/model"
)

// Route is one mocked endpoint.
type Route struct {
	Method   string          `json:"method"`
	Path     string          `json:"path"`    // the pattern, e.g. /orders/{id}
	Request  string          `json:"request"` // workspace path
	Name     string          `json:"name"`
	Examples []model.Example `json:"examples"`
	segs     []string        // "" = matches any segment
}

// Hit is one request the mock answered (for logs).
type Hit struct {
	At      time.Time `json:"at"`
	Method  string    `json:"method"`
	Path    string    `json:"path"`
	Status  int       `json:"status"`
	Example string    `json:"example,omitempty"`
	Request string    `json:"request,omitempty"`
}

// Server answers requests from a collection's examples. Routes are rebuilt
// from Source at most once a second, so newly saved examples are served
// without a restart.
type Server struct {
	// Source returns the collection (requests with examples) and the
	// variables used to resolve a leading {{baseUrl}}.
	Source func() (*importer.Bundle, map[string]string, error)
	Delay  time.Duration
	OnHit  func(Hit)

	mu     sync.Mutex
	routes []Route
	built  time.Time
	err    error
}

var varSeg = regexp.MustCompile(`^(\{\{[^{}]+\}\}|:[A-Za-z_][\w-]*|\{[A-Za-z_][\w-]*\})$`)
var leadingVar = regexp.MustCompile(`^\{\{\s*([^{}\s]+)\s*\}\}`)

// Build turns a collection into routes. vars resolve a leading {{name}}
// (usually baseUrl) so its path (e.g. /v1) becomes part of each route.
func Build(b *importer.Bundle, vars map[string]string) []Route {
	var out []Route
	for _, r := range b.Requests {
		req := r.Request
		if req == nil || len(req.Examples) == 0 || (req.Kind != "" && req.Kind != model.KindHTTP) {
			continue
		}
		p := routePath(req.URL, vars)
		method := strings.ToUpper(req.Method)
		if method == "" {
			method = "GET"
		}
		rt := Route{Method: method, Request: r.Path, Name: req.Name, Examples: req.Examples}
		var shown []string
		for _, s := range strings.Split(strings.Trim(p, "/"), "/") {
			if s == "" {
				continue
			}
			if varSeg.MatchString(s) {
				name := strings.Trim(s, "{}:")
				rt.segs = append(rt.segs, "")
				shown = append(shown, "{"+strings.TrimSpace(name)+"}")
				continue
			}
			rt.segs = append(rt.segs, s)
			shown = append(shown, s)
		}
		rt.Path = "/" + strings.Join(shown, "/")
		out = append(out, rt)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// routePath extracts the path from a request URL: scheme and host (or a
// leading {{var}} holding them) are dropped, the variable's own path kept.
func routePath(raw string, vars map[string]string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	prefix := ""
	if m := leadingVar.FindStringSubmatch(raw); m != nil {
		raw = raw[len(m[0]):]
		if v, ok := vars[m[1]]; ok {
			prefix = routePath(v, nil)
		}
	} else if i := strings.Index(raw, "://"); i >= 0 {
		rest := raw[i+3:]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			raw = rest[j:]
		} else {
			raw = "/"
		}
	} else if !strings.HasPrefix(raw, "/") {
		// host/path without a scheme
		if j := strings.IndexByte(raw, '/'); j >= 0 {
			raw = raw[j:]
		} else {
			raw = "/"
		}
	}
	return strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(raw, "/")
}

func (s *Server) table() ([]Route, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.built) < time.Second && (s.routes != nil || s.err != nil) {
		return s.routes, s.err
	}
	b, vars, err := s.Source()
	s.built, s.err = time.Now(), err
	if err == nil {
		s.routes = Build(b, vars)
		if s.routes == nil {
			s.routes = []Route{}
		}
	}
	return s.routes, s.err
}

// Routes lists the current routes.
func (s *Server) Routes() ([]Route, error) { return s.table() }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", r.Header.Get("Access-Control-Request-Headers"))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	routes, err := s.table()
	if err != nil {
		s.reply(w, r, http.StatusInternalServerError, map[string]any{"error": "reading the collection failed: " + err.Error()}, "", "")
		return
	}
	rt := match(routes, r.Method, r.URL.Path)
	if rt == nil {
		var known []string
		for _, x := range routes {
			known = append(known, x.Method+" "+x.Path)
		}
		s.reply(w, r, http.StatusNotFound, map[string]any{
			"error":  fmt.Sprintf("no mocked route for %s %s", r.Method, r.URL.Path),
			"routes": known,
			"hint":   "routes come from requests with saved examples",
		}, "", "")
		return
	}
	ex, ok := pick(rt.Examples, r)
	if !ok {
		s.reply(w, r, http.StatusNotFound, map[string]any{"error": "no example matches X-Mock-Example / X-Mock-Status", "examples": names(rt.Examples)}, "", rt.Request)
		return
	}
	if s.Delay > 0 {
		select {
		case <-time.After(s.Delay):
		case <-r.Context().Done():
			return
		}
	}
	for _, h := range ex.Headers {
		if k := http.CanonicalHeaderKey(h.Key); !h.Disabled && k != "Content-Length" && k != "Transfer-Encoding" {
			w.Header().Add(k, h.Value)
		}
	}
	w.Header().Set("X-Pyro-Mock-Example", ex.Name)
	status := ex.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		w.Write([]byte(ex.Body))
	}
	s.hit(r, status, ex.Name, rt.Request)
}

func (s *Server) reply(w http.ResponseWriter, r *http.Request, status int, body any, example, request string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
	s.hit(r, status, example, request)
}

func (s *Server) hit(r *http.Request, status int, example, request string) {
	if s.OnHit != nil {
		s.OnHit(Hit{At: time.Now(), Method: r.Method, Path: r.URL.RequestURI(), Status: status, Example: example, Request: request})
	}
}

// match finds the route for a request: same method (HEAD falls back to
// GET), same number of segments, literals equal; the route with the most
// literal segments wins.
func match(routes []Route, method, path string) *Route {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	if len(segs) == 1 && segs[0] == "" {
		segs = nil
	}
	var best *Route
	bestScore := -1
	for _, m := range []string{method, map[string]string{http.MethodHead: http.MethodGet}[method]} {
		if m == "" {
			continue
		}
		for i := range routes {
			rt := &routes[i]
			if rt.Method != m || len(rt.segs) != len(segs) {
				continue
			}
			score := 0
			ok := true
			for j, want := range rt.segs {
				got, _ := url.PathUnescape(segs[j])
				switch {
				case want == "":
				case want == got:
					score++
				default:
					ok = false
				}
				if !ok {
					break
				}
			}
			if ok && score > bestScore {
				best, bestScore = rt, score
			}
		}
		if best != nil {
			return best
		}
	}
	return nil
}

// pick chooses the example: X-Mock-Example (or ?__example=) by name,
// X-Mock-Status (or ?__status=) by status, else the first 2xx, else the first.
func pick(examples []model.Example, r *http.Request) (model.Example, bool) {
	q := r.URL.Query()
	if name := firstNonEmpty(r.Header.Get("X-Mock-Example"), q.Get("__example")); name != "" {
		for _, e := range examples {
			if strings.EqualFold(e.Name, name) {
				return e, true
			}
		}
		return model.Example{}, false
	}
	if st := firstNonEmpty(r.Header.Get("X-Mock-Status"), q.Get("__status")); st != "" {
		code, _ := strconv.Atoi(st)
		for _, e := range examples {
			if e.Status == code {
				return e, true
			}
		}
		return model.Example{}, false
	}
	for _, e := range examples {
		if e.Status == 0 || e.Status/100 == 2 {
			return e, true
		}
	}
	return examples[0], true
}

func names(examples []model.Example) []string {
	out := make([]string, len(examples))
	for i, e := range examples {
		out[i] = e.Name
	}
	return out
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
