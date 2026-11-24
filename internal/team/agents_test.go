package team

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/codeen15/pyro/internal/db/dbtest"
	"github.com/codeen15/pyro/internal/history"
	"github.com/codeen15/pyro/internal/server"
	"github.com/codeen15/pyro/internal/store"
	"github.com/codeen15/pyro/pkg/engine"
	"github.com/codeen15/pyro/pkg/engine/agent"
	"github.com/codeen15/pyro/pkg/engine/protocol/httpdriver"
)

// testAgent is an in-process direct agent.
func testAgent(t *testing.T, name string) *httptest.Server {
	t.Helper()
	as := httptest.NewServer((&agent.Server{Token: "tok", Name: name, Version: "0.2.0", TickInterval: 100 * time.Millisecond}).Handler())
	t.Cleanup(as.Close)
	return as
}

// dialIn runs an agent that connects out to the team server until the test ends.
func dialIn(t *testing.T, url, token, name string) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		done <- (&agent.Server{Token: token, Name: name, Version: "0.2.0", TickInterval: 100 * time.Millisecond}).Connect(ctx, url, false)
	}()
	return done
}

func agentByID(list []server.AgentStatus, id string) (server.AgentStatus, bool) {
	for _, a := range list {
		if a.ID == id {
			return a, true
		}
	}
	return server.AgentStatus{}, false
}

