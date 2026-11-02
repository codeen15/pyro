package metrics

import "time"

// SeriesDelta is what one series gained during an interval. Agents send
// these to a controller, which merges them into its own Aggregator, so
// percentiles over all agents are exact (to the histogram's precision), not
// averages of percentiles. Gauges are not included: the controller sums
// each agent's reported VU counts itself.
type SeriesDelta struct {
	Metric string  `json:"m"`
	Tags   Tags    `json:"t"`
	Count  int64   `json:"n"`
	Sum    float64 `json:"s,omitempty"`
	Trues  int64   `json:"r,omitempty"`
	// Type is set for custom (script-defined) metrics, so the controller
	// merges them with the right type.
	Type string `json:"y,omitempty"`
	// Buckets are a trend's non-empty HDR buckets as [value µs, count]
	// pairs; value is the bucket's lowest equivalent value, so merging is
	// lossless.
	Buckets [][2]int64 `json:"b,omitempty"`
}

// TrackDeltas makes the aggregator keep per-series deltas for TakeDelta.
func (a *Aggregator) TrackDeltas() {
	a.mu.Lock()
	if a.delta == nil {
		a.delta = map[key]*series{}
	}
	a.mu.Unlock()
}

// TakeDelta returns what every series gained since the previous call and
// resets the deltas. Call it after Flush, which drains the VU buffers.
func (a *Aggregator) TakeDelta() []SeriesDelta {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []SeriesDelta
	for k, s := range a.delta {
		if s.count == 0 {
			continue
		}
		d := SeriesDelta{Metric: k.Metric, Tags: k.Tags, Count: s.count, Sum: s.sum, Trues: s.trues}
		if _, builtin := Builtin[k.Metric]; !builtin {
			d.Type = s.typ.String()
		}
		if s.hist != nil {
			for i, c := range s.hist.Export().Counts {
				if c != 0 {
					d.Buckets = append(d.Buckets, [2]int64{bucketValue(int32(i)), c})
				}
			}
			s.hist.Reset()
		}
		s.count, s.sum, s.trues = 0, 0, 0
		out = append(out, d)
	}
	return out
}

// bucketValue is the lowest value of a flat counts index for the histogram
// geometry used here (lowest 1, 3 significant figures: unit magnitude 0,
// sub-bucket half count 1024), as in HdrHistogram's valueFromFlatIndex.
func bucketValue(idx int32) int64 {
	const halfMag, half = 10, 1024
	bucket := (idx >> halfMag) - 1
	sub := (idx & (half - 1)) + half
	if bucket < 0 {
		sub -= half
		bucket = 0
	}
	return int64(sub) << uint(bucket)
}

// Merge adds deltas from an agent to the cumulative and current-interval
// series, as if their samples had been recorded here.
func (a *Aggregator) Merge(deltas []SeriesDelta) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, d := range deltas {
		if d.Type != "" {
			_ = Register(d.Metric, parseType(d.Type))
		}
		t := TypeOf(d.Metric)
		if t == Gauge || d.Count == 0 {
			continue
		}
		k := key{d.Metric, d.Tags}
		ser := a.total[k]
		if ser == nil {
			ser = newSeries(t)
			a.total[k] = ser
		}
		ts := a.tick[d.Metric]
		if ts == nil {
			ts = newSeries(t)
			a.tick[d.Metric] = ts
		}
		for _, s := range []*series{ser, ts} {
			s.count += d.Count
			s.sum += d.Sum
			s.trues += d.Trues
			if s.hist != nil {
				for _, b := range d.Buckets {
					_ = s.hist.RecordValues(b[0], b[1])
				}
			}
		}
	}
}

// Since is when the aggregator started (for tests and controllers).
func (a *Aggregator) Since() time.Time { return a.start }

func parseType(s string) Type {
	switch s {
	case "counter":
		return Counter
	case "rate":
		return Rate
	case "gauge":
		return Gauge
	}
	return Trend
}
