package server

import (
	"encoding/json"
	"path/filepath"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pyrohq/pyro/internal/history"
	"github.com/pyrohq/pyro/internal/store"
	"github.com/pyrohq/pyro/pkg/engine"
	"github.com/pyrohq/pyro/pkg/engine/agent"
	"github.com/pyrohq/pyro/pkg/engine/model"
	"github.com/pyrohq/pyro/pkg/engine/protocol/httpdriver"
)

// runLoad starts a load test and waits for its "finished" event.
func runLoad(t *testing.T, ts *httptest.Server, name, body string) map[string]any {
	t.Helper()
	res, b := do(t, ts, "POST", "/api/v1/loadtests/"+name+"/start", body, true)
	var started struct{ ID string }
	json.Unmarshal([]byte(b), &started)
	if started.ID == "" {
		t.Fatalf("start: %d %s", res.StatusCode, b)
	}
	ev := subscribe(t, ts, "load:"+started.ID)
	// The test may have finished before we subscribed; fall back to the state.
	_, state := do(t, ts, "GET", "/api/v1/loadruns/"+started.ID, "", true)
	var st map[string]any
	json.Unmarshal([]byte(state), &st)
	if st["status"] != nil && st["status"] != "running" {
		return st
	}
	for {
		if e := readEvent(t, ev); e.Type == "finished" {
			return e.Data.(map[string]any)
		}
	}
}

func TestDistributedLoadTest(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer target.Close()
	var agents []string
	for _, name := range []string{"a", "b"} {
		as := httptest.NewServer((&agent.Server{Token: "tok", Name: name, TickInterval: 100 * time.Millisecond}).Handler())
		defer as.Close()
		agents = append(agents, as.URL)
	}
	ws, _ := store.Init(t.TempDir(), "test")
	ws.CreateCollection("demo", model.Collection{Name: "Demo"})
	hist, _ := history.Open(filepath.Join(t.TempDir(), "pyro.db"))
	defer hist.Close()
	eng := engine.New(httpdriver.New(httpdriver.Options{}))
	ts := httptest.NewServer(New(Config{Workspace: ws, Engine: eng, History: hist, Token: testToken, Agents: agents, AgentToken: "tok"}).Handler())
	defer ts.Close()

	_, body := do(t, ts, "GET", "/api/v1/agents", "", true)
	if !strings.Contains(body, `"name":"a"`) || !strings.Contains(body, `"name":"b"`) {
		t.Fatalf("agents: %s", body)
	}
	do(t, ts, "PUT", "/api/v1/requests/demo/ping", `{"name":"Ping","url":"`+target.URL+`/ping"}`, true)
	plan := "name: Dist\nscenarios:\n  s: { executor: per-vu-iterations, vus: 4, iterations: 5, flow: [ { request: demo/ping } ] }\nthresholds:\n  http_reqs: [\"count==20\"]\n"
	do(t, ts, "PUT", "/api/v1/loadtests/dist", loadPlanJSON(plan), true)
	st := runLoad(t, ts, "dist", `{"distributed":true}`)
	if st["status"] != "passed" {
		t.Fatalf("distributed: %+v", st)
	}
	time.Sleep(50 * time.Millisecond)
	_, body = do(t, ts, "GET", "/api/v1/loadruns?test=dist", "", true)
	var runs []map[string]any
	json.Unmarshal([]byte(body), &runs)
	if len(runs) != 1 {
		t.Fatalf("runs: %s", body)
	}
	_, body = do(t, ts, "GET", "/api/v1/loadruns/"+runs[0]["id"].(string), "", true)
	if !strings.Contains(body, `"agents":[`) || !strings.Contains(body, `"state":"done"`) {
		t.Fatalf("saved run lacks agents: %s", body)
	}
}

