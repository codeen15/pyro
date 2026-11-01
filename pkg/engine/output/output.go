// Package output streams live load-test metrics to external systems:
// InfluxDB (line protocol), Prometheus (remote write), and per-tick JSON or
// CSV files. Outputs are given as kind=target, like k6's --out:
//
//	influxdb=http://localhost:8086/write?db=pyro          (InfluxDB 1.x)
//	influxdb=http://localhost:8086/api/v2/write?org=o&bucket=b  (2.x; token in $PYRO_INFLUXDB_TOKEN)
//	prometheus=http://localhost:9090/api/v1/write          (remote write; needs --web.enable-remote-write-receiver)
//	json=ticks.ndjson
//	csv=ticks.csv
//
// Writes happen off the tick goroutine; a slow or unreachable backend never
// slows the test (points are dropped and counted instead).
package output

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pyrohq/pyro/pkg/engine/load"
	"github.com/pyrohq/pyro/pkg/engine/metrics"
)

// Point is one metric value at one time.
type Point struct {
	Name   string
	Labels map[string]string
	Value  float64
	Time   time.Time
}

// Batch is everything one tick produces.
type Batch struct {
	Time     time.Time
	Tick     load.Tick
	Requests []metrics.RequestStat
	Points   []Point
}

type backend interface {
	write(ctx context.Context, b Batch) error
	close() error
}

// Output is one configured destination.
type Output struct {
	spec    string
	be      backend
	test    string
	run     string
	queue   chan Batch
	done    chan struct{}
	mu      sync.Mutex
	total   float64 // cumulative requests, for *_total counters
	dropped int
	errs    int
	lastErr error
}

// Open parses kind=target and connects (files are created here).
// test and run label every point.
func Open(spec, test, run string) (*Output, error) {
	kind, target, ok := strings.Cut(spec, "=")
	if !ok || target == "" {
		return nil, fmt.Errorf("output %q: use kind=target, e.g. influxdb=http://localhost:8086/write?db=pyro", spec)
	}
	var (
		be  backend
		err error
	)
	switch kind {
	case "influxdb":
		be, err = newInflux(target)
	case "prometheus":
		be, err = newPrometheus(target)
	case "json":
		be, err = newJSONFile(target)
	case "csv":
		be, err = newCSVFile(target)
	default:
		return nil, fmt.Errorf("output %q: unknown kind %q (influxdb, prometheus, json, csv)", spec, kind)
	}
	if err != nil {
		return nil, fmt.Errorf("output %s: %w", kind, err)
	}
	o := &Output{spec: kind, be: be, test: test, run: run, queue: make(chan Batch, 64), done: make(chan struct{})}
	go o.loop()
	return o, nil
}

func (o *Output) loop() {
	defer close(o.done)
	for b := range o.queue {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := o.be.write(ctx, b)
		cancel()
		if err != nil {
			o.mu.Lock()
			o.errs++
			o.lastErr = err
			o.mu.Unlock()
		}
	}
}

// Tick queues one tick's metrics. It never blocks.
func (o *Output) Tick(t load.Tick, reqs []metrics.RequestStat) {
	o.mu.Lock()
	o.total += float64(t.Requests)
	b := Batch{Time: time.Now(), Tick: t, Requests: reqs}
	b.Points = o.points(b)
	o.mu.Unlock()
	select {
	case o.queue <- b:
	default:
		o.mu.Lock()
		o.dropped++
		o.mu.Unlock()
	}
}

// Close flushes queued ticks and reports write problems.
func (o *Output) Close() error {
	close(o.queue)
	select {
	case <-o.done:
	case <-time.After(15 * time.Second):
		return fmt.Errorf("output %s: timed out flushing", o.spec)
	}
	err := o.be.close()
	o.mu.Lock()
	defer o.mu.Unlock()
	var problems []error
	if o.errs > 0 {
		problems = append(problems, fmt.Errorf("output %s: %d of the writes failed, last: %v", o.spec, o.errs, o.lastErr))
	}
	if o.dropped > 0 {
		problems = append(problems, fmt.Errorf("output %s: %d ticks dropped (the backend was too slow)", o.spec, o.dropped))
	}
	if err != nil {
		problems = append(problems, err)
	}
	return errors.Join(problems...)
}

