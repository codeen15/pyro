package load

import (
	"strings"
	"testing"
	"time"

	"github.com/pyrohq/pyro/pkg/engine/metrics"
)

const samplePlan = `
name: Checkout stress
environment: staging
scenarios:
  browse:
    executor: ramping-vus
    stages:
      - { duration: 2m, target: 200 }
      - { duration: 30, target: 0 }
    flow:
      - request: shop/auth/login
        once: true
      - group: checkout
        steps:
          - request: shop/orders/create
          - think: { min: 1s, max: 3s }
      - weighted:
          - { weight: 80, steps: [ { request: shop/catalog/list } ] }
          - { weight: 20, steps: [ { think: 500ms } ] }
      - if:
          expr: status == 200 && cartSize > 0
          then: [ { request: shop/orders/create } ]
      - loop: { count: 3, steps: [ { think: 1s } ] }
    data: { file: data/users.csv, mode: per-vu }
  listeners:
    executor: constant-arrival-rate
    rate: 50
    duration: 1m
    flow:
      - request: shop/realtime/feed
        hold: 30s
thresholds:
  http_req_duration{scenario:browse}: ["p(95)<500", "p(99)<1200"]
  http_req_failed:
    - threshold: rate<0.01
      abortOnFail: true
      delayAbortEval: 10s
`

func TestParsePlan(t *testing.T) {
	p, err := ParsePlan([]byte(samplePlan), func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	b := p.Scenarios["browse"]
	if b.Stages[1].Duration.D() != 30*time.Second || b.GracefulRampDown.D() != 30*time.Second {
		t.Fatalf("browse = %+v", b)
	}
	if th := b.Flow[1].Steps[1].Think; th.Min.D() != time.Second || th.Max.D() != 3*time.Second {
		t.Fatalf("think = %+v", th)
	}
	if th := b.Flow[2].Weighted[1].Steps[0].Think; th.Min != th.Max || th.Min.D() != 500*time.Millisecond {
		t.Fatalf("scalar think = %+v", th)
	}
	l := p.Scenarios["listeners"]
	if l.TimeUnit.D() != time.Second || l.PreAllocatedVUs != 10 || l.MaxVUs != 10 || l.Flow[0].Hold.D() != 30*time.Second {
		t.Fatalf("listeners defaults = %+v", l)
	}
	if c := p.Thresholds["http_req_failed"][0]; !c.AbortOnFail || c.DelayAbortEval.D() != 10*time.Second {
		t.Fatalf("criterion = %+v", c)
	}
}

func TestPlanValidation(t *testing.T) {
	bad := `
scenarios:
  a:
    executor: constant-vus
    flow: [ { request: x/missing }, { think: 1s, request: x/y }, { loop: { steps: [] } } ]
  b:
    executor: warp-speed
    flow: [ { if: { expr: "status ==", then: [] } } ]
thresholds:
  "bad key{": ["p(95)<1"]
  http_reqs: ["p(150)<1", "wat"]
`
	_, err := ParsePlan([]byte(bad), func(p string) bool { return p != "x/missing" })
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{
		"constant-vus needs vus and duration", `no request at "x/missing"`, "needs exactly one of",
		"loop needs count or while", `unknown executor "warp-speed"`, "invalid expression",
		"expected metric or metric{tag:value}", "percentile", `invalid condition "wat"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}
	if _, err := ParsePlan([]byte("scenarios: {}\nbogus: 1"), nil); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Errorf("unknown field: %v", err)
	}
}

func TestThresholds(t *testing.T) {
	agg := metrics.NewAggregator()
	b := agg.NewBuffer()
	for i := 1; i <= 100; i++ {
		b.Add(metrics.Sample{Metric: "http_req_duration", Tags: metrics.Tags{Scenario: "a"}, Value: float64(i * 10)})
		b.Add(metrics.Sample{Metric: "http_req_failed", Value: 0})
	}
	b.Add(metrics.Sample{Metric: "http_req_failed", Value: 1})
	agg.Flush()

	p := &Plan{Thresholds: map[string]Criteria{
		"http_req_duration{scenario:a}": {{Threshold: "p(95)<900"}, {Threshold: "avg<600"}},
		"http_req_failed":               {{Threshold: "rate<0.005", AbortOnFail: true, DelayAbortEval: Duration(time.Minute)}},
		"ws_msgs_received":              {{Threshold: "count>0"}},
		"sio_event_rtt":                 {{Threshold: "p(95)<100"}},
	}}
	ts := compileThresholds(p)
	res, abort := ts.evaluate(agg, 30*time.Second)
	byCond := map[string]ThresholdResult{}
	for _, r := range res {
		byCond[r.Condition] = r
	}
	if r := byCond["p(95)<900"]; r.Passed || r.Actual < 940 {
		t.Errorf("p95 = %+v", r)
	}
	if r := byCond["avg<600"]; !r.Passed {
		t.Errorf("avg = %+v", r)
	}
	if r := byCond["rate<0.005"]; r.Passed || !r.Breached {
		t.Errorf("rate = %+v", r)
	}
	if r := byCond["count>0"]; r.NoData || r.Passed || r.Actual != 0 {
		t.Errorf("a counter with no samples is zero, so count>0 fails: %+v", r)
	}
	if r := byCond["p(95)<100"]; !r.NoData || !r.Passed {
		t.Errorf("a trend with no samples has no data: %+v", r)
	}
	if abort {
		t.Error("must not abort before delayAbortEval")
	}
	if _, abort = ts.evaluate(agg, 2*time.Minute); !abort {
		t.Error("expected abort after the delay")
	}
}
