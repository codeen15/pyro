package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/codeen15/pyro/internal/store"
	"github.com/codeen15/pyro/pkg/engine/model"
)

func TestSaveConflictsAndChangeEvents(t *testing.T) {
	ws, err := store.Init(t.TempDir(), "p")
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.CreateCollection("demo", model.Collection{Name: "Demo"}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var changes []Change
	h := New(Config{Workspace: ws, AnyHost: true, Team: true, OnChange: func(p Principal, c Change) {
		mu.Lock()
		changes = append(changes, c)
		mu.Unlock()
	}}).Handler()
	alice := Principal{ID: "a", Name: "Alice", Role: RoleEditor}
	bob := Principal{ID: "b", Name: "Bob", Role: RoleEditor}
	call := func(p Principal, method, path, body, ifMatch string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if ifMatch != "" {
			req.Header.Set("If-Match", ifMatch)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req.WithContext(WithPrincipal(req.Context(), p)))
		return rec
	}
	reqBody := func(name string) string {
		return `{"kind":"http","name":"` + name + `","method":"GET","url":"http://localhost"}`
	}

	if rec := call(alice, "PUT", "/api/v1/requests/demo/r", reqBody("v1"), ""); rec.Code != 200 || rec.Header().Get("ETag") == "" {
		t.Fatalf("create: %d %q", rec.Code, rec.Header().Get("ETag"))
	}
	got := call(alice, "GET", "/api/v1/requests/demo/r", "", "")
	v1 := got.Header().Get("ETag")
	if v1 == "" {
		t.Fatal("GET has no ETag")
	}
	// Both open v1; Bob saves first; Alice's save is refused with his name.
	saved := call(bob, "PUT", "/api/v1/requests/demo/r", reqBody("bob"), v1)
	if saved.Code != 200 {
		t.Fatalf("bob save: %d %s", saved.Code, saved.Body)
	}
	v2 := saved.Header().Get("ETag")
	if v2 == "" || v2 == v1 {
		t.Fatalf("version didn't move: %q %q", v1, v2)
	}
	if again := call(bob, "GET", "/api/v1/requests/demo/r", "", "").Header().Get("ETag"); again != v2 {
		t.Fatalf("PUT's ETag %q doesn't match GET's %q", v2, again)
	}
	rec := call(alice, "PUT", "/api/v1/requests/demo/r", reqBody("alice"), v1)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "Bob changed this") {
		t.Fatalf("stale save: %d %s", rec.Code, rec.Body)
	}
	// Without If-Match (overwrite) it goes through.
	if rec := call(alice, "PUT", "/api/v1/requests/demo/r", reqBody("alice"), ""); rec.Code != 200 {
		t.Fatalf("overwrite: %d", rec.Code)
	}
	// Reads and sends aren't changes; saves are.
	mu.Lock()
	defer mu.Unlock()
	if len(changes) != 3 || changes[1].Name != "Bob" || changes[1].Action != "saved request" || changes[1].Target != "demo/r" {
		t.Fatalf("changes: %+v", changes)
	}
	if rec := call(alice, "GET", "/api/v1/collections/demo/usage", "", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"loadTests":[]`) {
		t.Fatalf("usage: %d %s", rec.Code, rec.Body)
	}
}

func TestTarget(t *testing.T) {
	for _, c := range []struct{ pattern, path, want string }{
		{"/api/v1/requests/*", "/api/v1/requests/a/b%20c", "a/b c"},
		{"/api/v1/loadtests/{name}/start", "/api/v1/loadtests/smoke/start", "smoke"},
		{"/api/v1/collections", "/api/v1/collections", ""},
	} {
		if got := target(c.pattern, c.path); got != c.want {
			t.Errorf("%s: %q, want %q", c.path, got, c.want)
		}
	}
}
