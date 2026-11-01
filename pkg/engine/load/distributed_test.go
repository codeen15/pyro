package load

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// memWorker runs its segment in-process, with the job JSON round-tripped
// like it would be over the wire.
type memWorker struct {
	name    string
	fail    error // returned by Run after the job starts
	mu      sync.Mutex
	job     Job
	test    *Test
	report  func(Report)
	ticks   chan Report
	stopped bool
}

func (w *memWorker) Name() string { return w.name }

func (w *memWorker) Prepare(ctx context.Context, job Job) error {
	b, err := json.Marshal(job)
	if err != nil {
		return err
	}
	var j Job
	if err := json.Unmarshal(b, &j); err != nil {
		return err
	}
	w.ticks = make(chan Report, 1000)
	t, err := PrepareJob(j, Options{TickInterval: 50 * time.Millisecond}, func(r Report) { w.ticks <- r })
	if err != nil {
		return err
	}
	w.job, w.test = j, t
	return nil
}

func (w *memWorker) Run(ctx context.Context, report func(Report)) error {
	w.test.Begin()
	go func() {
		<-ctx.Done()
		w.test.Abort()
	}()
	if w.fail != nil {
		time.Sleep(100 * time.Millisecond)
		w.test.Abort()
		w.test.Wait()
		return w.fail
	}
	for {
		select {
		case r := <-w.ticks:
			report(r)
		case <-w.test.Done():
			for {
				select {
				case r := <-w.ticks:
					report(r)
				default:
					return nil
				}
			}
		}
	}
}

func (w *memWorker) Stop() {
	w.mu.Lock()
	w.stopped = true
	w.mu.Unlock()
	if w.test != nil {
		w.test.Stop()
	}
}

func (w *memWorker) Close() {
	if w.test != nil {
		w.test.Close()
	}
}

func TestDistributedMergesWorkers(t *testing.T) {
	tg := newTarget(t, 0)
	workers := []*memWorker{{name: "a"}, {name: "b"}, {name: "c"}}
	res := run(t, tg.URL, `
name: dist
scenarios:
  s:
    executor: per-vu-iterations
    vus: 7
    iterations: 20
    flow: [ { request: c/ok }, { request: c/fail } ]
thresholds:
  http_req_failed: ["rate<0.6"]
  http_reqs: ["count==280"]
`, func(o *Options) {
		for _, w := range workers {
			o.Workers = append(o.Workers, w)
		}
	})
	if res.Status != StatusPassed {
		t.Fatalf("status %s (%s): %+v", res.Status, res.Reason, res.Thresholds)
	}
	if got := res.Metrics["http_reqs"].Value; got != 280 || tg.hits.Load() != 280 {
		t.Errorf("requests: merged %v, target saw %d; want 280", got, tg.hits.Load())
	}
	if got := res.Metrics["http_req_failed"].Value; got != 0.5 {
		t.Errorf("failure rate %v, want 0.5", got)
	}
	if res.MaxVUs != 7 || len(res.Agents) != 3 {
		t.Errorf("maxVUs %d, agents %+v", res.MaxVUs, res.Agents)
	}
	var sum int64
	firstVUs := map[int]bool{}
	for i, a := range res.Agents {
		sum += a.Requests
		if a.State != "done" || a.Name != workers[i].name {
			t.Errorf("agent %+v", a)
		}
		firstVUs[workers[i].job.FirstVU] = true
	}
	if sum != 280 || len(firstVUs) != 3 {
		t.Errorf("agent requests sum %d, distinct first VUs %v", sum, firstVUs)
	}
	if len(res.Requests) != 2 || res.Requests[0].Count != 140 {
		t.Errorf("per-request rows: %+v", res.Requests)
	}
	if len(res.Errors) != 1 || res.Errors[0].Count != 140 {
		t.Errorf("errors merged from agents: %+v", res.Errors)
	}
}

func TestDistributedSplitsSharedData(t *testing.T) {
	tg := newTarget(t, 0)
	rows := []map[string]string{{"user": "1"}, {"user": "2"}, {"user": "3"}, {"user": "4"}}
	workers := []*memWorker{{name: "a"}, {name: "b"}}
	run(t, tg.URL, `
name: data
scenarios:
  s:
    executor: per-vu-iterations
    vus: 2
    iterations: 1
    data: { file: users.csv }
    flow: [ { request: c/login } ]
`, func(o *Options) {
		o.LoadData = func(string) ([]map[string]string, error) { return rows, nil }
		for _, w := range workers {
			o.Workers = append(o.Workers, w)
		}
	})
	a, b := workers[0].job.Rows["s"], workers[1].job.Rows["s"]
	if len(a) != 2 || len(b) != 2 || a[0]["user"] != "1" || b[0]["user"] != "2" {
		t.Errorf("rows split: %v / %v", a, b)
	}
}

func TestDistributedWorkerFailureFailsTest(t *testing.T) {
	tg := newTarget(t, 0)
	workers := []*memWorker{{name: "a"}, {name: "b", fail: errors.New("connection lost")}}
	res := run(t, tg.URL, `
name: fail
scenarios:
  s:
    executor: constant-vus
    vus: 2
    duration: 10s
    flow: [ { request: c/ok }, { think: 20ms } ]
`, func(o *Options) {
		for _, w := range workers {
			o.Workers = append(o.Workers, w)
		}
	})
	if res.Status != StatusFailed || res.Reason != "agent b failed: connection lost" {
		t.Errorf("status %s: %s", res.Status, res.Reason)
	}
	if !workers[0].stopped || res.Duration > 5 {
		t.Errorf("the healthy worker should be stopped early (duration %.1fs)", res.Duration)
	}
	if res.Agents[1].State != "failed" || res.Agents[0].State != "done" {
		t.Errorf("agents: %+v", res.Agents)
	}
}

func TestDistributedPrepareError(t *testing.T) {
	tg := newTarget(t, 0)
	p, _ := ParsePlan([]byte(`
name: x
scenarios:
  s: { executor: constant-vus, vus: 2, duration: 1s, flow: [ { request: c/ok } ] }
`), nil)
	bad := &badWorker{}
	_, err := Start(p, Options{Resolve: resolver(tg.URL), Workers: []Worker{&memWorker{name: "a"}, bad}})
	if err == nil || err.Error() != "agent bad: busy" {
		t.Fatalf("err = %v", err)
	}
}

type badWorker struct{ memWorker }

func (*badWorker) Name() string                            { return "bad" }
func (*badWorker) Prepare(context.Context, Job) error      { return errors.New("busy") }
func (*badWorker) Run(context.Context, func(Report)) error { return nil }