// waitStatus polls the registry until the agent has the status.
func waitStatus(t *testing.T, r *agentRegistry, id, status string, within time.Duration) server.AgentStatus {
	t.Helper()
	deadline := time.Now().Add(within)
	var last server.AgentStatus
	for time.Now().Before(deadline) {
		if a, ok := agentByID(r.List(context.Background()), id); ok {
			last = a
			if a.Status == status {
				return a
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("agent %s: want %s within %s, have %+v", id, status, within, last)
	return last
}

func TestRegistryDirectAgent(t *testing.T) {
	ctx := context.Background()
	ts, srv, st := newTeamServer(t, Config{})
	as := testAgent(t, "remote")
	a, err := st.AddDirectAgent(ctx, "vps-sg", as.URL, "tok", false, "")
	if err != nil {
		t.Fatal(err)
	}
	got := srv.agents.Probe(ctx, a.ID)
	if got.Status != "available" || got.Name != "vps-sg" || got.Version != "0.2.0" || got.CPUs == 0 || got.MaxVUs == 0 || got.Mode != "direct" || got.Encrypted || got.FromFlags || got.Address != as.URL || got.LastSeen == 0 {
		t.Fatalf("probe: %+v", got)
	}
	listed, ok := agentByID(srv.agents.List(ctx), a.ID)
	if !ok || listed.Status != "available" || listed.Version != "0.2.0" || listed.CPUs == 0 {
		t.Fatalf("list: %+v", listed)
	}

	// A wrong token is an error; an unreachable address is offline.
	bad, _ := st.AddDirectAgent(ctx, "wrong-token", as.URL, "nope", false, "")
	if got := srv.agents.Probe(ctx, bad.ID); got.Status != "error" || got.Detail == "" {
		t.Fatalf("wrong token: %+v", got)
	}
	down, _ := st.AddDirectAgent(ctx, "down", "127.0.0.1:1", "tok", false, "")
	if got := srv.agents.Probe(ctx, down.ID); got.Status != "offline" {
		t.Fatalf("unreachable: %+v", got)
	}

	// Projects see the registry through GET /api/v1/agents.
	alice := newClient(t, ts)
	pid := firstProject(t, signup(t, alice, "alice@example.com"))
	code, list := alice.doList("GET", "/api/v1/projects/"+pid+"/agents")
	if code != 200 || len(list) != 3 || !strings.Contains(toJSON(list), `"vps-sg"`) {
		t.Fatalf("project agents: %d %v", code, list)
	}
}

func TestRegistryConnectAgent(t *testing.T) {
	ctx := context.Background()
	ts, srv, st := newTeamServer(t, Config{})
	a, token, err := st.AddConnectAgent(ctx, "office-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := agentByID(srv.agents.List(ctx), a.ID); got.Status != "offline" {
		t.Fatalf("before connecting: %+v", got)
	}
	dialIn(t, ts.URL, token, "machine-name")
	got := waitStatus(t, srv.agents, a.ID, "available", 2*time.Second)
	if got.Name != "office-1" || got.Mode != "connect" || got.Encrypted || got.Version != "0.2.0" || got.CPUs == 0 || got.LastSeen == 0 {
		t.Fatalf("connected: %+v", got)
	}
	if stored, _ := st.Agents(ctx); stored[0].LastSeen.IsZero() {
		t.Fatal("connecting should record last seen")
	}
}

func connectWith(t *testing.T, url, token string) int {
	t.Helper()
	req, _ := http.NewRequest("GET", url+"/agent/v1/connect", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Pyro-Agent-Protocol", "1")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func TestRegistryConnectRateLimit(t *testing.T) {
	ts, srv, st := newTeamServer(t, Config{})
	_, token, _ := st.AddConnectAgent(context.Background(), "office-1", "")
	for i := range 5 {
		if code := connectWith(t, ts.URL, "pyro_agent_wrong"); code != 401 {
			t.Fatalf("attempt %d: %d", i+1, code)
		}
	}
	if code := connectWith(t, ts.URL, "pyro_agent_wrong"); code != 429 {
		t.Fatalf("6th attempt: %d", code)
	}
	// Only failures are limited: behind a proxy every agent shares one
	// address, so a valid token still connects while it is blocked.
	a, _ := st.Agents(context.Background())
	dialIn(t, ts.URL, token, "m")
	waitStatus(t, srv.agents, a[0].ID, "available", 2*time.Second)
	if code := connectWith(t, ts.URL, "pyro_agent_wrong"); code != 429 {
		t.Fatalf("wrong token while blocked: %d", code)
	}
}

func TestRegistryConnectLimiterForgetsOldEntries(t *testing.T) {
	l := newConnectLimiter(5, time.Minute)
	now := time.Unix(1000, 0)
	for range 5 {
		l.fail("1.2.3.4", now)
	}
	l.fail("5.6.7.8", now)
	l.sweep(now.Add(2 * time.Minute))
	if len(l.fails) != 0 || len(l.until) != 0 {
		t.Fatalf("expired entries kept: %v %v", l.fails, l.until)
	}
	// Sweeps also happen by themselves as addresses pile up.
	for i := range 3000 {
		l.fail(net.IPv4(10, 0, byte(i>>8), byte(i)).String(), now.Add(time.Duration(i)*time.Second))
	}
	if n := len(l.fails) + len(l.until); n > 1100 {
		t.Fatalf("%d entries kept", n)
	}
}

func TestRegistryLimiterKeysIPv6By64(t *testing.T) {
	if limiterKey("2001:db8:1:2:aaaa::1") != limiterKey("2001:db8:1:2:bbbb::9") {
		t.Fatal("addresses in one /64 should share a key")
	}
	if limiterKey("2001:db8:1:2::1") == limiterKey("2001:db8:1:3::1") || limiterKey("10.0.0.1") == limiterKey("10.0.0.2") {
		t.Fatal("different networks share a key")
	}
}

// rawConnect opens a dial-in connection without sending hello yet.
func rawConnect(t *testing.T, url, token string) *websocket.Conn {
	t.Helper()
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+token)
	hdr.Set("Pyro-Agent-Protocol", "1")
	c, _, err := websocket.Dial(context.Background(), "ws"+url[4:]+"/agent/v1/connect", &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c
}

func TestRegistryForgetBeforeHello(t *testing.T) {
	ctx := context.Background()
	for _, how := range []string{"delete", "rotate"} {
		t.Run(how, func(t *testing.T) {
			ts, srv, st := newTeamServer(t, Config{})
			a, token, _ := st.AddConnectAgent(ctx, "office-1", "")
			c := rawConnect(t, ts.URL, token)
			if how == "delete" {
				if err := st.DeleteAgent(ctx, a.ID); err != nil {
					t.Fatal(err)
				}
			} else if _, err := st.RotateAgentToken(ctx, a.ID); err != nil {
				t.Fatal(err)
			}
			srv.agents.Forget(a.ID)
			hello := map[string]any{"type": "hello", "info": map[string]any{"name": "late", "cpus": 1}}
			if err := wsjson.Write(ctx, c, hello); err != nil {
				t.Fatal(err)
			}
			rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			var m map[string]any
			if err := wsjson.Read(rctx, c, &m); err == nil || rctx.Err() != nil {
				t.Fatalf("connection not closed: %v %v", err, m)
			}
			if _, ok := srv.agents.hub.ConnectedIDs()[a.ID]; ok {
				t.Fatal("connection kept after Forget")
			}
		})
	}
}

func TestRegistryConnectLimiterWindow(t *testing.T) {
	l := newConnectLimiter(5, time.Minute)
	now := time.Unix(1000, 0)
	for range 4 {
		l.fail("1.2.3.4", now)
	}
	if l.blocked("1.2.3.4", now) {
		t.Fatal("blocked after 4 failures")
	}
	l.fail("1.2.3.4", now.Add(30*time.Second))
	if !l.blocked("1.2.3.4", now.Add(31*time.Second)) || l.blocked("5.6.7.8", now) {
		t.Fatal("5 failures in a minute should block that address only")
	}
	if !l.blocked("1.2.3.4", now.Add(89*time.Second)) || l.blocked("1.2.3.4", now.Add(91*time.Second)) {
		t.Fatal("the block should last a minute")
	}
	// Failures spread over more than a minute don't add up.
	for i := range 5 {
		l.fail("9.9.9.9", now.Add(time.Duration(i)*20*time.Second))
	}
	if l.blocked("9.9.9.9", now.Add(81*time.Second)) {
		t.Fatal("old failures should expire")
	}
}

func TestRegistryForgetDropsAgent(t *testing.T) {
	ctx := context.Background()
	ts, srv, st := newTeamServer(t, Config{})
	a, token, _ := st.AddConnectAgent(ctx, "office-1", "")
	done := dialIn(t, ts.URL, token, "m")
	waitStatus(t, srv.agents, a.ID, "available", 2*time.Second)

	if err := st.DeleteAgent(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	srv.agents.Forget(a.ID)
	if _, ok := srv.agents.hub.ConnectedIDs()[a.ID]; ok {
		t.Fatal("agent still connected after Forget")
	}
	if _, ok := agentByID(srv.agents.List(ctx), a.ID); ok {
		t.Fatal("forgotten agent still listed")
	}
	// The agent's reconnect is refused, so it gives up.
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "401") {
			t.Fatalf("agent stopped with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("agent kept connecting with a revoked token")
	}
	if code := connectWith(t, ts.URL, token); code != 401 {
		t.Fatalf("revoked token: %d", code)
	}
}

func TestRegistryFlagAgents(t *testing.T) {
	ctx := context.Background()
	as := testAgent(t, "flagged")
	_, srv, _ := newTeamServer(t, Config{FlagAgents: []string{as.URL}, FlagAgentToken: "tok"})
	id := "flag:" + as.URL
	if got := srv.agents.Probe(ctx, id); got.Status != "available" || !got.FromFlags || got.Mode != "direct" {
		t.Fatalf("probe: %+v", got)
	}
	got, ok := agentByID(srv.agents.List(ctx), id)
	if !ok || !got.FromFlags || got.Status != "available" || got.Address != as.URL {
		t.Fatalf("list: %+v", got)
	}
}

// project signs alice in and sets up a project with a request to target
// and load tests.
func agentProject(t *testing.T, ts *httptest.Server, target string, plans map[string]string) (*client, string) {
	t.Helper()
	alice := newClient(t, ts)
	pid := firstProject(t, signup(t, alice, "alice@example.com"))
	base := "/api/v1/projects/" + pid
	if code, out := alice.do("POST", base+"/collections", `{"slug":"demo","name":"Demo"}`); code != 201 {
		t.Fatalf("collection: %d %v", code, out)
	}
	if code, out := alice.do("PUT", base+"/requests/demo/ping", `{"name":"Ping","url":"`+target+`"}`); code >= 300 {
		t.Fatalf("request: %d %v", code, out)
	}
	for name, plan := range plans {
		b, _ := json.Marshal(map[string]string{"yaml": plan})
		if code, out := alice.do("PUT", base+"/loadtests/"+name, string(b)); code >= 300 {
			t.Fatalf("load test: %d %v", code, out)
		}
	}
	return alice, base
}

// waitRun polls a load run until it isn't running.
func waitRun(t *testing.T, c *client, base, id string, within time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		_, out := c.do("GET", base+"/loadruns/"+id, "")
		if s, _ := out["status"].(string); s != "" && s != "running" {
			return out
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("run %s still running after %s", id, within)
	return nil
}

func okTarget(t *testing.T) *httptest.Server {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	t.Cleanup(target.Close)
	return target
}

func TestRegistryRunNamesAndUnavailable(t *testing.T) {
	ctx := context.Background()
	ts, srv, st := newTeamServer(t, Config{})
	direct, _ := st.AddDirectAgent(ctx, "vps-sg", testAgent(t, "host-a").URL, "tok", false, "")
	conn, token, _ := st.AddConnectAgent(ctx, "office-1", "")
	down, _ := st.AddDirectAgent(ctx, "vps-down", "127.0.0.1:1", "tok", false, "")
	dialIn(t, ts.URL, token, "host-b")
	waitStatus(t, srv.agents, conn.ID, "available", 2*time.Second)

	alice, base := agentProject(t, ts, okTarget(t).URL, map[string]string{
		"p": "name: P\nscenarios:\n  s: { executor: per-vu-iterations, vus: 2, iterations: 2, flow: [ { request: demo/ping } ] }\n",
	})
	code, out := alice.do("POST", base+"/loadtests/p/start", `{"distributed":true,"confirmHosts":true,"agents":["`+direct.ID+`","`+conn.ID+`"]}`)
	if code != 202 {
		t.Fatalf("start: %d %v", code, out)
	}
	run := waitRun(t, alice, base, out["id"].(string), 20*time.Second)
	if run["status"] != "passed" {
		t.Fatalf("run: %v", run)
	}
	names := map[string]bool{}
	for _, a := range run["result"].(map[string]any)["agents"].([]any) {
		names[a.(map[string]any)["name"].(string)] = true
	}
	if len(names) != 2 || !names["vps-sg"] || !names["office-1"] {
		t.Fatalf("worker names: %v", names)
	}

	// An unavailable agent is named by its registry name, not its id.
	waitStatus(t, srv.agents, conn.ID, "available", 5*time.Second)
	code, out = alice.do("POST", base+"/loadtests/p/start", `{"distributed":true,"confirmHosts":true,"agents":["`+down.ID+`","`+conn.ID+`"]}`)
	msg, _ := out["error"].(string)
	if code != 409 || !strings.Contains(msg, "vps-down (offline)") || strings.Contains(msg, down.ID) {
		t.Fatalf("unavailable: %d %v", code, out)
	}
	// All or nothing: the connected agent is still free.
	if got, _ := agentByID(srv.agents.List(ctx), conn.ID); got.Status != "available" {
		t.Fatalf("connected agent after a refused start: %+v", got)
	}
}

func TestRegistryRemoveDuringRun(t *testing.T) {
	ctx := context.Background()
	ts, srv, st := newTeamServer(t, Config{})
	conn, token, _ := st.AddConnectAgent(ctx, "office-1", "")
	dialIn(t, ts.URL, token, "host-b")
	waitStatus(t, srv.agents, conn.ID, "available", 2*time.Second)
	alice, base := agentProject(t, ts, okTarget(t).URL, map[string]string{
		"long": "name: Long\nscenarios:\n  s: { executor: constant-vus, vus: 1, duration: 2m, flow: [ { request: demo/ping } ] }\n",
	})
	code, out := alice.do("POST", base+"/loadtests/long/start", `{"distributed":true,"confirmHosts":true,"agents":["`+conn.ID+`"]}`)
	if code != 202 {
		t.Fatalf("start: %d %v", code, out)
	}
	id := out["id"].(string)
	waitStatus(t, srv.agents, conn.ID, "busy", 2*time.Second)
	time.Sleep(300 * time.Millisecond)

	if err := st.DeleteAgent(ctx, conn.ID); err != nil {
		t.Fatal(err)
	}
	srv.agents.Forget(conn.ID)
	run := waitRun(t, alice, base, id, 30*time.Second)
	reason, _ := run["result"].(map[string]any)["reason"].(string)
	if run["status"] != "failed" || !strings.Contains(reason, "office-1") {
		t.Fatalf("run after removing its agent: %v %q", run["status"], reason)
	}
}

func TestRegistryAfterRestart(t *testing.T) {
	ctx := context.Background()
	d := dbtest.Open(t)
	st, err := NewStore(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	hist, _ := history.New(ctx, d)
	cfg := Config{DataDir: t.TempDir(), Store: st, JWTSecret: []byte("test-secret-test-secret-test-secret!"),
		Project: func(ws *store.Workspace, id string) server.Config {
			return server.Config{Engine: engine.New(httpdriver.New(httpdriver.Options{FileRoot: ws.Root})), History: hist}
		}}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	srv1 := New(cfg)
	hs1 := &http.Server{Handler: srv1.Handler()}
	go hs1.Serve(ln)

	direct, _ := st.AddDirectAgent(ctx, "vps-sg", testAgent(t, "a").URL, "tok", false, "")
	conn, token, _ := st.AddConnectAgent(ctx, "office-1", "")
	dialIn(t, "http://"+addr, token, "b")
	waitStatus(t, srv1.agents, conn.ID, "available", 2*time.Second)

	// Restart: the old server goes away and a new one serves the same database.
	hs1.Close()
	srv1.Close()
	srv2 := New(cfg)
	t.Cleanup(srv2.Close)
	var ln2 net.Listener
	for range 50 {
		if ln2, err = net.Listen("tcp", addr); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	hs2 := &http.Server{Handler: srv2.Handler()}
	go hs2.Serve(ln2)
	t.Cleanup(func() { hs2.Close() })

	// The direct agent is loaded from the database and probed on start.
	waitStatus(t, srv2.agents, direct.ID, "available", 5*time.Second)
	// The connecting agent dials back in by itself.
	waitStatus(t, srv2.agents, conn.ID, "available", 15*time.Second)
}

// Registry agents that get no share of the load are handed back when the
// test starts: connected agents return to the idle pool and direct ones
// are re-probed, instead of staying busy.
func TestRegistryUnusedAgentsReleased(t *testing.T) {
	ctx := context.Background()
	ts, srv, st := newTeamServer(t, Config{})
	direct, _ := st.AddDirectAgent(ctx, "vps-sg", testAgent(t, "host-a").URL, "tok", false, "")
	c1, tok1, _ := st.AddConnectAgent(ctx, "office-1", "")
	c2, tok2, _ := st.AddConnectAgent(ctx, "office-2", "")
	dialIn(t, ts.URL, tok1, "host-b")
	dialIn(t, ts.URL, tok2, "host-c")
	waitStatus(t, srv.agents, c1.ID, "available", 2*time.Second)
	waitStatus(t, srv.agents, c2.ID, "available", 2*time.Second)

	alice, base := agentProject(t, ts, okTarget(t).URL, map[string]string{
		"one": "name: One\nscenarios:\n  s: { executor: per-vu-iterations, vus: 1, iterations: 1, flow: [ { request: demo/ping } ] }\n",
	})
	code, out := alice.do("POST", base+"/loadtests/one/start", `{"distributed":true,"confirmHosts":true}`)
	if code != 202 {
		t.Fatalf("start: %d %v", code, out)
	}
	if run := waitRun(t, alice, base, out["id"].(string), 20*time.Second); run["status"] != "passed" {
		t.Fatalf("run: %v", run)
	}
	for _, id := range []string{direct.ID, c1.ID, c2.ID} {
		waitStatus(t, srv.agents, id, "available", 2*time.Second)
	}
	workers, err := srv.agents.Claim(ctx, []string{direct.ID, c1.ID, c2.ID})
	if err != nil || len(workers) != 3 {
		t.Fatalf("claim again: %d %v", len(workers), err)
	}
	srv.agents.Release(workers)
}
