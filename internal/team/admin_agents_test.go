package team

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/codeen15/pyro/internal/version"
)

func TestAdminAgentsAccess(t *testing.T) {
	ts, st := newTeam(t, Config{})
	admin := newClient(t, ts)
	signup(t, admin, "admin@example.com")
	if _, err := st.CreateUser(t.Context(), NewUser{Email: "user@example.com", Password: "correct horse battery"}); err != nil {
		t.Fatal(err)
	}
	user := newClient(t, ts)
	user.do("POST", "/api/v1/auth/login", `{"email":"user@example.com","password":"correct horse battery"}`)

	for _, rt := range [][2]string{
		{"GET", "/api/v1/admin/agents"},
		{"POST", "/api/v1/admin/agents"},
		{"PATCH", "/api/v1/admin/agents/x"},
		{"DELETE", "/api/v1/admin/agents/x"},
		{"POST", "/api/v1/admin/agents/x/test"},
		{"POST", "/api/v1/admin/agents/x/token"},
	} {
		if code, _ := user.do(rt[0], rt[1], `{}`); code != 403 {
			t.Fatalf("non-admin %s %s: %d", rt[0], rt[1], code)
		}
	}

	// Writes without the CSRF header are refused, even for an admin.
	for _, rt := range [][2]string{
		{"POST", "/api/v1/admin/agents"},
		{"PATCH", "/api/v1/admin/agents/x"},
		{"DELETE", "/api/v1/admin/agents/x"},
		{"POST", "/api/v1/admin/agents/x/test"},
		{"POST", "/api/v1/admin/agents/x/token"},
	} {
		req, _ := http.NewRequest(rt[0], ts.URL+rt[1], strings.NewReader(`{"name":"a","mode":"connect"}`))
		res, err := admin.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 403 {
			t.Fatalf("no CSRF %s %s: %d", rt[0], rt[1], res.StatusCode)
		}
	}
}

func TestAdminAgentsDirectLifecycle(t *testing.T) {
	ts, srv, st := newTeamServer(t, Config{})
	admin := newClient(t, ts)
	signup(t, admin, "admin@example.com")
	as := testAgent(t, "sg-box")
	ctx := context.Background()

	body := fmt.Sprintf(`{"name":"vps-sg","mode":"direct","address":%q,"token":"tok"}`, as.URL)
	code, out := admin.do("POST", "/api/v1/admin/agents", body)
	if code != 201 {
		t.Fatalf("add: %d %v", code, out)
	}
	if out["token"] != nil || out["command"] != nil {
		t.Fatalf("direct agents get no generated token or command: %v", out)
	}
	ag := out["agent"].(map[string]any)
	id := ag["id"].(string)
	if ag["name"] != "vps-sg" || ag["mode"] != "direct" || ag["address"] != as.URL || ag["createdBy"] == "" {
		t.Fatalf("agent: %v", ag)
	}
	if strings.Contains(toJSON(out), `"tok"`) {
		t.Fatalf("token leaked: %v", out)
	}

	// Validation and duplicates.
	if code, _ := admin.do("POST", "/api/v1/admin/agents", `{"name":"","mode":"connect"}`); code != 400 {
		t.Fatalf("empty name: %d", code)
	}
	if code, _ := admin.do("POST", "/api/v1/admin/agents", `{"name":"x","mode":"direct","address":"nope","token":"t"}`); code != 400 {
		t.Fatalf("bad address: %d", code)
	}
	if code, _ := admin.do("POST", "/api/v1/admin/agents", `{"name":"x","mode":"weird"}`); code != 400 {
		t.Fatalf("bad mode: %d", code)
	}
	if code, _ := admin.do("POST", "/api/v1/admin/agents", `{"name":"VPS-SG","mode":"connect"}`); code != 409 {
		t.Fatalf("duplicate: %d", code)
	}

	// The list carries who made it.
	code, list := admin.doList("GET", "/api/v1/admin/agents")
	if code != 200 || len(list) != 1 || list[0]["id"] != id || list[0]["createdBy"] == "" || list[0]["created"] == nil {
		t.Fatalf("list: %d %v", code, list)
	}

	// Test probes now.
	code, out = admin.do("POST", "/api/v1/admin/agents/"+id+"/test", "")
	if code != 200 || out["status"] != "available" || out["version"] != "0.2.0" {
		t.Fatalf("test: %d %v", code, out)
	}
	if code, _ := admin.do("POST", "/api/v1/admin/agents/nope/test", ""); code != 404 {
		t.Fatalf("test unknown: %d", code)
	}

	// Rename; a wrong token shows up as an error right away (probe after PATCH).
	code, out = admin.do("PATCH", "/api/v1/admin/agents/"+id, `{"name":"sg-1","token":"wrong","insecureTLS":true}`)
	if code != 200 || out["name"] != "sg-1" || out["status"] != "error" || out["insecureTLS"] != true {
		t.Fatalf("patch: %d %v", code, out)
	}
	code, out = admin.do("PATCH", "/api/v1/admin/agents/"+id, `{"token":"tok"}`)
	if code != 200 || out["status"] != "available" {
		t.Fatalf("patch token back: %d %v", code, out)
	}
	if code, _ := admin.do("PATCH", "/api/v1/admin/agents/nope", `{"name":"z"}`); code != 404 {
		t.Fatalf("patch unknown: %d", code)
	}
	if code, _ := admin.do("PATCH", "/api/v1/admin/agents/"+id, `{"name":""}`); code != 400 {
		t.Fatalf("patch empty name: %d", code)
	}
	if code, _ := admin.do("PATCH", "/api/v1/admin/agents/"+id, `{"bogus":1}`); code != 400 {
		t.Fatalf("patch unknown field: %d", code)
	}

	// Direct agents have no generated token to rotate.
	if code, _ := admin.do("POST", "/api/v1/admin/agents/"+id+"/token", ""); code != 400 {
		t.Fatalf("rotate direct: %d", code)
	}

	// Delete removes the row and the registry's status.
	if code, _ := admin.do("DELETE", "/api/v1/admin/agents/"+id, ""); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := admin.do("DELETE", "/api/v1/admin/agents/"+id, ""); code != 404 {
		t.Fatalf("delete again: %d", code)
	}
	if _, ok := agentByID(srv.agents.List(ctx), id); ok {
		t.Fatal("deleted agent still listed")
	}
	if agents, _ := st.Agents(ctx); len(agents) != 0 {
		t.Fatalf("rows left: %v", agents)
	}

	// Add, update and remove are in the server activity log (no workspace).
	got := serverActivity(t, st)
	want := []string{"agent.add vps-sg", "agent.update sg-1", "agent.update sg-1", "agent.remove sg-1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("activity: %v, want %v", got, want)
	}
}

