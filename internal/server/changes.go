package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
)

// Edits by several people: versions (ETags) so a save can't silently
// overwrite someone else's newer change, "tree" events so everyone's sidebar
// follows, and an OnChange hook the team server records as activity.

// versioned are the GET/PUT pairs whose responses carry an ETag; a PUT with
// If-Match is refused (409) when the stored version has moved on.
var versioned = map[string]bool{
	"/api/v1/requests/*":          true,
	"/api/v1/collections/{slug}":  true,
	"/api/v1/folders/*":           true,
	"/api/v1/environments/{name}": true,
	"/api/v1/loadtests/{name}":    true,
	"/api/v1/globals":             true,
}

// changeActions name what a successful write did, for activity and events.
var changeActions = map[string]string{
	"POST /api/v1/collections":            "created collection",
	"PUT /api/v1/collections/{slug}":      "edited collection settings",
	"DELETE /api/v1/collections/{slug}":   "deleted collection",
	"POST /api/v1/folders/*":              "created folder",
	"PUT /api/v1/folders/*":               "edited folder settings",
	"DELETE /api/v1/folders/*":            "deleted folder",
	"PUT /api/v1/requests/*":              "saved request",
	"DELETE /api/v1/requests/*":           "deleted request",
	"PUT /api/v1/environments/{name}":     "saved environment",
	"DELETE /api/v1/environments/{name}":  "deleted environment",
	"PUT /api/v1/globals":                 "saved globals",
	"POST /api/v1/import":                 "imported",
	"PUT /api/v1/data/*":                  "uploaded data file",
	"PUT /api/v1/loadtests/{name}":        "saved load test",
	"DELETE /api/v1/loadtests/{name}":     "deleted load test",
	"POST /api/v1/loadtests/{name}/start": "started load test",
	"POST /api/v1/reports":                "uploaded a report",
	"DELETE /api/v1/reports/{id}":         "deleted a report",
}

// Change describes one successful write.
type Change struct {
	By     string `json:"by"` // user ID ("" locally)
	Name   string `json:"name,omitempty"`
	Action string `json:"action"`
	Target string `json:"target,omitempty"`
}

type versions struct {
	mu sync.Mutex // serialises check-and-write on versioned resources

	wmu    sync.Mutex
	writer map[string]string // last writer's name by URL path
}

func (v *versions) lastWriter(key string) string {
	v.wmu.Lock()
	defer v.wmu.Unlock()
	return v.writer[key]
}

func (v *versions) setWriter(key, name string) {
	v.wmu.Lock()
	defer v.wmu.Unlock()
	if len(v.writer) > 10000 {
		v.writer = map[string]string{}
	}
	v.writer[key] = name
}

// etag versions a GET response. A load test's view also reports whether its
// steps' requests exist, which changes without the file changing, so only
// its YAML counts.
func etag(pattern string, body []byte) string {
	if pattern == "/api/v1/loadtests/{name}" {
		var v struct {
			YAML string `json:"yaml"`
		}
		if json.Unmarshal(body, &v) == nil {
			body = []byte(v.YAML)
		}
	}
	h := sha256.Sum256(body)
	return `"` + hex.EncodeToString(h[:12]) + `"`
}

// target is the resource a route names: the wildcard path or the {param}.
func target(pattern, path string) string {
	ps, xs := strings.Split(pattern, "/"), strings.Split(path, "/")
	for i, seg := range ps {
		if i >= len(xs) {
			break
		}
		var t string
		switch {
		case seg == "*":
			t = strings.Join(xs[i:], "/")
		case strings.HasPrefix(seg, "{"):
			t = xs[i]
		default:
			continue
		}
		if u, err := url.PathUnescape(t); err == nil {
			return u
		}
		return t
	}
	return ""
}

// serveTracked runs a matched route, adding versions to versioned GETs and
// PUTs and reporting successful writes.
func (s *Server) serveTracked(next http.Handler, w http.ResponseWriter, r *http.Request, pattern string) {
	ver := versioned[pattern] && (r.Method == http.MethodGet || r.Method == http.MethodPut)
	action := changeActions[r.Method+" "+pattern]
	if !ver && action == "" {
		next.ServeHTTP(w, r)
		return
	}
	key := r.URL.Path
	if ver && r.Method == http.MethodPut {
		s.versions.mu.Lock()
		defer s.versions.mu.Unlock()
		if want := r.Header.Get("If-Match"); want != "" {
			if cur := s.currentVersion(next, r, pattern); cur != "" && cur != want {
				body := map[string]any{"error": "this was changed since you opened it", "conflict": true}
				if by := s.versions.lastWriter(key); by != "" {
					body["by"] = by
					body["error"] = by + " changed this since you opened it"
				}
				writeJSON(w, http.StatusConflict, body)
				return
			}
		}
	}
	rec := httptest.NewRecorder()
	next.ServeHTTP(rec, r)
	if ver && rec.Code == http.StatusOK {
		tag := ""
		if r.Method == http.MethodGet {
			tag = etag(pattern, rec.Body.Bytes())
		} else {
			tag = s.currentVersion(next, r, pattern)
		}
		if tag != "" {
			rec.Header().Set("ETag", tag)
		}
	}
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())

	if action != "" && rec.Code < 300 {
		p := principal(r)
		if ver || r.Method == http.MethodDelete {
			s.versions.setWriter(key, p.Name)
		}
		ch := Change{By: p.ID, Name: p.Name, Action: action, Target: target(pattern, r.URL.Path)}
		if r.Method != http.MethodPost || !strings.HasSuffix(pattern, "/start") {
			s.hub.publish("tree", "changed", ch)
		}
		if s.cfg.OnChange != nil {
			s.cfg.OnChange(p, ch)
		}
	}
}

// currentVersion is the ETag a GET of the same URL would return now ("" if
// the resource doesn't exist).
func (s *Server) currentVersion(next http.Handler, r *http.Request, pattern string) string {
	// A fresh chi routing context: the router records its matches in the
	// one it finds, which the real request still has to route with.
	get := r.Clone(context.WithValue(r.Context(), chi.RouteCtxKey, nil))
	get.Method, get.Body, get.ContentLength = http.MethodGet, http.NoBody, 0
	rec := httptest.NewRecorder()
	next.ServeHTTP(rec, get)
	if rec.Code != http.StatusOK {
		return ""
	}
	return etag(pattern, rec.Body.Bytes())
}

// Changed tells the project's members about a change made from outside
// (e.g. a collection copied in from another project).
func (s *Server) Changed(c Change) {
	s.hub.publish("tree", "changed", c)
}