func TestDistributedNeedsAgents(t *testing.T) {
	ts := newTestServer(t)
	do(t, ts, "PUT", "/api/v1/requests/demo/ping", `{"name":"Ping","url":"http://127.0.0.1:1/"}`, true)
	do(t, ts, "PUT", "/api/v1/loadtests/x", loadPlanJSON("scenarios:\n  s: { executor: constant-vus, vus: 1, duration: 1s, flow: [ { request: demo/ping } ] }\n"), true)
	res, body := do(t, ts, "POST", "/api/v1/loadtests/x/start", `{"distributed":true}`, true)
	if res.StatusCode != 400 || !strings.Contains(body, "no agents") {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
}

func loadPlanJSON(yaml string) string {
	b, _ := json.Marshal(map[string]string{"yaml": yaml})
	return string(b)
}

func TestLoadTestLifecycle(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer target.Close()
	ts := newTestServer(t)
	do(t, ts, "PUT", "/api/v1/requests/demo/ping", `{"name":"Ping","url":"`+target.URL+`/ping"}`, true)

	// Invalid plans are rejected with the reasons.
	res, body := do(t, ts, "PUT", "/api/v1/loadtests/bad", loadPlanJSON("scenarios:\n  s: { executor: constant-vus, flow: [ { request: demo/nope } ] }\n"), true)
	if res.StatusCode != 400 || !strings.Contains(body, "no request at") {
		t.Fatalf("bad plan: %d %s", res.StatusCode, body)
	}

	plan := "name: Quick\nscenarios:\n  s: { executor: constant-vus, vus: 3, duration: 1500ms, flow: [ { request: demo/ping }, { think: 20ms } ] }\nthresholds:\n  http_req_failed: [\"rate<0.01\"]\n"
	if res, body := do(t, ts, "PUT", "/api/v1/loadtests/quick", loadPlanJSON(plan), true); res.StatusCode != 200 || !strings.Contains(body, `"peakVUs":3`) {
		t.Fatalf("put: %d %s", res.StatusCode, body)
	}
	_, body = do(t, ts, "GET", "/api/v1/loadtests", "", true)
	if !strings.Contains(body, `"title":"Quick"`) {
		t.Fatalf("list: %s", body)
	}

	_, body = do(t, ts, "POST", "/api/v1/loadtests/quick/start", `{}`, true)
	var started struct{ ID string }
	json.Unmarshal([]byte(body), &started)
	if started.ID == "" {
		t.Fatalf("start: %s", body)
	}
	ev := subscribe(t, ts, "load:"+started.ID)
	if res, _ := do(t, ts, "POST", "/api/v1/loadtests/quick/start", `{}`, true); res.StatusCode != http.StatusConflict {
		t.Fatalf("second start: %d", res.StatusCode)
	}
	sawTick := false
	for {
		e := readEvent(t, ev)
		if e.Type == "tick" {
			sawTick = true
		}
		if e.Type == "finished" {
			if e.Data.(map[string]any)["status"] != "passed" {
				t.Fatalf("finished: %+v", e.Data)
			}
			break
		}
	}
	if !sawTick {
		t.Fatal("no live ticks")
	}
	time.Sleep(50 * time.Millisecond) // the run is saved just before "finished"

	_, body = do(t, ts, "GET", "/api/v1/loadruns?test=quick", "", true)
	if !strings.Contains(body, `"id":"`+started.ID+`"`) || !strings.Contains(body, `"status":"passed"`) {
		t.Fatalf("runs: %s", body)
	}
	_, body = do(t, ts, "GET", "/api/v1/loadruns/"+started.ID, "", true)
	if !strings.Contains(body, `"requests"`) || !strings.Contains(body, `"demo/ping"`) {
		t.Fatalf("run: %s", body)
	}
	res, body = do(t, ts, "GET", "/api/v1/loadruns/"+started.ID+"/report", "", true)
	if res.StatusCode != 200 || !strings.Contains(body, "<title>Quick") {
		t.Fatalf("report: %d", res.StatusCode)
	}
}

func TestLoadTestHostConfirmationAndStop(t *testing.T) {
	ts := newTestServer(t)
	do(t, ts, "PUT", "/api/v1/requests/demo/remote", `{"name":"Remote","url":"http://203.0.113.9:9/x"}`, true)
	plan := "scenarios:\n  s: { executor: constant-vus, vus: 1, duration: 1m, flow: [ { request: demo/remote } ] }\n"
	do(t, ts, "PUT", "/api/v1/loadtests/remote", loadPlanJSON(plan), true)

	res, body := do(t, ts, "POST", "/api/v1/loadtests/remote/start", `{}`, true)
	if res.StatusCode != http.StatusConflict || !strings.Contains(body, `"needsConfirmation":true`) || !strings.Contains(body, "203.0.113.9") {
		t.Fatalf("unconfirmed: %d %s", res.StatusCode, body)
	}
	_, body = do(t, ts, "POST", "/api/v1/loadtests/remote/start", `{"confirmHosts":true}`, true)
	var started struct{ ID string }
	json.Unmarshal([]byte(body), &started)
	if started.ID == "" {
		t.Fatalf("confirmed start: %s", body)
	}
	time.Sleep(200 * time.Millisecond)
	if res, _ := do(t, ts, "POST", "/api/v1/loadruns/"+started.ID+"/stop?abort=1", "", true); res.StatusCode != http.StatusNoContent {
		t.Fatalf("stop: %d", res.StatusCode)
	}
	for i := 0; i < 50; i++ {
		_, body = do(t, ts, "GET", "/api/v1/loadruns/"+started.ID, "", true)
		if strings.Contains(body, `"status":"stopped"`) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("not stopped: %s", body)
}