func serverActivity(t *testing.T, st *Store) []string {
	t.Helper()
	var rows []auditRow
	if err := st.db.NewSelect().Model(&rows).Where("ws = ''").Order("id").Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range rows {
		if r.UserID == "" {
			t.Fatalf("activity without a user: %+v", r)
		}
		out = append(out, r.Action+" "+r.Target)
	}
	return out
}

func TestAdminAgentsConnectLifecycle(t *testing.T) {
	ts, srv, st := newTeamServer(t, Config{PublicURL: "https://pyro.example.com/"})
	admin := newClient(t, ts)
	signup(t, admin, "admin@example.com")
	ctx := context.Background()

	code, out := admin.do("POST", "/api/v1/admin/agents", `{"name":"office-1","mode":"connect"}`)
	if code != 201 {
		t.Fatalf("add: %d %v", code, out)
	}
	tok, _ := out["token"].(string)
	cmd, _ := out["command"].(string)
	id := out["agent"].(map[string]any)["id"].(string)
	if tok == "" || out["base"] != "https://pyro.example.com" {
		t.Fatalf("token/base: %v", out)
	}
	if !strings.Contains(cmd, "--connect 'https://pyro.example.com' --token '"+tok+"'") || !strings.HasPrefix(cmd, "docker run -d --name pyro-agent --restart unless-stopped ghcr.io/codeen15/pyro:") || !strings.Contains(cmd, " agent --connect ") || out["warning"] != nil {
		t.Fatalf("command: %q", cmd)
	}
	if a, _ := st.Agents(ctx); len(a) != 1 || a[0].Mode != AgentConnect {
		t.Fatalf("stored: %v", a)
	}

	// Not connected yet.
	code, out = admin.do("POST", "/api/v1/admin/agents/"+id+"/test", "")
	if code != 200 || out["status"] != "offline" {
		t.Fatalf("test offline: %d %v", code, out)
	}

	// The agent dials in with the token.
	dialIn(t, ts.URL, tok, "office-1")
	waitStatus(t, srv.agents, id, "available", 5*time.Second)

	// The list never shows the token or its hash.
	if _, list := admin.doList("GET", "/api/v1/admin/agents"); strings.Contains(toJSON(list), tok) || strings.Contains(toJSON(list), hashAgentToken(tok)) || strings.Contains(strings.ToLower(toJSON(list)), "hash") {
		t.Fatalf("list leaks the token: %v", list)
	}

	// Connect agents have no address or stored token to change; rename works.
	if code, _ := admin.do("PATCH", "/api/v1/admin/agents/"+id, `{"insecureTLS":true}`); code != 400 {
		t.Fatalf("patch insecureTLS on connect: %d", code)
	}
	if code, _ := admin.do("PATCH", "/api/v1/admin/agents/"+id, `{"address":"1.2.3.4:5"}`); code != 400 {
		t.Fatalf("patch address on connect: %d", code)
	}
	if code, out := admin.do("PATCH", "/api/v1/admin/agents/"+id, `{"name":"office-2"}`); code != 200 || out["name"] != "office-2" {
		t.Fatalf("rename: %d %v", code, out)
	}

	// Rotating gives a new token, drops the connection and keeps the row.
	code, out = admin.do("POST", "/api/v1/admin/agents/"+id+"/token", "")
	if code != 200 {
		t.Fatalf("rotate: %d %v", code, out)
	}
	tok2, _ := out["token"].(string)
	if tok2 == "" || tok2 == tok || !strings.Contains(out["command"].(string), tok2) || out["base"] != "https://pyro.example.com" {
		t.Fatalf("rotate result: %v", out)
	}
	waitStatus(t, srv.agents, id, "offline", 5*time.Second)
	if got := connectWith(t, ts.URL, tok); got != 401 {
		t.Fatalf("old token: %d", got)
	}
	if a, ok := agentByID(srv.agents.List(ctx), id); !ok || a.Name != "office-2" {
		t.Fatalf("row kept: %+v %v", a, ok)
	}
	if code, _ := admin.do("POST", "/api/v1/admin/agents/nope/token", ""); code != 404 {
		t.Fatalf("rotate unknown: %d", code)
	}

	// Remove.
	dialIn(t, ts.URL, tok2, "office-2")
	waitStatus(t, srv.agents, id, "available", 5*time.Second)
	if code, _ := admin.do("DELETE", "/api/v1/admin/agents/"+id, ""); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if _, ok := agentByID(srv.agents.List(ctx), id); ok {
		t.Fatal("deleted agent still listed")
	}
	if got := connectWith(t, ts.URL, tok2); got != 401 {
		t.Fatalf("deleted agent's token: %d", got)
	}

	got := serverActivity(t, st)
	want := []string{"agent.add office-1", "agent.update office-2", "agent.rotate office-2", "agent.remove office-2"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("activity: %v, want %v", got, want)
	}
}

