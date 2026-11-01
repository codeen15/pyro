package server

import (
	"net/http"
	"sync"

	"github.com/codeen15/pyro/pkg/engine/vars"
)

// sessionVars keeps values that scripts and extractors set during interactive
// use, like Postman's "current values". They live in memory for the life of
// `pyro serve` and are never written to the workspace files.
type sessionVars struct {
	mu      sync.Mutex
	runtime overlay
	globals overlay
	env     map[string]overlay // by environment name
	coll    map[string]overlay // by collection slug
}

// overlay maps a key to its session value; nil means "unset this session".
type overlay map[string]*string

func newSessionVars() *sessionVars {
	return &sessionVars{runtime: overlay{}, globals: overlay{}, env: map[string]overlay{}, coll: map[string]overlay{}}
}

func (o overlay) applyTo(m map[string]string) {
	for k, v := range o {
		if v == nil {
			delete(m, k)
		} else {
			m[k] = *v
		}
	}
}

// diff records every change between base and final into o.
func (o overlay) diff(base, final map[string]string) {
	for k, v := range final {
		if b, ok := base[k]; !ok || b != v {
			val := v
			o[k] = &val
		}
	}
	for k := range base {
		if _, ok := final[k]; !ok {
			o[k] = nil
		}
	}
}

func clone(m map[string]string) map[string]string {
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// apply layers session values over a set built from the files and returns
// snapshots of the bases so record can find what the send changed.
func (s *sessionVars) apply(v *vars.Set, slug string) (bases [4]map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bases = [4]map[string]string{clone(v.Runtime), clone(v.Environment), clone(v.Collection), clone(v.Globals)}
	s.runtime.applyTo(v.Runtime)
	s.globals.applyTo(v.Globals)
	if o := s.env[v.EnvName]; o != nil && v.EnvName != "" {
		o.applyTo(v.Environment)
	}
	if o := s.coll[slug]; o != nil && slug != "" {
		o.applyTo(v.Collection)
	}
	return bases
}

func (s *sessionVars) record(v *vars.Set, slug string, bases [4]map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtime.diff(bases[0], v.Runtime)
	s.globals.diff(bases[3], v.Globals)
	if v.EnvName != "" {
		if s.env[v.EnvName] == nil {
			s.env[v.EnvName] = overlay{}
		}
		s.env[v.EnvName].diff(bases[1], v.Environment)
	}
	if slug != "" {
		if s.coll[slug] == nil {
			s.coll[slug] = overlay{}
		}
		s.coll[slug].diff(bases[2], v.Collection)
	}
}

type sessionView struct {
	Runtime     overlay            `json:"runtime"`
	Globals     overlay            `json:"globals"`
	Environment map[string]overlay `json:"environments"`
	Collection  map[string]overlay `json:"collections"`
}

func (s *Server) getSessionVars(w http.ResponseWriter, r *http.Request) {
	s.session.mu.Lock()
	defer s.session.mu.Unlock()
	writeJSON(w, http.StatusOK, sessionView{s.session.runtime, s.session.globals, s.session.env, s.session.coll})
}

func (s *Server) clearSessionVars(w http.ResponseWriter, r *http.Request) {
	s.session.mu.Lock()
	s.session.runtime, s.session.globals = overlay{}, overlay{}
	s.session.env, s.session.coll = map[string]overlay{}, map[string]overlay{}
	s.session.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}
