// Package metrics collects load-test samples. Virtual users append raw
// samples to their own Buffer (no shared lock on the hot path); the
// Aggregator drains every buffer once per tick into HDR histograms:
// cumulative per series (metric + tags) for summaries and thresholds, and
// per interval per metric for live charts.
package metrics

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/HdrHistogram/hdrhistogram-go"
)

type Type int

const (
	Counter Type = iota // a sum (requests, bytes)
	Trend               // a distribution of durations in milliseconds
	Rate                // fraction of true values (failures, checks)
	Gauge               // last value (active VUs)
)

func (t Type) String() string {
	return [...]string{"counter", "trend", "rate", "gauge"}[t]
}

// Builtin metric names and types. Unknown names (custom metrics) are
// treated as trends.
var Builtin = map[string]Type{
	"http_reqs":           Counter,
	"http_req_failed":     Rate,
	"http_req_duration":   Trend,
	"http_req_blocked":    Trend,
	"http_req_connecting": Trend,
	"http_req_tls":        Trend,
	"http_req_sending":    Trend,
	"http_req_waiting":    Trend,
	"http_req_receiving":  Trend,
	"iterations":          Counter,
	"iteration_duration":  Trend,
	"dropped_iterations":  Counter,
	"group_duration":      Trend,
	"checks":              Rate,
	"data_sent":           Counter,
	"data_received":       Counter,
	"vus":                 Gauge,
	"vus_max":             Gauge,
	"ws_sessions":         Counter,
	"ws_connecting":       Trend,
	"ws_session_duration": Trend,
	"ws_msgs_sent":        Counter,
	"ws_msgs_received":    Counter,
	"ws_errors":           Counter,
	"sio_event_rtt":       Trend,
}

func TypeOf(name string) Type {
	if t, ok := Builtin[name]; ok {
		return t
	}
	if t, ok := custom.Load(name); ok {
		return t.(Type)
	}
	return Trend
}

var custom sync.Map // name -> Type, for metrics defined by scripts

// Register declares a custom metric's type (scripts call it before their
// first sample). Built-in names can't be redefined, and a custom name keeps
// its first type.
func Register(name string, t Type) error {
	if _, ok := Builtin[name]; ok {
		return fmt.Errorf("%s is a built-in metric", name)
	}
	if prev, loaded := custom.LoadOrStore(name, t); loaded && prev.(Type) != t {
		return fmt.Errorf("metric %s is already a %s", name, prev.(Type))
	}
	return nil
}

// Tags label a sample. Comparable, so it can key a map.
type Tags struct {
	Scenario string `json:"scenario,omitempty"`
	Request  string `json:"request,omitempty"`
	Method   string `json:"method,omitempty"`
	Status   string `json:"status,omitempty"`
	Group    string `json:"group,omitempty"`
}

// Get returns a tag by name, for threshold filters.
func (t Tags) Get(name string) string {
	switch name {
	case "scenario":
		return t.Scenario
	case "request", "name":
		return t.Request
	case "method":
		return t.Method
	case "status":
		return t.Status
	case "group":
		return t.Group
	}
	return ""
}

type Sample struct {
	Metric string
	Tags   Tags
	Value  float64 // ms for trends; 0/1 for rates; amount for counters
}

// Buffer is one VU's sample buffer.
type Buffer struct {
	mu      sync.Mutex
	samples []Sample
}

func (b *Buffer) Add(s Sample) {
	b.mu.Lock()
	b.samples = append(b.samples, s)
	b.mu.Unlock()
}

// AddMany appends several samples under one lock.
func (b *Buffer) AddMany(ss ...Sample) {
	b.mu.Lock()
	b.samples = append(b.samples, ss...)
	b.mu.Unlock()
}

func (b *Buffer) swap(spare []Sample) []Sample {
	b.mu.Lock()
	out := b.samples
	b.samples = spare[:0]
	b.mu.Unlock()
	return out
}

// HDR range: 1µs to 1h at 3 significant digits (0.1% error).
const (
	histMin = 1
	histMax = int64(time.Hour / time.Microsecond)
	histSig = 3
)

func newHist() *hdrhistogram.Histogram { return hdrhistogram.New(histMin, histMax, histSig) }

func toMicros(ms float64) int64 {
	us := int64(math.Round(ms * 1000))
	if us < histMin {
		return histMin
	}
	if us > histMax {
		return histMax
	}
	return us
}

type series struct {
	typ    Type
	hist   *hdrhistogram.Histogram // trends
	sum    float64                 // counters: total; trends: sum of values
	count  int64                   // samples
	trues  int64                   // rates
	last   float64                 // gauges
	maxVal float64
}