func TestAdminAgentsCommandBase(t *testing.T) {
	// Without PYRO_PUBLIC_URL the command uses the address the admin used.
	ts, _ := newTeam(t, Config{})
	admin := newClient(t, ts)
	signup(t, admin, "admin@example.com")
	_, out := admin.do("POST", "/api/v1/admin/agents", `{"name":"a","mode":"connect"}`)
	host := strings.TrimPrefix(ts.URL, "http://")
	if out["base"] != "http://"+host || !strings.Contains(out["command"].(string), "--connect 'http://"+host+"' --token '") {
		t.Fatalf("base: %v", out)
	}
}

func TestAgentImageTag(t *testing.T) {
	for in, want := range map[string]string{
		"v0.2.0":               "0.2.0",
		"0.2.0":                "0.2.0",
		"v1.10.3-rc.2":         "1.10.3-rc.2",
		"dev":                  "latest",
		"":                     "latest",
		"v0.1.0-43-gabc":       "latest",
		"v0.1.0-dirty":         "latest",
		"v0.1.0-43-gabc-dirty": "latest",
		"v0.2":                 "latest",
		"vfoo":                 "latest",
	} {
		if got := agentImageTag(in); got != want {
			t.Errorf("agentImageTag(%q) = %q, want %q", in, got, want)
		}
	}
	old := version.Version
	version.Version = "v0.3.1"
	defer func() { version.Version = old }()
	ts, _ := newTeam(t, Config{PublicURL: "http://x"})
	admin := newClient(t, ts)
	signup(t, admin, "admin@example.com")
	_, out := admin.do("POST", "/api/v1/admin/agents", `{"name":"a","mode":"connect"}`)
	if !strings.Contains(out["command"].(string), "ghcr.io/codeen15/pyro:0.3.1 agent") {
		t.Fatalf("command: %v", out["command"])
	}
}

func TestAdminAgentsFlagAgentsReadOnly(t *testing.T) {
	as := testAgent(t, "flagged")
	addr := strings.TrimPrefix(as.URL, "http://")
	ts, _ := newTeam(t, Config{FlagAgents: []string{addr}, FlagAgentToken: "tok"})
	admin := newClient(t, ts)
	signup(t, admin, "admin@example.com")
	id := flagAgentPrefix + addr

	code, list := admin.doList("GET", "/api/v1/admin/agents")
	if code != 200 || len(list) != 1 || list[0]["id"] != id || list[0]["fromFlags"] != true {
		t.Fatalf("list: %d %v", code, list)
	}
	for _, rt := range [][3]string{
		{"PATCH", "/api/v1/admin/agents/" + id, `{"name":"x"}`},
		{"DELETE", "/api/v1/admin/agents/" + id, ""},
		{"POST", "/api/v1/admin/agents/" + id + "/token", ""},
	} {
		if code, out := admin.do(rt[0], rt[1], rt[2]); code != 400 {
			t.Fatalf("%s %s: %d %v", rt[0], rt[1], code, out)
		}
	}
	// An unknown flag id is a 404 like any other unknown id.
	if code, _ := admin.do("POST", "/api/v1/admin/agents/"+flagAgentPrefix+"9.9.9.9:1/test", ""); code != 404 {
		t.Fatalf("test unknown flag id: %d", code)
	}
	// Testing is fine.
	if code, out := admin.do("POST", "/api/v1/admin/agents/"+id+"/test", ""); code != 200 || out["status"] != "available" {
		t.Fatalf("test: %d %v", code, out)
	}
}

