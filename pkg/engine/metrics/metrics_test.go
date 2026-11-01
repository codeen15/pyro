package metrics

import (
	"math"
	"sync"
	"testing"
	"time"
)

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestTrendPercentilesAndQuery(t *testing.T) {
	a := NewAggregator()
	b := a.NewBuffer()
	login := Tags{Scenario: "s", Request: "login", Method: "POST", Status: "200"}
	list := Tags{Scenario: "s", Request: "list", Method: "GET", Status: "200"}
	for i := 1; i <= 1000; i++ {
		b.Add(Sample{"http_req_duration", login, float64(i)}) // 1..1000 ms
		b.Add(Sample{"http_req_duration", list, 5})
		b.Add(Sample{"http_req_failed", login, boolf(i%10 == 0)})
		b.Add(Sample{"http_reqs", login, 1})
	}
	a.Flush()

	st := a.Query("http_req_duration", map[string]string{"request": "login"})
	if st.Count != 1000 || !near(st.Avg, 500.5, 0.01) || !near(st.P95, 950, 1) || !near(st.P99, 990, 1) || !near(st.Max, 1000, 1) {
		t.Fatalf("login stat = %+v", st)
	}
	if p := st.Percentile(99.9); !near(p, 999, 1) {
		t.Fatalf("p99.9 = %v", p)
	}
	all := a.Query("http_req_duration", nil)
	if all.Count != 2000 || !near(all.Med, 5, 0.01) {
		t.Fatalf("all = %+v", all)
	}
	if f := a.Query("http_req_failed", nil); !near(f.Value, 0.1, 1e-9) {
		t.Fatalf("failed rate = %v", f.Value)
	}
	if n := a.Query("http_req_duration", map[string]string{"request": "nope"}); n.Count != 0 {
		t.Fatalf("no match = %+v", n)
	}

	rows := a.Requests()
	if len(rows) != 2 || rows[0].Request != "list" || rows[1].Failed != 100 || rows[1].Method != "POST" {
		t.Fatalf("rows = %+v", rows)
	}
}

func boolf(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func TestIntervalsResetButGaugesCarry(t *testing.T) {
	a := NewAggregator()
	b := a.NewBuffer()
	a.Record(Sample{Metric: "vus", Value: 10})
	b.Add(Sample{Metric: "http_reqs", Value: 1})
	b.Add(Sample{Metric: "http_req_duration", Value: 100})
	time.Sleep(20 * time.Millisecond)
	iv := a.Flush()
	if iv.Metric("http_reqs").Value != 1 || iv.Metric("http_reqs").PerSecond <= 0 || iv.Metric("vus").Value != 10 {
		t.Fatalf("first = %+v %+v", iv.Metric("http_reqs"), iv.Metric("vus"))
	}
	iv = a.Flush()
	if iv.Metric("http_reqs").Count != 0 || iv.Metric("http_req_duration").Count != 0 || iv.Metric("vus").Value != 10 {
		t.Fatalf("second = %+v", iv.metrics)
	}
	if a.Query("http_reqs", nil).Value != 1 {
		t.Fatal("cumulative lost")
	}
}

func TestConcurrentBuffers(t *testing.T) {
	a := NewAggregator()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				a.Flush()
			}
		}
	}()
	for v := 0; v < 50; v++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := a.NewBuffer()
			for i := 0; i < 2000; i++ {
				b.Add(Sample{Metric: "http_reqs", Value: 1})
			}
		}()
	}
	wg.Wait()
	close(stop)
	a.Flush()
	if got := a.Query("http_reqs", nil).Value; got != 100000 {
		t.Fatalf("lost samples: %v", got)
	}
}

func BenchmarkBufferAdd(b *testing.B) {
	a := NewAggregator()
	buf := a.NewBuffer()
	s := Sample{Metric: "http_req_duration", Tags: Tags{Request: "x"}, Value: 12.5}
	for i := 0; i < b.N; i++ {
		buf.Add(s)
		if i%100000 == 0 {
			a.Flush()
		}
	}
}
