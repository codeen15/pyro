package report

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/codeen15/pyro/pkg/engine"
	"github.com/codeen15/pyro/pkg/engine/check"
	"github.com/codeen15/pyro/pkg/engine/load"
	"github.com/codeen15/pyro/pkg/engine/metrics"
	"github.com/codeen15/pyro/pkg/engine/protocol"
	"github.com/codeen15/pyro/pkg/engine/runner"
)

func TestJUnit(t *testing.T) {
	sum := &runner.Summary{Iterations: 1, Duration: 2 * time.Second, Results: []runner.ItemResult{
		{Path: "c/login", Name: "Login", Result: &engine.Result{
			Response: &protocol.Response{Status: 200, Timings: protocol.Timings{Total: 120 * time.Millisecond}},
			Tests: []check.Result{
				{Name: "status ok", Passed: true},
				{Name: "has <token>", Passed: false, Error: `expected "a" & "b"`},
			},
		}},
		{Path: "c/down", Name: "Down", Result: &engine.Result{Error: "could not connect"}},
		{Path: "c/plain", Name: "Plain", Result: &engine.Result{Response: &protocol.Response{Status: 200}}},
		{Path: "c/ws", Name: "WS", Note: "skipped"},
	}}
	b, err := JUnit("demo", sum)
	if err != nil {
		t.Fatal(err)
	}
	var parsed junitSuites
	if err := xml.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("invalid XML: %v\n%s", err, b)
	}
	if parsed.Tests != 4 || parsed.Failures != 1 || parsed.Errors != 1 || len(parsed.Suites) != 4 {
		t.Fatalf("counts = %+v", parsed)
	}
	if parsed.Suites[0].Time != "0.120" || parsed.Suites[0].Cases[1].Failure.Message != `expected "a" & "b"` {
		t.Fatalf("suite 0 = %+v", parsed.Suites[0])
	}
	if !strings.HasPrefix(string(b), "<?xml") || parsed.Suites[3].Skipped != 1 {
		t.Fatalf("header/skip:\n%s", b)
	}
}

func TestLoadHTML(t *testing.T) {
	res := &load.Result{
		Name: "Checkout <stress>", Status: load.StatusFailed, Reason: "one or more thresholds failed", Duration: 125,
		Ticks: []load.Tick{
			{Elapsed: 1, VUs: 5, RPS: 40, Latency: load.Latency{P50: 20, P95: 90, P99: 140}},
			{Elapsed: 2, VUs: 10, RPS: 80, ErrorRate: 0.02, Latency: load.Latency{P50: 25, P95: 420, P99: 800}},
		},
		Metrics: map[string]metrics.Stat{"http_reqs": {Value: 120, PerSecond: 60}, "http_req_duration": {P95: 420}},
		Requests: []metrics.RequestStat{{Scenario: "s", Request: "shop/<list>", Method: "GET", Count: 120,
			Duration: metrics.Stat{Avg: 50, Med: 25, P95: 420, P99: 800, Max: 900}}},
		Thresholds: []load.ThresholdResult{
			{Metric: "http_req_duration", Condition: "p(95)<300", Actual: 420, Passed: false, Breached: true},
			{Metric: "http_req_failed", Condition: "rate<0.05", Actual: 0.02, Passed: true, Breached: true},
		},
		Errors: []load.ErrorCount{{Message: "HTTP 500 from shop/list", Count: 3}},
	}
	b, err := LoadHTML(res)
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	for _, want := range []string{
		"Checkout &lt;stress&gt;", "✗ Failed", "shop/&lt;list&gt;", `class="sw cherry"`, "p(95)&lt;300", `class="ref"`,
		"HTTP 500 from shop/list", `data-chart="latency"`, "#5b9ddf", "(failed earlier in the run)",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("report missing %q", want)
		}
	}
	if strings.Contains(html, "<list>") {
		t.Error("request names must be escaped")
	}
	if v, ok := percentileLimit("p(99) <= 1200"); !ok || v != 1200 {
		t.Errorf("limit = %v %v", v, ok)
	}
}
