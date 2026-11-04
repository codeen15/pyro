package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/codeen15/pyro/internal/store"
	"github.com/codeen15/pyro/pkg/engine"
	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/protocol/httpdriver"
)

// Every project route must say which role it needs, or team users get 403s
// (and, worse, a new route could be added without thinking about access).
func TestEveryRouteHasARole(t *testing.T) {
	s := New(Config{AnyHost: true})
	r := chi.NewRouter()
	s.routes(r)
	seen := map[string]bool{}
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + strings.ReplaceAll(route, "/*/", "/")
		seen[key] = true
		if _, ok := routeRoles[key]; !ok {
			t.Errorf("route %q has no entry in routeRoles", key)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for k := range routeRoles {
		if !seen[k] {
			t.Errorf("routeRoles has %q but no such route", k)
		}
	}
}

func TestRolesAreEnforcedForTeamUsers(t *testing.T) {
	ws, err := store.Init(t.TempDir(), "p")
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.CreateCollection("demo", model.Collection{Name: "Demo"}); err != nil {
		t.Fatal(err)
	}
	h := New(Config{Workspace: ws, Engine: engine.New(httpdriver.New(httpdriver.Options{})), AnyHost: true, Team: true}).Handler()
	call := func(p *Principal, method, path, body string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if p != nil {
			req = req.WithContext(WithPrincipal(req.Context(), *p))
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	viewer := &Principal{ID: "v", Role: RoleViewer}
	editor := &Principal{ID: "e", Role: RoleEditor}
	none := &Principal{ID: "n"}
	req := `{"kind":"http","name":"x","method":"GET","url":"http://localhost"}`

	cases := []struct {
		p            *Principal
		method, path string
		body         string
		want         int
	}{
		{viewer, "GET", "/api/v1/workspace", "", 200},
		{none, "GET", "/api/v1/workspace", "", 403},
		{viewer, "PUT", "/api/v1/requests/demo/x", req, 403},
		{editor, "PUT", "/api/v1/requests/demo/x", req, 200},
		{viewer, "GET", "/api/v1/requests/demo/x", "", 200},
		{editor, "POST", "/api/v1/plugins", "", 403},
		{editor, "POST", "/api/v1/mocks", `{"collection":"demo"}`, 403},
		{viewer, "GET", "/api/v1/nope", "", 404},
		{nil, "GET", "/api/v1/workspace", "", 200}, // local mode
	}
	for _, c := range cases {
		if got := call(c.p, c.method, c.path, c.body); got != c.want {
			t.Errorf("%v %s %s: got %d, want %d", c.p, c.method, c.path, got, c.want)
		}
	}

	// Team mode hides the folder path.
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v1/workspace", nil)
	h.ServeHTTP(rec, r.WithContext(WithPrincipal(r.Context(), *viewer)))
	if strings.Contains(rec.Body.String(), ws.Root) {
		t.Errorf("workspace response leaks the folder path: %s", rec.Body)
	}
}

func TestSessionVariablesArePerUser(t *testing.T) {
	ws, err := store.Init(t.TempDir(), "p")
	if err != nil {
		t.Fatal(err)
	}
	h := New(Config{Workspace: ws, AnyHost: true, Team: true}).Handler()
	call := func(id, method, body string) string {
		req := httptest.NewRequest(method, "/api/v1/session/variables", strings.NewReader(body))
		req = req.WithContext(WithPrincipal(req.Context(), Principal{ID: id, Role: RoleViewer}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Body.String()
	}
	call("a", "PATCH", `{"scope":"globals","key":"token","value":"alice"}`)
	if got := call("a", "GET", ""); !strings.Contains(got, "alice") {
		t.Fatalf("user a lost their value: %s", got)
	}
	if got := call("b", "GET", ""); strings.Contains(got, "alice") {
		t.Fatalf("user b sees user a's value: %s", got)
	}
}
