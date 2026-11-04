package team

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codeen15/pyro/internal/db/dbtest"
	"github.com/codeen15/pyro/internal/history"
	"github.com/codeen15/pyro/internal/server"
	"github.com/codeen15/pyro/internal/store"
	"github.com/codeen15/pyro/pkg/engine"
	"github.com/codeen15/pyro/pkg/engine/protocol/httpdriver"
)

func newTeam(t *testing.T, cfg Config) (*httptest.Server, *Store) {
	t.Helper()
	dir := t.TempDir()
	d := dbtest.Open(t)
	st, err := NewStore(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	hist, err := history.New(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DataDir, cfg.Store = dir, st
	if cfg.JWTSecret == nil {
		cfg.JWTSecret = []byte("test-secret-test-secret-test-secret!")
	}
	cfg.Project = func(ws *store.Workspace, id string) server.Config {
		return server.Config{Engine: engine.New(httpdriver.New(httpdriver.Options{FileRoot: ws.Root})), History: hist}
	}
	srv := New(cfg)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); srv.Close() })
	return ts, st
}

// client is a browser: a cookie jar plus the CSRF header on writes.
type client struct {
	t  *testing.T
	ts *httptest.Server
	c  *http.Client
}

func newClient(t *testing.T, ts *httptest.Server) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t, ts, &http.Client{Jar: jar}}
}

func (c *client) do(method, path, body string) (int, map[string]any) {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.ts.URL+path, strings.NewReader(body))
	if method != "GET" {
		req.Header.Set(CSRFHeader, "1")
	}
	res, err := c.c.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{"raw": string(raw)}
	}
	return res.StatusCode, out
}

// doList is do for endpoints that return a JSON array.
func (c *client) doList(method, path string) (int, []map[string]any) {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.ts.URL+path, nil)
	res, err := c.c.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	var out []map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func signup(t *testing.T, c *client, email string) map[string]any {
	t.Helper()
	code, out := c.do("POST", "/api/v1/auth/signup", `{"email":"`+email+`","name":"N","password":"correct horse battery"}`)
	if code != 201 {
		t.Fatalf("signup %s: %d %v", email, code, out)
	}
	return out
}

func firstProject(t *testing.T, me map[string]any) string {
	t.Helper()
	ws := me["workspaces"].([]any)[0].(map[string]any)
	return ws["projects"].([]any)[0].(map[string]any)["id"].(string)
}

func TestSignupLoginAndProjectAccess(t *testing.T) {
	ts, st := newTeam(t, Config{})
	alice := newClient(t, ts)

	if code, _ := alice.do("GET", "/api/v1/me", ""); code != 401 {
		t.Fatalf("me before sign-in: %d", code)
	}
	code, cfg := alice.do("GET", "/api/v1/auth/config", "")
	if code != 200 || cfg["firstUser"] != true || cfg["mode"] != "team" {
		t.Fatalf("auth config: %d %v", code, cfg)
	}
	me := signup(t, alice, "alice@example.com")
	if me["user"].(map[string]any)["admin"] != true {
		t.Fatal("first user should be admin")
	}
	pid := firstProject(t, me)

	// The project API works through the proxy, acting as alice.
	if code, out := alice.do("POST", "/api/v1/projects/"+pid+"/collections", `{"slug":"shop","name":"Shop"}`); code != 201 {
		t.Fatalf("create collection: %d %v", code, out)
	}
	code, out := alice.do("GET", "/api/v1/projects/"+pid+"/workspace", "")
	if code != 200 || !strings.Contains(toJSON(out), "shop") {
		t.Fatalf("workspace tree: %d %v", code, out)
	}
	// Writes need the CSRF header.
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/projects/"+pid+"/collections", strings.NewReader(`{"slug":"x"}`))
	res, _ := alice.c.Do(req)
	if res.StatusCode != 403 {
		t.Fatalf("write without CSRF header: %d", res.StatusCode)
	}
	res.Body.Close()

	// Invite-only by default: a second sign-up is refused.
	bob := newClient(t, ts)
	if code, _ := bob.do("POST", "/api/v1/auth/signup", `{"email":"bob@example.com","password":"correct horse battery"}`); code != 403 {
		t.Fatalf("second signup under invite-only: %d", code)
	}

	// Other users can't see alice's project.
	if _, err := st.CreateUser(t.Context(), NewUser{Email: "bob@example.com", Password: "correct horse battery"}); err != nil {
		t.Fatal(err)
	}
	if code, _ := bob.do("POST", "/api/v1/auth/login", `{"email":"bob@example.com","password":"correct horse battery"}`); code != 200 {
		t.Fatalf("bob login: %d", code)
	}
	if code, _ := bob.do("GET", "/api/v1/projects/"+pid+"/workspace", ""); code != 404 {
		t.Fatalf("bob reading alice's project: %d", code)
	}

	// Sign out ends the session.
	alice.do("POST", "/api/v1/auth/logout", "")
	if code, _ := alice.do("GET", "/api/v1/me", ""); code != 401 {
		t.Fatalf("me after sign-out: %d", code)
	}
}

func toJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestWrongPasswordIsRateLimited(t *testing.T) {
	ts, _ := newTeam(t, Config{})
	c := newClient(t, ts)
	signup(t, c, "a@example.com")
	for i := 0; i < 10; i++ {
		if code, _ := c.do("POST", "/api/v1/auth/login", `{"email":"a@example.com","password":"wrong password!"}`); code != 400 {
			t.Fatalf("attempt %d: %d", i, code)
		}
	}
	if code, _ := c.do("POST", "/api/v1/auth/login", `{"email":"a@example.com","password":"correct horse battery"}`); code != 429 {
		t.Fatalf("after 10 failures: %d", code)
	}
}

func TestSignupPolicies(t *testing.T) {
	ts, _ := newTeam(t, Config{Signup: SignupDomains, SignupDomains: []string{"example.com"}})
	signup(t, newClient(t, ts), "first@other.org") // the first account is always allowed
	if code, _ := newClient(t, ts).do("POST", "/api/v1/auth/signup", `{"email":"x@other.org","password":"correct horse battery"}`); code != 403 {
		t.Fatalf("outside domain: %d", code)
	}
	signup(t, newClient(t, ts), "y@Example.com")
	if code, _ := newClient(t, ts).do("POST", "/api/v1/auth/signup", `{"email":"y@example.com","password":"correct horse battery"}`); code != 409 {
		t.Fatalf("duplicate email: %d", code)
	}
	if code, _ := newClient(t, ts).do("POST", "/api/v1/auth/signup", `{"email":"z@example.com","password":"short"}`); code != 400 {
		t.Fatalf("short password: %d", code)
	}
}

func TestWorkspaceAndProjectLifecycle(t *testing.T) {
	ts, st := newTeam(t, Config{})
	c := newClient(t, ts)
	signup(t, c, "o@example.com")
	code, ws := c.do("POST", "/api/v1/workspaces", `{"name":"QA team"}`)
	if code != 201 || ws["kind"] != "team" {
		t.Fatalf("create workspace: %d %v", code, ws)
	}
	wsID := ws["id"].(string)
	code, p := c.do("POST", "/api/v1/workspaces/"+wsID+"/projects", `{"name":"Payments"}`)
	if code != 201 {
		t.Fatalf("create project: %d %v", code, p)
	}
	pid := p["id"].(string)
	if code, out := c.do("GET", "/api/v1/projects/"+pid, ""); code != 200 || out["role"] != "owner" || out["workspaceName"] != "QA team" {
		t.Fatalf("project info: %d %v", code, out)
	}
	if code, _ := c.do("DELETE", "/api/v1/workspaces/"+wsID, ""); code != 400 {
		t.Fatalf("deleting a workspace with projects: %d", code)
	}

	// A viewer can read but not write.
	u, err := st.CreateUser(t.Context(), NewUser{Email: "v@example.com", Password: "correct horse battery"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.NewInsert().Model(&memberRow{WS: wsID, UserID: u.ID, Role: "viewer"}).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	v := newClient(t, ts)
	v.do("POST", "/api/v1/auth/login", `{"email":"v@example.com","password":"correct horse battery"}`)
	if code, _ := v.do("GET", "/api/v1/projects/"+pid+"/workspace", ""); code != 200 {
		t.Fatalf("viewer read: %d", code)
	}
	if code, _ := v.do("POST", "/api/v1/projects/"+pid+"/collections", `{"slug":"x"}`); code != 403 {
		t.Fatalf("viewer write: %d", code)
	}
	if code, _ := v.do("DELETE", "/api/v1/projects/"+pid, ""); code != 403 {
		t.Fatalf("viewer delete project: %d", code)
	}

	if code, _ := c.do("DELETE", "/api/v1/projects/"+pid, ""); code != 204 {
		t.Fatalf("delete project: %d", code)
	}
	if code, _ := c.do("DELETE", "/api/v1/workspaces/"+wsID, ""); code != 204 {
		t.Fatalf("delete empty workspace: %d", code)
	}
}

func TestPasswordHashing(t *testing.T) {
	h, err := hashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword(h, "correct horse battery") || verifyPassword(h, "correct horse batterY") {
		t.Fatal("verify mismatch")
	}
	h2, _ := hashPassword("correct horse battery")
	if h == h2 {
		t.Fatal("hashes should be salted")
	}
}
