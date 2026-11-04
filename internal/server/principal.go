package server

import (
	"context"
	"net/http"
)

// Principal is the signed-in user a request acts for. The team server sets it
// (WithPrincipal) before handing a request to a project; in local mode there
// is none and every request acts for the one local user.
type Principal struct {
	ID   string
	Name string
	Role Role
	// Admin is a server administrator (plugins, users).
	Admin bool
}

type principalKey struct{}

// WithPrincipal attaches the acting user to a request context.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// principal returns the acting user; the zero value is the local user.
func principal(r *http.Request) Principal {
	p, _ := r.Context().Value(principalKey{}).(Principal)
	return p
}

// userKey scopes per-user data (history, cookies) inside a project.
func (s *Server) userKey(r *http.Request) string {
	if id := principal(r).ID; id != "" {
		return s.key + "\x00" + id
	}
	return s.key
}

// sessionFor returns the user's current values in this project.
func (s *Server) sessionFor(r *http.Request) *sessionVars {
	id := principal(r).ID
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	sv := s.sessionsByUser[id]
	if sv == nil {
		sv = newSessionVars()
		s.sessionsByUser[id] = sv
	}
	return sv
}
