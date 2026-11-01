// Package server exposes the engine and workspace over a local REST API
// under /api/v1 and serves the embedded web UI.
package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/pyrohq/pyro/internal/history"
	"github.com/pyrohq/pyro/internal/store"
	"github.com/pyrohq/pyro/internal/version"
	"github.com/pyrohq/pyro/pkg/engine"
	"github.com/pyrohq/pyro/pkg/engine/model"
)

// TokenHeader carries the per-launch session token. The UI receives the token
// once via the ?token= query string printed by `pyro serve`.
const TokenHeader = "X-Pyro-Token"

type Config struct {
	Workspace *store.Workspace
	Engine    *engine.Engine
	// History is optional; when nil, sends aren't recorded.
	History *history.Store
	// Token, when non-empty, is required on every /api request.
	Token string
	// UI is the built single-page app; nil disables UI serving.
	UI     fs.FS
	Logger *slog.Logger
}

type Server struct {
	cfg      Config
	ws       *store.Workspace
	log      *slog.Logger
	session  *sessionVars
	hub      *hub
	sessions *sessionRegistry
	runs     *runRegistry
	loads    *loadRegistry
}

func New(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Server{
		cfg: cfg, ws: cfg.Workspace, log: cfg.Logger, session: newSessionVars(), hub: newHub(),
		sessions: &sessionRegistry{byID: map[string]*liveSession{}},
		runs:     &runRegistry{byID: map[string]*liveRun{}},
		loads:    &loadRegistry{byID: map[string]*liveLoad{}},
	}
}

// Close ends open sessions and cancels running collection runs.
func (s *Server) Close() {
	s.closeAllSessions()
	s.runs.mu.Lock()
	for _, run := range s.runs.byID {
		run.cancel()
	}
	s.runs.mu.Unlock()
	s.loads.mu.Lock()
	cur := s.loads.current
	s.loads.mu.Unlock()
	if cur != nil {
		cur.test.Abort()
		cur.test.Wait()
	}
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.Recoverer)

	// The event stream checks its token itself (browsers can't send headers
	// on WebSocket upgrades) and must not sit behind the request timeout.
	r.Get("/api/v1/events", s.events)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(middleware.Timeout(60 * time.Second))
		r.Use(s.requireToken)
		r.Get("/health", s.health)
		r.Get("/workspace", s.getWorkspace)

		r.Post("/collections", s.createCollection)
		r.Get("/collections/{slug}", s.getCollection)
		r.Put("/collections/{slug}", s.putCollection)

		r.Get("/requests/*", s.getRequest)
		r.Put("/requests/*", s.putRequest)
		r.Delete("/requests/*", s.deleteRequest)

		r.Get("/environments", s.listEnvironments)
		r.Get("/environments/{name}", s.getEnvironment)
		r.Put("/environments/{name}", s.putEnvironment)
		r.Delete("/environments/{name}", s.deleteEnvironment)

		r.Post("/send", s.send)
		r.Get("/history", s.listHistory)
		r.Get("/history/{id}", s.getHistory)
		r.Delete("/history", s.clearHistory)

		r.Post("/import", s.importFile)

		r.Post("/sessions", s.openSession)
		r.Get("/sessions/{id}", s.getSession)
		r.Post("/sessions/{id}/send", s.sendSession)
		r.Delete("/sessions/{id}", s.closeSession)

		r.Post("/runs", s.startRun)
		r.Get("/runs/{id}", s.getRun)
		r.Delete("/runs/{id}", s.cancelRun)

		r.Get("/loadtests", s.listLoadTests)
		r.Get("/loadtests/{name}", s.getLoadTest)
		r.Put("/loadtests/{name}", s.putLoadTest)
		r.Delete("/loadtests/{name}", s.deleteLoadTest)
		r.Post("/loadtests/{name}/start", s.startLoadTest)
		r.Get("/loadruns", s.listLoadRuns)
		r.Get("/loadruns/{id}", s.getLoadRun)
		r.Get("/loadruns/{id}/report", s.loadRunReport)
		r.Post("/loadruns/{id}/stop", s.stopLoadRun)
		r.Delete("/loadruns/{id}", s.deleteLoadRun)

		r.Get("/session/variables", s.getSessionVars)
		r.Delete("/session/variables", s.clearSessionVars)
	})

	if s.cfg.UI != nil {
		r.Handle("/*", spaHandler(s.cfg.UI))
	}
	return r
}

func (s *Server) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Token != "" {
			got := r.Header.Get(TokenHeader)
			if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.Token)) != 1 {
				writeError(w, http.StatusUnauthorized, "missing or invalid session token")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version.Version})
}

func (s *Server) getWorkspace(w http.ResponseWriter, r *http.Request) {
	tree, err := s.ws.Tree()
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":        s.ws.Meta.Name,
		"root":        s.ws.Root,
		"collections": tree,
	})
}

func (s *Server) createCollection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Slug string `json:"slug"`
		model.Collection
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Slug == "" {
		writeError(w, http.StatusBadRequest, "slug is required")
		return
	}
	if body.Name == "" {
		body.Name = body.Slug
	}
	if err := s.ws.CreateCollection(body.Slug, body.Collection); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, body)
}

func (s *Server) getCollection(w http.ResponseWriter, r *http.Request) {
	c, err := s.ws.ReadCollection(chi.URLParam(r, "slug"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) getRequest(w http.ResponseWriter, r *http.Request) {
	req, err := s.ws.ReadRequest(reqPath(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) putRequest(w http.ResponseWriter, r *http.Request) {
	var req model.Request
	if !decode(w, r, &req) {
		return
	}
	if req.Kind == "" {
		req.Kind = model.KindHTTP
	}
	if err := s.ws.WriteRequest(reqPath(r), &req); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) deleteRequest(w http.ResponseWriter, r *http.Request) {
	if err := s.ws.DeleteRequest(reqPath(r)); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reqPath returns the decoded request path from the route wildcard. chi
// matches on the raw (escaped) path, so "%2F" or "%20" arrive still encoded.
func reqPath(r *http.Request) string {
	p := chi.URLParam(r, "*")
	if u, err := url.PathUnescape(p); err == nil {
		return u
	}
	return p
}

func (s *Server) listEnvironments(w http.ResponseWriter, r *http.Request) {
	names, err := s.ws.ListEnvironments()
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, names)
}

func (s *Server) getEnvironment(w http.ResponseWriter, r *http.Request) {
	env, err := s.ws.ReadEnvironment(chi.URLParam(r, "name"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, env)
}

func (s *Server) putEnvironment(w http.ResponseWriter, r *http.Request) {
	var env model.Environment
	if !decode(w, r, &env) {
		return
	}
	name := chi.URLParam(r, "name")
	if env.Name == "" {
		env.Name = name
	}
	if err := s.ws.WriteEnvironment(name, &env); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, env)
}

// fail maps store errors to HTTP status codes; anything unexpected is logged
// and reported as a 500 without leaking internals.
func (s *Server) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, history.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, store.ErrInvalidPath), isValidation(err):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrExists):
		writeError(w, http.StatusConflict, err.Error())
	default:
		s.log.Error("request failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

const maxBodyBytes = 10 << 20

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// spaHandler serves static files and falls back to index.html for client-side
// routes. Hashed assets get long-lived caching; index.html is never cached.
func spaHandler(ui fs.FS) http.Handler {
	files := http.FileServerFS(ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if st, err := fs.Stat(ui, p); err == nil && !st.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(ui, "index.html")
		if err != nil {
			http.Error(w, "UI not built: run `make ui` and rebuild pyro", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}
