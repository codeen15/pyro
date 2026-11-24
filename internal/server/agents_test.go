package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codeen15/pyro/internal/history"
	"github.com/codeen15/pyro/internal/store"
	"github.com/codeen15/pyro/pkg/engine"
	"github.com/codeen15/pyro/pkg/engine/agent"
	"github.com/codeen15/pyro/pkg/engine/load"
	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/protocol/httpdriver"
)

// agentServer is an in-process agent reachable over HTTP (a --agent flag).
func agentServer(t *testing.T, name string) *httptest.Server {
	t.Helper()
	as := httptest.NewServer((&agent.Server{Token: "tok", Name: name, TickInterval: 100 * time.Millisecond}).Handler())
	t.Cleanup(as.Close)
	return as
}

type agentEnv struct {
	ts  *httptest.Server
	srv *Server
	hub *agent.Hub
}

// newAgentEnv serves a project with these flag agents and, when withHub, a
// hub with one agent ("nat") connected out to it.
func newAgentEnv(t *testing.T, flags []string, withHub bool) agentEnv {
	t.Helper()
	ws, _ := store.Init(t.TempDir(), "test")
	ws.CreateCollection("demo", model.Collection{Name: "Demo"})
	hist, _ := history.Open(filepath.Join(t.TempDir(), "pyro.db"))
	t.Cleanup(func() { hist.Close() })
	eng := engine.New(httpdriver.New(httpdriver.Options{}))
	srv := New(Config{Workspace: ws, Engine: eng, History: hist, Token: testToken, Agents: flags, AgentToken: "tok"})
	env := agentEnv{srv: srv}
	if withHub {
		env.hub = agent.NewHub("tok")
		srv.SetAgentHub(env.hub)
		hs := httptest.NewServer(env.hub.Handler())
		t.Cleanup(hs.Close)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		go (&agent.Server{Token: "tok", Name: "nat", TickInterval: 100 * time.Millisecond}).Connect(ctx, hs.URL, false)
		wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
		defer wcancel()
		if err := env.hub.Wait(wctx, 1); err != nil {
			t.Fatal(err)
		}
	}
	env.ts = httptest.NewServer(srv.Handler())
	t.Cleanup(env.ts.Close)
	return env
}

func listAgentStatus(t *testing.T, ts *httptest.Server) []AgentStatus {
	t.Helper()
	res, body := do(t, ts, "GET", "/api/v1/agents", "", true)
	if res.StatusCode != 200 {
		t.Fatalf("agents: %d %s", res.StatusCode, body)
	}
	var out []AgentStatus
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return out
}

// hubAgentID is the id of the hub's only agent.
func hubAgentID(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	for _, a := range listAgentStatus(t, ts) {
		if strings.HasPrefix(a.ID, "hub:") {
			return a.ID
		}
	}
	t.Fatal("no hub agent listed")
	return ""
}

func assertHubIdle(t *testing.T, hub *agent.Hub) {
	t.Helper()
	for id, in := range hub.ConnectedIDs() {
		if in.Busy {
			t.Fatalf("hub agent %s is still claimed", id)
		}
	}
	if len(hub.ConnectedIDs()) == 0 {
		t.Fatal("hub agent went away")
	}
}

func TestListAgentStatus(t *testing.T) {
	a := agentServer(t, "alpha")
	env := newAgentEnv(t, []string{a.URL, "127.0.0.1:1"}, true)
	got := map[string]AgentStatus{}
	for _, s := range listAgentStatus(t, env.ts) {
		got[s.ID] = s
	}
	up := got["flag:"+a.URL]
	if up.Status != "available" || up.Name != "alpha" || up.Mode != "direct" || !up.FromFlags || up.Address != a.URL || up.CPUs == 0 || up.MaxVUs == 0 || up.Encrypted {
		t.Fatalf("reachable agent: %+v", up)
	}
	down := got["flag:127.0.0.1:1"]
	if down.Status != "offline" || down.Detail == "" || !down.FromFlags {
		t.Fatalf("unreachable agent: %+v", down)
	}
	var hubbed *AgentStatus
	for id, s := range got {
		if strings.HasPrefix(id, "hub:nat@") {
			hubbed = &s
		}
	}
	if hubbed == nil || hubbed.Status != "available" || hubbed.Mode != "connect" || hubbed.Name != "nat" || !hubbed.FromFlags {
		t.Fatalf("hub agent: %+v (all %+v)", hubbed, got)
	}
}