func newSeries(t Type) *series {
	s := &series{typ: t}
	if t == Trend {
		s.hist = newHist()
	}
	return s
}

func (s *series) add(v float64) {
	s.count++
	switch s.typ {
	case Counter:
		s.sum += v
	case Trend:
		s.sum += v
		_ = s.hist.RecordValue(toMicros(v))
	case Rate:
		if v != 0 {
			s.trues++
		}
	case Gauge:
		s.last = v
		if v > s.maxVal {
			s.maxVal = v
		}
	}
}

type key struct {
	Metric string
	Tags   Tags
}

// Aggregator owns all series. Safe for concurrent use.
type Aggregator struct {
	mu      sync.Mutex
	buffers []*Buffer
	total   map[key]*series
	tick    map[string]*series // per metric, all tags merged, reset each flush
	spare   []Sample
	// delta holds what each series gained since the last TakeDelta, when
	// TrackDeltas is on (agents ship it to a controller).
	delta map[key]*series
	// MinWindow is the shortest time an interval's per-second rates are
	// computed over. Load tests set it to the tick interval, so a short final
	// interval (or agents' late final reports) doesn't show a spike.
	MinWindow time.Duration
	start     time.Time
	last      time.Time
}

func NewAggregator() *Aggregator {
	now := time.Now()
	return &Aggregator{total: map[key]*series{}, tick: map[string]*series{}, start: now, last: now}
}

// NewBuffer registers a buffer for a VU.
func (a *Aggregator) NewBuffer() *Buffer {
	b := &Buffer{}
	a.mu.Lock()
	a.buffers = append(a.buffers, b)
	a.mu.Unlock()
	return b
}

// Record adds a sample directly (for the scheduler: gauges, dropped iterations).
func (a *Aggregator) Record(s Sample) {
	a.mu.Lock()
	a.add(s)
	a.mu.Unlock()
}

func (a *Aggregator) add(s Sample) {
	t := TypeOf(s.Metric)
	k := key{s.Metric, s.Tags}
	ser := a.total[k]
	if ser == nil {
		ser = newSeries(t)
		a.total[k] = ser
	}
	ser.add(s.Value)
	if a.delta != nil && t != Gauge {
		d := a.delta[k]
		if d == nil {
			d = newSeries(t)
			a.delta[k] = d
		}
		d.add(s.Value)
	}
	ts := a.tick[s.Metric]
	if ts == nil {
		ts = newSeries(t)
		a.tick[s.Metric] = ts
	}
	ts.add(s.Value)
}

// Flush drains all buffers and returns the interval since the last flush.
func (a *Aggregator) Flush() Interval {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, b := range a.buffers {
		drained := b.swap(a.spare)
		for _, s := range drained {
			a.add(s)
		}
		a.spare = drained
	}
	now := time.Now()
	iv := Interval{Start: a.last, End: now, Elapsed: now.Sub(a.start), metrics: map[string]Stat{}}
	secs := max(now.Sub(a.last), a.MinWindow).Seconds()
	for name, s := range a.tick {
		st := statOf(s)
		if s.typ == Counter && secs > 0 {
			st.PerSecond = s.sum / secs
		}
		iv.metrics[name] = st
	}
	// Gauges carry over between intervals; everything else restarts.
	next := map[string]*series{}
	for name, s := range a.tick {
		if s.typ == Gauge {
			g := newSeries(Gauge)
			g.last, g.maxVal = s.last, s.last
			next[name] = g
		}
	}
	a.tick = next
	a.last = now
	return iv
}

// Pending reports whether samples were recorded since the last Flush
// (gauges aside).
func (a *Aggregator) Pending() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, b := range a.buffers {
		b.mu.Lock()
		n := len(b.samples)
		b.mu.Unlock()
		if n > 0 {
			return true
		}
	}
	for _, s := range a.tick {
		if s.typ != Gauge && s.count > 0 {
			return true
		}
	}
	return false
}

// Interval is what happened between two flushes.
type Interval struct {
	Start, End time.Time
	Elapsed    time.Duration // since the aggregator started
	metrics    map[string]Stat
}

// Metric returns the interval's stat for a metric (zero if none).
func (iv Interval) Metric(name string) Stat { return iv.metrics[name] }

// Stat summarises a series or a merge of series. Durations are in ms.
type Stat struct {
	Type      string  `json:"type"`
	Count     int64   `json:"count"`
	Value     float64 `json:"value"` // counter total, gauge last, rate fraction, trend avg
	PerSecond float64 `json:"perSecond,omitempty"`
	Avg       float64 `json:"avg,omitempty"`
	Min       float64 `json:"min,omitempty"`
	Med       float64 `json:"med,omitempty"`
	Max       float64 `json:"max,omitempty"`
	P90       float64 `json:"p90,omitempty"`
	P95       float64 `json:"p95,omitempty"`
	P99       float64 `json:"p99,omitempty"`
	hist      *hdrhistogram.Histogram
}

