package load

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/pyrohq/pyro/pkg/engine/model"
)

type target struct {
	*httptest.Server
	hits atomic.Int64
}

// newTarget answers /ok (fast), /slow (delay), /fail (500), /login (token),
// /me (echoes the token header) and /ws (echo WebSocket).
func newTarget(t *testing.T, delay time.Duration) *target {
	tg := &target{}
	tg.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tg.hits.Add(1)
		switch r.URL.Path {
		case "/slow":
			time.Sleep(delay)
		case "/fail":
			w.WriteHeader(500)
			return
		case "/login":
			fmt.Fprintf(w, `{"token":"t-%s"}`, r.URL.Query().Get("user"))
			return
		case "/me":
			w.Write([]byte(r.Header.Get("Authorization")))
			return
		case "/ws":
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			for {
				typ, data, err := c.Read(context.Background())
				if err != nil {
					return
				}
				c.Write(context.Background(), typ, data)
			}
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(tg.Close)
	return tg
}

func resolver(base string) func(string) (Target, error) {
	chain := []*model.Collection{{Variables: []model.Variable{{Key: "base", Value: base}}}}
	reqs := map[string]*model.Request{
		"c/ok":    {Kind: model.KindHTTP, Name: "OK", URL: "{{base}}/ok"},
		"c/slow":  {Kind: model.KindHTTP, Name: "Slow", URL: "{{base}}/slow"},
		"c/fail":  {Kind: model.KindHTTP, Name: "Fail", URL: "{{base}}/fail"},
		"c/login": {Kind: model.KindHTTP, Name: "Login", Method: "POST", URL: "{{base}}/login?user={{user}}", Extract: []model.Extractor{{Var: "token", From: "jsonpath", Path: "$.token"}}},
		"c/me":    {Kind: model.KindHTTP, Name: "Me", URL: "{{base}}/me", Headers: []model.KeyValue{{Key: "Authorization", Value: "{{token}}"}}, Assertions: []model.Assertion{{Type: "body", Op: "contains", Value: "t-"}}},
		"c/ws":    {Kind: model.KindWebSocket, Name: "WS", URL: strings.Replace(base, "http", "ws", 1) + "/ws"},
	}
	return func(p string) (Target, error) {
		r, ok := reqs[p]
		if !ok {
			return Target{}, fmt.Errorf("not found")
		}
		return Target{Path: p, Request: r, Chain: chain}, nil
	}
}

func run(t *testing.T, base, planYAML string, opts ...func(*Options)) *Result {
	t.Helper()
	p, err := ParsePlan([]byte(planYAML), nil)
	if err != nil {
		t.Fatal(err)
	}
	o := Options{Resolve: resolver(base), TickInterval: 100 * time.Millisecond}
	for _, f := range opts {
		f(&o)
	}
	test, err := Start(p, o)
	if err != nil {
		t.Fatal(err)
	}
	return test.Wait()
}

func within(got, want, tol float64) bool { return math.Abs(got-want) <= tol }

func TestConstantVUs(t *testing.T) {
	tg := newTarget(t, 0)
	res := run(t, tg.URL, `
name: smoke
scenarios:
  s:
    executor: constant-vus
    vus: 5
    duration: 600ms
    flow: [ { request: c/ok }, { think: 50ms } ]
thresholds:
  http_req_duration: ["p(95)<200"]
  http_req_failed: ["rate<0.01"]
`)
	reqs := res.Metrics["http_reqs"].Value
	if res.Status != StatusPassed || res.MaxVUs != 5 || reqs < 30 || reqs > 70 {
		t.Fatalf("status=%s maxVUs=%d reqs=%v reason=%s", res.Status, res.MaxVUs, reqs, res.Reason)
	}
	if len(res.Requests) != 1 || res.Requests[0].Request != "c/ok" || res.Requests[0].Method != "GET" {
		t.Fatalf("requests = %+v", res.Requests)
	}
	if len(res.Ticks) < 5 || res.Ticks[2].VUs != 5 {
		t.Fatalf("ticks = %d, vus at tick 2 = %d", len(res.Ticks), res.Ticks[2].VUs)
	}
}

func TestRampingVUs(t *testing.T) {
	tg := newTarget(t, 0)
	res := run(t, tg.URL, `
scenarios:
  ramp:
    executor: ramping-vus
    stages: [ { duration: 500ms, target: 8 }, { duration: 300ms, target: 8 }, { duration: 300ms, target: 0 } ]
    gracefulRampDown: 1s
    flow: [ { request: c/ok }, { think: 20ms } ]
`)
	peak := 0
	for _, tk := range res.Ticks {
		peak = max(peak, tk.VUs)
	}
	if res.MaxVUs != 8 || peak < 7 || res.Ticks[len(res.Ticks)-1].VUs != 0 {
		t.Fatalf("maxVUs=%d peak=%d last=%d", res.MaxVUs, peak, res.Ticks[len(res.Ticks)-1].VUs)
	}
}

func TestPerVUIterationsExact(t *testing.T) {
	tg := newTarget(t, 0)
	res := run(t, tg.URL, `
scenarios:
  it:
    executor: per-vu-iterations
    vus: 3
    iterations: 4
    flow: [ { request: c/ok } ]
`)
	if got := res.Metrics["http_reqs"].Value; got != 12 || res.Metrics["iterations"].Value != 12 {
		t.Fatalf("reqs=%v iterations=%v", got, res.Metrics["iterations"].Value)
	}
}

func TestConstantArrivalRate(t *testing.T) {
	tg := newTarget(t, 0)
	res := run(t, tg.URL, `
scenarios:
  open:
    executor: constant-arrival-rate
    rate: 100
    duration: 1s
    preAllocatedVUs: 5
    flow: [ { request: c/ok } ]
`)
	if got := res.Metrics["http_reqs"].Value; !within(got, 100, 8) {
		t.Fatalf("arrival rate produced %v requests, want ~100", got)
	}
}

func TestArrivalRateDropsWhenSaturated(t *testing.T) {
	tg := newTarget(t, 400*time.Millisecond)
	res := run(t, tg.URL, `
scenarios:
  open:
    executor: constant-arrival-rate
    rate: 20
    duration: 800ms
    preAllocatedVUs: 2
    maxVUs: 3
    flow: [ { request: c/slow } ]
`)
	if res.Metrics["dropped_iterations"].Value < 5 || res.MaxVUs != 3 {
		t.Fatalf("dropped=%v maxVUs=%d", res.Metrics["dropped_iterations"].Value, res.MaxVUs)
	}
}

func TestAbortOnFail(t *testing.T) {
	tg := newTarget(t, 0)
	start := time.Now()
	res := run(t, tg.URL, `
scenarios:
  s:
    executor: constant-vus
    vus: 2
    duration: 10s
    flow: [ { request: c/fail }, { think: 20ms } ]
thresholds:
  http_req_failed: [ { threshold: "rate<0.1", abortOnFail: true } ]
`)
	if res.Status != StatusAborted || time.Since(start) > 3*time.Second {
		t.Fatalf("status=%s after %v", res.Status, time.Since(start))
	}
	if len(res.Errors) == 0 || !strings.Contains(res.Errors[0].Message, "HTTP 500 from c/fail") {
		t.Fatalf("errors = %+v", res.Errors)
	}
}

func TestStopIsGraceful(t *testing.T) {
	tg := newTarget(t, 0)
	p, _ := ParsePlan([]byte(`
scenarios:
  s:
    executor: constant-vus
    vus: 3
    duration: 1m
    flow: [ { request: c/ok }, { think: 20ms } ]
`), nil)
	test, err := Start(p, Options{Resolve: resolver(tg.URL), TickInterval: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	test.Stop()
	select {
	case <-test.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not end the test")
	}
	if res := test.Wait(); res.Status != StatusStopped || res.Metrics["http_reqs"].Value == 0 {
		t.Fatalf("result = %s %v", res.Status, res.Metrics["http_reqs"].Value)
	}
}

func TestFlowFeatures(t *testing.T) {
	tg := newTarget(t, 0)
	res := run(t, tg.URL, `
scenarios:
  flow:
    executor: per-vu-iterations
    vus: 2
    iterations: 3
    data: { file: users.csv, mode: per-vu }
    flow:
      - request: c/login
        once: true
      - group: account
        steps:
          - request: c/me
      - if: { expr: "status == 200 && iteration >= 1", then: [ { request: c/ok, name: second-and-later } ] }
      - loop: { count: 2, steps: [ { request: c/ok, name: looped } ] }
      - weighted:
          - { weight: 1, steps: [ { request: c/ok, name: branch } ] }
`, func(o *Options) {
		o.LoadData = func(string) ([]map[string]string, error) {
			return []map[string]string{{"user": "ann"}, {"user": "bob"}}, nil
		}
	})
	counts := map[string]int64{}
	for _, r := range res.Requests {
		counts[r.Request] = r.Count
	}
	want := map[string]int64{"c/login": 2, "c/me": 6, "second-and-later": 4, "looped": 12, "branch": 6}
	for k, v := range want {
		if counts[k] != v {
			t.Errorf("%s ran %d times, want %d (all: %v)", k, counts[k], v, counts)
		}
	}
	if checks := res.Metrics["checks"]; checks.Value != 1 || checks.Count != 6 {
		t.Errorf("checks = %+v (the extracted token must reach /me)", checks)
	}
	if res.Metrics["group_duration"].Count != 6 {
		t.Errorf("group_duration count = %d", res.Metrics["group_duration"].Count)
	}
}

func TestStreamHold(t *testing.T) {
	tg := newTarget(t, 0)
	res := run(t, tg.URL, `
scenarios:
  ws:
    executor: per-vu-iterations
    vus: 3
    iterations: 1
    flow:
      - request: c/ws
        hold: 300ms
        send: [ { content: "hello" }, { content: "again" } ]
`)
	m := res.Metrics
	if m["ws_sessions"].Value != 3 || m["ws_msgs_sent"].Value != 6 || m["ws_msgs_received"].Value != 6 || m["ws_session_duration"].Min < 290 {
		t.Fatalf("ws metrics: sessions=%v sent=%v recv=%v dur=%+v", m["ws_sessions"].Value, m["ws_msgs_sent"].Value, m["ws_msgs_received"].Value, m["ws_session_duration"])
	}
}

func TestLimitsAndHosts(t *testing.T) {
	p, _ := ParsePlan([]byte(`
scenarios:
  big: { executor: constant-vus, vus: 600, duration: 1s, flow: [ { request: c/ok } ] }
`), nil)
	if _, err := Start(p, Options{Resolve: resolver("http://x"), MaxVUs: 500}); err == nil || !strings.Contains(err.Error(), "limit is 500") {
		t.Fatalf("cap err = %v", err)
	}
	hosts := Hosts(p, resolver("https://api.example.com:8443"), nil)
	if len(hosts) != 1 || hosts[0] != "api.example.com" {
		t.Fatalf("hosts = %v", hosts)
	}
}

// A threshold that fails early and recovers must not fail the test.
func TestVerdictUsesFinalValues(t *testing.T) {
	tg := newTarget(t, 0)
	res := run(t, tg.URL, `
scenarios:
  s:
    executor: constant-vus
    vus: 2
    duration: 500ms
    flow: [ { request: c/ok }, { think: 20ms } ]
thresholds:
  http_reqs: ["count>20"]
`)
	th := res.Thresholds[0]
	if res.Status != StatusPassed || !th.Passed || !th.Breached {
		t.Fatalf("status=%s threshold=%+v", res.Status, th)
	}
}
