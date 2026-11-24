package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/codeen15/pyro/pkg/engine/load"
)

// tokenAuth gives each agent token "tok-<id>" the id <id>.
func tokenAuth(token string) (string, bool) {
	id, ok := strings.CutPrefix(token, "tok-")
	return id, ok && id != ""
}

func serveHub(t *testing.T, h *Hub) *httptest.Server {
	hs := httptest.NewServer(h.Handler())
	t.Cleanup(hs.Close)
	return hs
}

// startAgent runs a dial-out agent until the test ends.
func startAgent(t *testing.T, url, token, name string) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.Cleanup(func() { cancel(); <-done })
	s := &Server{Token: token, Name: name, TickInterval: 50 * time.Millisecond}
	go func() { defer close(done); s.Connect(ctx, url, false) }()
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHubPerAgentAuth(t *testing.T) {
	h := NewHub("")
	h.Authenticate = tokenAuth
	hs := serveHub(t, h)

	bad := &Server{Token: "nope", Name: "bad"}
	if err := bad.Connect(context.Background(), hs.URL, false); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("bad token: %v", err)
	}
	startAgent(t, hs.URL, "tok-a", "alpha")
	startAgent(t, hs.URL, "tok-b", "beta")
	eventually(t, "two agents", func() bool { return len(h.ConnectedIDs()) == 2 })
	ids := h.ConnectedIDs()
	if ids["a"].Name != "alpha" || ids["b"].Name != "beta" || ids["a"].Busy || ids["b"].Busy {
		t.Fatalf("connected: %+v", ids)
	}
	if got := h.Connected(); len(got) != 2 || got[0].Name != "alpha" {
		t.Fatalf("Connected: %+v", got)
	}
}

