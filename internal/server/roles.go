package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Role is a workspace member's role; higher roles include the lower ones.
type Role int

const (
	RoleNone Role = iota
	RoleViewer
	RoleEditor
	RoleOwner
	// RoleAdmin marks routes only server administrators may use (plugins
	// are installed for the whole server).
	RoleAdmin
)

func (r Role) String() string {
	switch r {
	case RoleViewer:
		return "viewer"
	case RoleEditor:
		return "editor"
	case RoleOwner:
		return "owner"
	case RoleAdmin:
		return "admin"
	}
	return ""
}

// ParseRole reads a role name; unknown names are RoleNone.
func ParseRole(s string) Role {
	for r := RoleViewer; r <= RoleAdmin; r++ {
		if r.String() == s {
			return r
		}
	}
	return RoleNone
}

// routeRoles is the minimum role for every project route, by "METHOD pattern".
// Viewers can read, send and run; editors change files and start load tests
// and mocks. A route missing here is refused for team users (and fails
// TestEveryRouteHasARole).
var routeRoles = map[string]Role{
	"GET /api/v1/events":                     RoleViewer,
	"GET /oauth2/callback":                   RoleViewer,
	"GET /api/v1/health":                     RoleViewer,
	"GET /api/v1/auth/config":                RoleViewer,
	"GET /api/v1/workspace":                  RoleViewer,
	"POST /api/v1/collections":               RoleEditor,
	"GET /api/v1/collections/{slug}":         RoleViewer,
	"PUT /api/v1/collections/{slug}":         RoleEditor,
	"DELETE /api/v1/collections/{slug}":      RoleEditor,
	"GET /api/v1/collections/{slug}/export":  RoleViewer,
	"GET /api/v1/collections/{slug}/docs":    RoleViewer,
	"GET /api/v1/mocks":                      RoleViewer,
	"POST /api/v1/mocks":                     RoleEditor,
	"GET /api/v1/mocks/{slug}":               RoleViewer,
	"DELETE /api/v1/mocks/{slug}":            RoleEditor,
	"GET /api/v1/folders/*":                  RoleViewer,
	"PUT /api/v1/folders/*":                  RoleEditor,
	"POST /api/v1/folders/*":                 RoleEditor,
	"DELETE /api/v1/folders/*":               RoleEditor,
	"GET /api/v1/requests/*":                 RoleViewer,
	"PUT /api/v1/requests/*":                 RoleEditor,
	"DELETE /api/v1/requests/*":              RoleEditor,
	"GET /api/v1/environments":               RoleViewer,
	"GET /api/v1/environments/{name}":        RoleViewer,
	"PUT /api/v1/environments/{name}":        RoleEditor,
	"DELETE /api/v1/environments/{name}":     RoleEditor,
	"GET /api/v1/environments/{name}/export": RoleViewer,
	"POST /api/v1/send":                      RoleViewer,
	"GET /api/v1/history":                    RoleViewer,
	"GET /api/v1/history/{id}":               RoleViewer,
	"DELETE /api/v1/history":                 RoleViewer,
	"POST /api/v1/import":                    RoleEditor,
	"POST /api/v1/sessions":                  RoleViewer,
	"GET /api/v1/sessions/{id}":              RoleViewer,
	"POST /api/v1/sessions/{id}/send":        RoleViewer,
	"DELETE /api/v1/sessions/{id}":           RoleViewer,
	"POST /api/v1/runs":                      RoleViewer,
	"GET /api/v1/runs/{id}":                  RoleViewer,
	"DELETE /api/v1/runs/{id}":               RoleViewer,
	"GET /api/v1/data":                       RoleViewer,
	"PUT /api/v1/data/*":                     RoleEditor,
	"POST /api/v1/data/preview":              RoleViewer,
	"GET /api/v1/loadtests":                  RoleViewer,
	"GET /api/v1/loadtests/{name}":           RoleViewer,
	"PUT /api/v1/loadtests/{name}":           RoleEditor,
	"DELETE /api/v1/loadtests/{name}":        RoleEditor,
	"POST /api/v1/loadtests/{name}/start":    RoleEditor,
	"GET /api/v1/agents":                     RoleViewer,
	"GET /api/v1/loadruns":                   RoleViewer,
	"GET /api/v1/loadruns/{id}":              RoleViewer,
	"GET /api/v1/loadruns/{id}/report":       RoleViewer,
	"POST /api/v1/loadruns/{id}/stop":        RoleEditor,
	"DELETE /api/v1/loadruns/{id}":           RoleEditor,
	"GET /api/v1/plugins":                    RoleViewer,
	"POST /api/v1/plugins":                   RoleAdmin,
	"GET /api/v1/plugins/index":              RoleAdmin,
	"POST /api/v1/plugins/index/install":     RoleAdmin,
	"POST /api/v1/plugins/{name}/approve":    RoleAdmin,
	"POST /api/v1/plugins/{name}/enabled":    RoleAdmin,
	"PUT /api/v1/plugins/{name}/config":      RoleAdmin,
	"DELETE /api/v1/plugins/{name}":          RoleAdmin,
	"POST /api/v1/oauth2/token":              RoleViewer,
	"POST /api/v1/oauth2/authorize":          RoleViewer,
	"GET /api/v1/cookies":                    RoleViewer,
	"PUT /api/v1/cookies":                    RoleViewer,
	"DELETE /api/v1/cookies":                 RoleViewer,
	"GET /api/v1/globals":                    RoleViewer,
	"PUT /api/v1/globals":                    RoleEditor,
	"GET /api/v1/session/variables":          RoleViewer,
	"PATCH /api/v1/session/variables":        RoleViewer,
	"DELETE /api/v1/session/variables":       RoleViewer,
}

// enforce checks the acting user's role for team requests (those carrying a
// Principal); local requests pass through.
func (s *Server) enforce(mux *chi.Mux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, team := r.Context().Value(principalKey{}).(Principal)
		if team {
			pattern := mux.Find(chi.NewRouteContext(), r.Method, r.URL.Path)
			if pattern == "" {
				mux.ServeHTTP(w, r) // 404 / 405
				return
			}
			need, ok := routeRoles[r.Method+" "+pattern]
			if !ok || !allowed(p, need) {
				writeError(w, http.StatusForbidden, forbidden(need))
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func allowed(p Principal, need Role) bool {
	if need == RoleAdmin {
		return p.Admin
	}
	return need != RoleNone && p.Role >= need
}

func forbidden(need Role) string {
	switch need {
	case RoleAdmin:
		return "only a server administrator can do this"
	case RoleEditor, RoleOwner:
		return "your role in this workspace doesn't allow this (needs " + need.String() + ")"
	}
	return "you don't have access to this"
}