// points converts a tick into named values. Durations are in seconds
// (Prometheus convention); quantile 1 is the maximum.
func (o *Output) points(b Batch) []Point {
	t := b.Tick
	base := map[string]string{"test": o.test, "run": o.run}
	with := func(kv ...string) map[string]string {
		m := make(map[string]string, len(base)+len(kv)/2)
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	var out []Point
	add := func(name string, v float64, labels map[string]string) {
		out = append(out, Point{Name: name, Labels: labels, Value: v, Time: b.Time})
	}
	add("pyro_vus", float64(t.VUs), base)
	add("pyro_vus_max", float64(t.VUsMax), base)
	add("pyro_http_reqs_total", o.total, base)
	add("pyro_http_reqs_per_second", t.RPS, base)
	add("pyro_http_req_failed_ratio", t.ErrorRate, base)
	add("pyro_iterations_per_second", t.IterationsPS, base)
	add("pyro_dropped_iterations", t.Dropped, base)
	add("pyro_data_received_bytes_per_second", t.ReceivedPS, base)
	if t.WSMessagesPS > 0 {
		add("pyro_ws_msgs_received_per_second", t.WSMessagesPS, base)
	}
	if t.Requests > 0 {
		for q, ms := range map[string]float64{"0.5": t.Latency.P50, "0.9": t.Latency.P90, "0.95": t.Latency.P95, "0.99": t.Latency.P99, "1": t.Latency.Max} {
			add("pyro_http_req_duration_seconds", ms/1000, with("quantile", q))
		}
		add("pyro_http_req_duration_seconds_avg", t.Latency.Avg/1000, base)
	}
	for _, th := range t.Thresholds {
		v := 0.0
		if th.Passed {
			v = 1
		}
		add("pyro_threshold_passing", v, with("threshold", th.Metric+" "+th.Condition))
	}
	for _, r := range b.Requests {
		l := func(kv ...string) map[string]string {
			return with(append([]string{"request", r.Request, "scenario", r.Scenario, "method", r.Method}, kv...)...)
		}
		add("pyro_request_reqs_total", float64(r.Count), l())
		add("pyro_request_failed_total", float64(r.Failed), l())
		add("pyro_request_duration_seconds", r.Duration.P95/1000, l("quantile", "0.95"))
		add("pyro_request_duration_seconds", r.Duration.P99/1000, l("quantile", "0.99"))
	}
	if t.CPU >= 0 {
		add("pyro_generator_cpu_ratio", t.CPU, base)
	}
	add("pyro_generator_memory_bytes", t.MemMB*(1<<20), base)
	for _, a := range t.Agents {
		add("pyro_agent_vus", float64(a.VUs), with("agent", a.Name))
		add("pyro_agent_reqs_per_second", a.RPS, with("agent", a.Name))
		if a.CPU >= 0 {
			add("pyro_agent_cpu_ratio", a.CPU, with("agent", a.Name))
		}
	}
	return out
}

// Set is several outputs used together.
type Set []*Output

// OpenAll opens every spec; on error, the ones already open are closed.
func OpenAll(specs []string, test, run string) (Set, error) {
	var s Set
	for _, spec := range specs {
		o, err := Open(spec, test, run)
		if err != nil {
			s.Close()
			return nil, err
		}
		s = append(s, o)
	}
	return s, nil
}

func (s Set) Tick(t load.Tick, reqs []metrics.RequestStat) {
	for _, o := range s {
		o.Tick(t, reqs)
	}
}

func (s Set) Close() error {
	var errs []error
	for _, o := range s {
		errs = append(errs, o.Close())
	}
	return errors.Join(errs...)
}