// Percentile returns any percentile of a trend (0 for other types).
func (s Stat) Percentile(p float64) float64 {
	if s.hist == nil || s.hist.TotalCount() == 0 {
		return 0
	}
	return float64(s.hist.ValueAtQuantile(p)) / 1000
}

func statOf(s *series) Stat {
	st := Stat{Type: s.typ.String(), Count: s.count}
	switch s.typ {
	case Counter:
		st.Value = s.sum
	case Gauge:
		st.Value, st.Max = s.last, s.maxVal
	case Rate:
		if s.count > 0 {
			st.Value = float64(s.trues) / float64(s.count)
		}
	case Trend:
		fillTrend(&st, s.hist, s.sum, s.count)
	}
	return st
}

func fillTrend(st *Stat, h *hdrhistogram.Histogram, sum float64, count int64) {
	st.hist = h
	if count == 0 {
		return
	}
	st.Avg = sum / float64(count)
	st.Value = st.Avg
	st.Min = float64(h.Min()) / 1000
	st.Max = float64(h.Max()) / 1000
	st.Med = st.Percentile(50)
	st.P90 = st.Percentile(90)
	st.P95 = st.Percentile(95)
	st.P99 = st.Percentile(99)
}

// Query merges every cumulative series of a metric whose tags match filter.
func (a *Aggregator) Query(metric string, filter map[string]string) Stat {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.query(metric, filter)
}

func (a *Aggregator) query(metric string, filter map[string]string) Stat {
	t := TypeOf(metric)
	merged := newSeries(t)
	found := false
	for k, s := range a.total {
		if k.Metric != metric || !matches(k.Tags, filter) {
			continue
		}
		found = true
		merged.count += s.count
		merged.sum += s.sum
		merged.trues += s.trues
		if t == Trend {
			merged.hist.Merge(s.hist)
		}
		if t == Gauge {
			merged.last += s.last
			merged.maxVal = math.Max(merged.maxVal, s.maxVal)
		}
	}
	st := statOf(merged)
	if !found {
		st.Count = 0
	}
	if t == Counter {
		if secs := a.last.Sub(a.start).Seconds(); secs > 0 {
			st.PerSecond = merged.sum / secs
		}
	}
	return st
}

func matches(t Tags, filter map[string]string) bool {
	for k, v := range filter {
		if t.Get(k) != v {
			return false
		}
	}
	return true
}

// RequestStat is one row of the per-request table.
type RequestStat struct {
	Scenario string  `json:"scenario"`
	Request  string  `json:"request"`
	Method   string  `json:"method"`
	Count    int64   `json:"count"`
	Failed   int64   `json:"failed"`
	PerSec   float64 `json:"perSecond"`
	Duration Stat    `json:"duration"`
}

// Requests summarises HTTP requests per (scenario, request), statuses merged.
func (a *Aggregator) Requests() []RequestStat {
	a.mu.Lock()
	defer a.mu.Unlock()
	type rk struct{ scenario, request string }
	method := map[rk]string{}
	for k := range a.total {
		if k.Metric == "http_req_duration" {
			method[rk{k.Tags.Scenario, k.Tags.Request}] = k.Tags.Method
		}
	}
	secs := a.last.Sub(a.start).Seconds()
	out := make([]RequestStat, 0, len(method))
	for r, m := range method {
		f := map[string]string{"scenario": r.scenario, "request": r.request}
		failed := a.query("http_req_failed", f)
		d := a.query("http_req_duration", f)
		row := RequestStat{Scenario: r.scenario, Request: r.request, Method: m, Count: d.Count, Duration: d,
			Failed: int64(math.Round(failed.Value * float64(failed.Count)))}
		if secs > 0 {
			row.PerSec = float64(d.Count) / secs
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scenario != out[j].Scenario {
			return out[i].Scenario < out[j].Scenario
		}
		return out[i].Request < out[j].Request
	})
	return out
}

// Totals returns every metric merged across tags.
func (a *Aggregator) Totals() map[string]Stat {
	a.mu.Lock()
	defer a.mu.Unlock()
	names := map[string]bool{}
	for k := range a.total {
		names[k.Metric] = true
	}
	out := make(map[string]Stat, len(names))
	for n := range names {
		out[n] = a.query(n, nil)
	}
	return out
}

// Elapsed is the time covered so far (start to last flush).
func (a *Aggregator) Elapsed() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.last.Sub(a.start)
}