func TestListAgentsJSONShape(t *testing.T) {
	a := agentServer(t, "alpha")
	env := newAgentEnv(t, []string{a.URL}, false)
	_, body := do(t, env.ts, "GET", "/api/v1/agents", "", true)
	for _, k := range []string{`"id":`, `"name":`, `"mode":`, `"address":`, `"status":`, `"version":`, `"cpus":`, `"maxVUs":`, `"encrypted":`, `"fromFlags":`, `"lastSeen":`} {
		if !strings.Contains(body, k) {
			t.Fatalf("missing %s: %s", k, body)
		}
	}
}

const pingPlan = "name: P\nscenarios:\n  s: { executor: per-vu-iterations, vus: 2, iterations: 2, flow: [ { request: demo/ping } ] }\n"

func TestStartOnPickedAgent(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer target.Close()
	a, b := agentServer(t, "a"), agentServer(t, "b")
	env := newAgentEnv(t, []string{a.URL, b.URL}, false)
	do(t, env.ts, "PUT", "/api/v1/requests/demo/ping", `{"name":"Ping","url":"`+target.URL+`"}`, true)
	do(t, env.ts, "PUT", "/api/v1/loadtests/p", loadPlanJSON(pingPlan), true)
	st := runLoad(t, env.ts, "p", `{"distributed":true,"agents":["flag:`+a.URL+`"]}`)
	if st["status"] != "passed" {
		t.Fatalf("run: %+v", st)
	}
	time.Sleep(50 * time.Millisecond)
	_, body := do(t, env.ts, "GET", "/api/v1/loadruns?test=p", "", true)
	var runs []map[string]any
	json.Unmarshal([]byte(body), &runs)
	if len(runs) != 1 {
		t.Fatalf("runs: %s", body)
	}
	_, body = do(t, env.ts, "GET", "/api/v1/loadruns/"+runs[0]["id"].(string), "", true)
	var run struct {
		Result struct {
			Agents []struct{ Name string }
		}
	}
	json.Unmarshal([]byte(body), &run)
	hostA := strings.TrimPrefix(a.URL, "http://")
	if len(run.Result.Agents) != 1 || run.Result.Agents[0].Name != hostA {
		t.Fatalf("want only %s: %s", hostA, body)
	}
}

