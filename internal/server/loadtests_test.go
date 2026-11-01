package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
