package metrics

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"
)

// Merging deltas from several aggregators must give the same stats as
// recording every sample into one.
func TestDeltaMergeMatchesDirect(t *testing.T) {
	direct := NewAggregator()
	ctrl := NewAggregator()
	agents := []*Aggregator{NewAggregator(), NewAggregator(), NewAggregator()}
	for _, a := range agents {
		a.TrackDeltas()
	}
	rnd := rand.New(rand.NewPCG(1, 2))
	tags := []Tags{{Scenario: "s", Request: "a", Method: "GET", Status: "200"}, {Scenario: "s", Request: "b", Method: "POST", Status: "500"}}
	for round := 0; round < 3; round++ {
		for i := 0; i < 20000; i++ {
			a := agents[rnd.IntN(len(agents))]
			tg := tags[rnd.IntN(len(tags))]
			v := rnd.ExpFloat64() * 40 // ms, long tail
			fail := 0.0
			if tg.Status == "500" {
				fail = 1
			}
			for _, s := range []Sample{
				{Metric: "http_req_duration", Tags: tg, Value: v},
				{Metric: "http_reqs", Tags: tg, Value: 1},
				{Metric: "http_req_failed", Tags: tg, Value: fail},
				{Metric: "vus", Value: 7},
			} {
				a.Record(s)
				direct.Record(s)
			}
		}
		for _, a := range agents {
			a.Flush()
			ctrl.Merge(a.TakeDelta())
		}
	}
	if len(agents[0].TakeDelta()) != 0 {
		t.Fatal("deltas not reset after TakeDelta")
	}
	for _, m := range []string{"http_req_duration", "http_reqs", "http_req_failed"} {
		for _, f := range []map[string]string{nil, {"request": "a"}, {"status": "500"}} {
			want, got := direct.Query(m, f), ctrl.Query(m, f)
			if want.Count != got.Count || math.Abs(want.Value-got.Value) > 1e-9 || want.Max != got.Max || want.Min != got.Min ||
				want.P95 != got.P95 || want.P99 != got.P99 || want.Med != got.Med {
				t.Errorf("%s %v:\n got %+v\nwant %+v", m, f, got, want)
			}
		}
	}
	if !direct.Query("http_req_duration", nil).hist.Equals(ctrl.Query("http_req_duration", nil).hist) {
		t.Error("merged histogram differs from the direct one")
	}
	if got := ctrl.Query("vus", nil); got.Count != 0 {
		t.Errorf("gauges must not be merged, got %+v", got)
	}
	// The interval view sees merged samples too.
	ctrl.Flush()
	ctrl.Merge([]SeriesDelta{{Metric: "http_reqs", Count: 5, Sum: 5}})
	if iv := ctrl.Flush(); iv.Metric("http_reqs").Value != 5 {
		t.Errorf("interval http_reqs = %v, want 5", iv.Metric("http_reqs").Value)
	}
}

func TestMinWindowFloorsRates(t *testing.T) {
	a := NewAggregator()
	a.MinWindow = time.Second
	a.Merge([]SeriesDelta{{Metric: "http_reqs", Count: 600, Sum: 600}})
	if ps := a.Flush().Metric("http_reqs").PerSecond; ps > 600 {
		t.Errorf("a short interval reported %.0f/s for 600 samples", ps)
	}
}