func TestStartWithOfflineAgent(t *testing.T) {
	env := newAgentEnv(t, []string{"127.0.0.1:1"}, true)
	do(t, env.ts, "PUT", "/api/v1/requests/demo/ping", `{"name":"Ping","url":"http://127.0.0.1:1/"}`, true)
	do(t, env.ts, "PUT", "/api/v1/loadtests/p", loadPlanJSON(pingPlan), true)
	hid := hubAgentID(t, env.ts)
	res, body := do(t, env.ts, "POST", "/api/v1/loadtests/p/start", `{"distributed":true,"agents":["`+hid+`","flag:127.0.0.1:1"]}`, true)
	if res.StatusCode != 409 || !strings.Contains(body, "flag:127.0.0.1:1 (offline)") || !strings.Contains(body, "not available") {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	// All or nothing: the hub agent picked alongside isn't left claimed.
	assertHubIdle(t, env.hub)
	res, body = do(t, env.ts, "POST", "/api/v1/loadtests/p/start", `{"distributed":true,"agents":["flag:nowhere:9"]}`, true)
	if res.StatusCode != 409 || !strings.Contains(body, "flag:nowhere:9") {
		t.Fatalf("unknown agent: %d %s", res.StatusCode, body)
	}
}

func TestRejectedStartReleasesAgents(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer target.Close()
	env := newAgentEnv(t, nil, true)
	do(t, env.ts, "PUT", "/api/v1/requests/demo/ping", `{"name":"Ping","url":"`+target.URL+`"}`, true)
	do(t, env.ts, "PUT", "/api/v1/requests/demo/far", `{"name":"Far","url":"http://api.example.com/"}`, true)
	do(t, env.ts, "PUT", "/api/v1/loadtests/p", loadPlanJSON(pingPlan), true)
	do(t, env.ts, "PUT", "/api/v1/loadtests/far", loadPlanJSON("scenarios:\n  s: { executor: per-vu-iterations, vus: 1, iterations: 1, flow: [ { request: demo/far } ] }\n"), true)
	do(t, env.ts, "PUT", "/api/v1/loadtests/long", loadPlanJSON("scenarios:\n  s: { executor: constant-vus, vus: 1, duration: 30s, flow: [ { request: demo/ping } ] }\n"), true)

	// An unknown environment.
	res, body := do(t, env.ts, "POST", "/api/v1/loadtests/p/start", `{"distributed":true,"environment":"nope"}`, true)
	if res.StatusCode != 404 {
		t.Fatalf("environment: %d %s", res.StatusCode, body)
	}
	assertHubIdle(t, env.hub)

	// Hosts that need confirming.
	res, body = do(t, env.ts, "POST", "/api/v1/loadtests/far/start", `{"distributed":true}`, true)
	if res.StatusCode != 409 || !strings.Contains(body, "needsConfirmation") {
		t.Fatalf("hosts: %d %s", res.StatusCode, body)
	}
	assertHubIdle(t, env.hub)

	// Another test already running.
	res, body = do(t, env.ts, "POST", "/api/v1/loadtests/long/start", `{}`, true)
	if res.StatusCode != 202 {
		t.Fatalf("long: %d %s", res.StatusCode, body)
	}
	var started struct{ ID string }
	json.Unmarshal([]byte(body), &started)
	defer do(t, env.ts, "POST", "/api/v1/loadruns/"+started.ID+"/stop?abort=1", "", true)
	res, body = do(t, env.ts, "POST", "/api/v1/loadtests/p/start", `{"distributed":true}`, true)
	if res.StatusCode != 409 || !strings.Contains(body, "already running") {
		t.Fatalf("running: %d %s", res.StatusCode, body)
	}
	assertHubIdle(t, env.hub)
}

func TestNoAgentsMessage(t *testing.T) {
	env := newAgentEnv(t, nil, false)
	do(t, env.ts, "PUT", "/api/v1/requests/demo/ping", `{"name":"Ping","url":"http://127.0.0.1:1/"}`, true)
	do(t, env.ts, "PUT", "/api/v1/loadtests/p", loadPlanJSON(pingPlan), true)
	res, body := do(t, env.ts, "POST", "/api/v1/loadtests/p/start", `{"distributed":true}`, true)
	if res.StatusCode != 400 || !strings.Contains(body, "no agents") || !strings.Contains(body, "--agent") {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
}

func TestAgentsChangedEvent(t *testing.T) {
	env := newAgentEnv(t, nil, true)
	c := subscribe(t, env.ts, "agents")
	hid := strings.TrimPrefix(hubAgentID(t, env.ts), "hub:")
	env.hub.Disconnect(hid)
	if ev := readEvent(t, c); ev.Topic != "agents" || ev.Type != "changed" {
		t.Fatalf("event: %+v", ev)
	}
}

type fakeSource struct {
	listed  []AgentStatus
	claimed [][]string
	cancels int
}

func (f *fakeSource) List(context.Context) []AgentStatus { return f.listed }
func (f *fakeSource) Claim(_ context.Context, ids []string) ([]load.Worker, error) {
	f.claimed = append(f.claimed, ids)
	return nil, &UnavailableError{Agents: map[string]string{"x": "busy"}}
}
func (f *fakeSource) Release([]load.Worker)            {}
func (f *fakeSource) Subscribe(func()) (cancel func()) { return func() { f.cancels++ } }

func TestConfigAgentSource(t *testing.T) {
	ws, _ := store.Init(t.TempDir(), "test")
	ws.CreateCollection("demo", model.Collection{Name: "Demo"})
	src := &fakeSource{listed: []AgentStatus{{ID: "x", Name: "Lab", Status: "busy"}}}
	srv := New(Config{Workspace: ws, Token: testToken, AgentSource: src})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	if got := listAgentStatus(t, ts); len(got) != 1 || got[0].Name != "Lab" {
		t.Fatalf("list: %+v", got)
	}
	do(t, ts, "PUT", "/api/v1/requests/demo/ping", `{"name":"Ping","url":"http://127.0.0.1:1/"}`, true)
	do(t, ts, "PUT", "/api/v1/loadtests/p", loadPlanJSON(pingPlan), true)
	res, body := do(t, ts, "POST", "/api/v1/loadtests/p/start", `{"distributed":true,"agents":["x"]}`, true)
	if res.StatusCode != 409 || !strings.Contains(body, "x (busy)") || len(src.claimed) != 1 || src.claimed[0][0] != "x" {
		t.Fatalf("%d %s %v", res.StatusCode, body, src.claimed)
	}
	srv.Close()
	if src.cancels != 1 {
		t.Fatal("Close didn't cancel the agents subscription")
	}
}