// hostile creates a connecting agent with a chosen Host header.
func hostile(t *testing.T, c *client, host string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest("POST", c.ts.URL+"/api/v1/admin/agents", strings.NewReader(`{"name":"agent-for-test","mode":"connect"}`))
	req.Host = host
	// The jar doesn't attach cookies once req.Host differs from the URL's.
	for _, ck := range c.c.Jar.Cookies(req.URL) {
		req.AddCookie(ck)
	}
	req.Header.Set(CSRFHeader, "1")
	res, err := c.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	if res.StatusCode != 201 {
		t.Fatalf("host %q: %d %v", host, res.StatusCode, out)
	}
	return out
}

func TestAdminAgentsCommandIsShellSafe(t *testing.T) {
	// A crafted Host header must not reach a shell: the command falls back to
	// a placeholder and a warning says why.
	for _, host := range []string{"x;id", "x$(id)", "x'y", "x&y", "x!y"} {
		ts, _ := newTeam(t, Config{})
		admin := newClient(t, ts)
		signup(t, admin, "admin@example.com")
		out := hostile(t, admin, host)
		cmd := out["command"].(string)
		beforeToken := strings.SplitN(cmd, " --token", 2)[0]
		if !strings.HasSuffix(beforeToken, "--connect '<server-url>'") || strings.ContainsAny(cmd, ";$&!") || strings.Contains(cmd, "id)") {
			t.Fatalf("host %q: command %q", host, cmd)
		}
		if w, _ := out["warning"].(string); w == "" || !strings.Contains(w, "PYRO_PUBLIC_URL") {
			t.Fatalf("host %q: warning %v", host, out["warning"])
		}
		if out["base"] != "http://"+host {
			t.Fatalf("host %q: base %v", host, out["base"])
		}
	}

	// A public URL that isn't a plain address is refused the same way.
	for _, pub := range []string{"https://pyro.example.com; rm -rf /", "https://a b", "https://x/$(id)", "ftp://x", "https://x'y", "pyro.example.com"} {
		ts, _ := newTeam(t, Config{PublicURL: pub})
		admin := newClient(t, ts)
		signup(t, admin, "admin@example.com")
		_, out := admin.do("POST", "/api/v1/admin/agents", `{"name":"a","mode":"connect"}`)
		cmd, _ := out["command"].(string)
		if !strings.Contains(cmd, "--connect '<server-url>' --token '") || out["warning"] == nil || strings.ContainsAny(cmd, ";$&") {
			t.Fatalf("public URL %q: %q %v", pub, cmd, out["warning"])
		}
	}

	// Ordinary addresses are single-quoted, with no warning.
	for _, pub := range []string{"https://pyro.example.com", "http://10.0.0.5:7878", "https://[::1]:8443", "https://pyro.example.com/base/path"} {
		ts, _ := newTeam(t, Config{PublicURL: pub})
		admin := newClient(t, ts)
		signup(t, admin, "admin@example.com")
		_, out := admin.do("POST", "/api/v1/admin/agents", `{"name":"a","mode":"connect"}`)
		cmd, _ := out["command"].(string)
		if !strings.Contains(cmd, "--connect '"+pub+"' --token '") || out["warning"] != nil {
			t.Fatalf("public URL %q: %q %v", pub, cmd, out["warning"])
		}
	}

	// Rotating goes through the same check.
	ts, _ := newTeam(t, Config{PublicURL: "https://x;y"})
	admin := newClient(t, ts)
	signup(t, admin, "admin@example.com")
	_, out := admin.do("POST", "/api/v1/admin/agents", `{"name":"a","mode":"connect"}`)
	id := out["agent"].(map[string]any)["id"].(string)
	_, out = admin.do("POST", "/api/v1/admin/agents/"+id+"/token", "")
	if cmd, _ := out["command"].(string); !strings.Contains(cmd, "--connect '<server-url>'") || out["warning"] == nil {
		t.Fatalf("rotate: %v", out)
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{"abc": `'abc'`, "": `''`, "a b": `'a b'`, "it's": `'it'\''s'`, "$(x);": `'$(x);'`} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}