func TestHubSameIDReplacesOldConnection(t *testing.T) {
	h := NewHub("")
	h.Authenticate = tokenAuth
	hs := serveHub(t, h)

	// A first connection for id "a", opened by hand so we can watch it.
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer tok-a")
	hdr.Set(protocolHeader, strconv.Itoa(Protocol))
	ctx := context.Background()
	first, _, err := websocket.Dial(ctx, "ws"+hs.URL[4:]+"/agent/v1/connect", &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatal(err)
	}
	defer first.CloseNow()
	if err := wsjson.Write(ctx, first, message{Type: "hello", Info: &Info{Name: "old"}}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "first connection", func() bool { return h.ConnectedIDs()["a"].Name == "old" })

	startAgent(t, hs.URL, "tok-a", "new")
	eventually(t, "replacement", func() bool { return h.ConnectedIDs()["a"].Name == "new" })
	if n := len(h.ConnectedIDs()); n != 1 {
		t.Fatalf("%d connections for one id", n)
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var m message
	if err := wsjson.Read(rctx, first, &m); err == nil || rctx.Err() != nil {
		t.Fatalf("first connection not closed: %v (%+v)", err, m)
	}
}

func TestHubClaimIDsReleaseAndNames(t *testing.T) {
	tg, hits := target(t)
	h := NewHub("")
	h.Authenticate = tokenAuth
	h.NameFor = func(id string) string { return "Agent " + strings.ToUpper(id) }
	var changes atomic.Int64
	h.OnChange = func() { changes.Add(1) }
	hs := serveHub(t, h)
	startAgent(t, hs.URL, "tok-a", "alpha")
	startAgent(t, hs.URL, "tok-b", "beta")
	eventually(t, "two agents", func() bool { return len(h.ConnectedIDs()) == 2 })
	if changes.Load() < 2 {
		t.Fatalf("OnChange after connect: %d", changes.Load())
	}

	// All or nothing: an offline id claims nothing.
	ws, un := h.ClaimIDs([]string{"a", "x"})
	if ws != nil || un["x"] != "offline" || len(un) != 1 {
		t.Fatalf("offline claim: %v %v", ws, un)
	}
	if len(h.Connected()) != 2 {
		t.Fatalf("a failed claim took agents: %+v", h.Connected())
	}

	before := changes.Load()
	ws, un = h.ClaimIDs([]string{"a"})
	if len(ws) != 1 || un != nil {
		t.Fatalf("claim a: %v %v", ws, un)
	}
	if ws[0].Name() != "Agent A" {
		t.Fatalf("worker name %q", ws[0].Name())
	}
	if !h.ConnectedIDs()["a"].Busy || len(h.Connected()) != 1 || changes.Load() == before {
		t.Fatalf("after claim: %+v, OnChange %d->%d", h.ConnectedIDs(), before, changes.Load())
	}
	if ws2, un := h.ClaimIDs([]string{"a", "b"}); ws2 != nil || un["a"] != "busy" || len(un) != 1 {
		t.Fatalf("busy claim: %v %v", ws2, un)
	}

	before = changes.Load()
	h.Release(ws)
	if h.ConnectedIDs()["a"].Busy || len(h.Connected()) != 2 || changes.Load() == before {
		t.Fatalf("after release: %+v", h.ConnectedIDs())
	}

	// The released connection still runs a job.
	ws, un = h.ClaimIDs([]string{"a", "b"})
	if len(ws) != 2 || un != nil {
		t.Fatalf("claim both: %v %v", ws, un)
	}
	p := plan(t, `
name: by-id
scenarios:
  s: { executor: per-vu-iterations, vus: 2, iterations: 3, flow: [ { request: ok } ] }
`)
	test, err := load.Start(p, load.Options{Resolve: resolver(tg.URL), TickInterval: 100 * time.Millisecond, Workers: ws})
	if err != nil {
		t.Fatal(err)
	}
	res := test.Wait()
	if res.Status != load.StatusPassed || hits.Load() != 6 || len(res.Agents) != 2 || res.Agents[0].Name != "Agent A" {
		t.Fatalf("%s (%s), %d hits, agents %+v", res.Status, res.Reason, hits.Load(), res.Agents)
	}
	// Agents reconnect after the job.
	eventually(t, "agents back", func() bool { return len(h.Connected()) == 2 })
}

func TestHubReleaseAfterPrepareCloses(t *testing.T) {
	h := NewHub("")
	h.Authenticate = tokenAuth
	hs := serveHub(t, h)
	startAgent(t, hs.URL, "tok-a", "alpha")
	eventually(t, "agent", func() bool { return len(h.Connected()) == 1 })
	ws, _ := h.ClaimIDs([]string{"a"})
	p := plan(t, `
name: prepared
scenarios:
  s: { executor: per-vu-iterations, vus: 1, iterations: 1, flow: [ { request: ok } ] }
`)
	// The agent may accept or reject the job; either way it was sent, so
	// the connection can't go back to idle.
	ws[0].Prepare(context.Background(), load.Job{Plan: p, Segments: 1})
	h.Release(ws)
	// The agent ran nothing, so it reconnects as a fresh connection; the
	// released one must not be handed out again in the meantime.
	for _, in := range h.ConnectedIDs() {
		if in.Busy {
			t.Fatalf("released prepared worker still listed busy")
		}
	}
	eventually(t, "agent back", func() bool { return len(h.Connected()) == 1 })
}

func TestHubDisconnect(t *testing.T) {
	h := NewHub("")
	h.Authenticate = tokenAuth
	var changes atomic.Int64
	h.OnChange = func() { changes.Add(1) }
	hs := serveHub(t, h)
	startAgent(t, hs.URL, "tok-a", "alpha")
	startAgent(t, hs.URL, "tok-b", "beta")
	eventually(t, "two agents", func() bool { return len(h.ConnectedIDs()) == 2 })
	before := changes.Load()
	h.Disconnect("a")
	ids := h.ConnectedIDs()
	if _, ok := ids["a"]; ok || len(ids) != 1 || changes.Load() == before {
		t.Fatalf("after disconnect: %+v", ids)
	}
}

// A connection that authenticated before its agent was disconnected (the
// agent removed or its token rotated) is refused when its hello arrives.
func TestHubDisconnectBeforeHello(t *testing.T) {
	h := NewHub("")
	h.Authenticate = tokenAuth
	hs := serveHub(t, h)
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer tok-a")
	hdr.Set(protocolHeader, strconv.Itoa(Protocol))
	ctx := context.Background()
	c, _, err := websocket.Dial(ctx, "ws"+hs.URL[4:]+"/agent/v1/connect", &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	h.Disconnect("a")
	if err := wsjson.Write(ctx, c, message{Type: "hello", Info: &Info{Name: "late"}}); err != nil {
		t.Fatal(err)
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var m message
	if err := wsjson.Read(rctx, c, &m); err == nil || rctx.Err() != nil {
		t.Fatalf("connection not closed: %v (%+v)", err, m)
	}
	if _, ok := h.ConnectedIDs()["a"]; ok {
		t.Fatal("revoked connection was kept")
	}
	// A new connection after the disconnect is fine.
	startAgent(t, hs.URL, "tok-a", "again")
	eventually(t, "reconnect", func() bool { return h.ConnectedIDs()["a"].Name == "again" })
}

func TestHubSingleTokenIDs(t *testing.T) {
	h := NewHub("tok")
	hs := serveHub(t, h)
	startAgent(t, hs.URL, "tok", "n1")
	eventually(t, "agent", func() bool { return len(h.ConnectedIDs()) == 1 })
	var id string
	for k := range h.ConnectedIDs() {
		id = k
	}
	if !strings.HasPrefix(id, "n1@127.0.0.1:") {
		t.Fatalf("id %q", id)
	}
	ws, un := h.ClaimIDs([]string{id})
	if len(ws) != 1 || un != nil || ws[0].Name() != "n1" {
		t.Fatalf("claim: %v %v", ws, un)
	}
	h.Release(ws)
	if len(h.Connected()) != 1 {
		t.Fatal("not idle after release")
	}
	h.Disconnect(id)
	if len(h.ConnectedIDs()) != 0 {
		t.Fatal("still connected after disconnect")
	}
}

// Idle connections answer the hub's keepalive pings and stay connected.
func TestHubIdleKeepalive(t *testing.T) {
	h := NewHub("tok")
	h.pingEvery = 50 * time.Millisecond
	var changes atomic.Int64
	h.OnChange = func() { changes.Add(1) }
	hs := serveHub(t, h)
	startAgent(t, hs.URL, "tok", "n1")
	eventually(t, "agent", func() bool { return len(h.Connected()) == 1 })
	n := changes.Load()
	time.Sleep(time.Second)
	if len(h.Connected()) != 1 || changes.Load() != n {
		t.Fatalf("idle connection churned: %d changes, connected %+v", changes.Load()-n, h.Connected())
	}
}
