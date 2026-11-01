package output

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"os"
	"strconv"
)

// jsonFile writes one JSON object per tick (NDJSON).
type jsonFile struct {
	f *os.File
	w *bufio.Writer
}

func newJSONFile(path string) (*jsonFile, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &jsonFile{f: f, w: bufio.NewWriter(f)}, nil
}

func (j *jsonFile) write(_ context.Context, b Batch) error {
	line, err := json.Marshal(struct {
		Time     string `json:"time"`
		Tick     any    `json:"tick"`
		Requests any    `json:"requests,omitempty"`
	}{b.Time.UTC().Format("2006-01-02T15:04:05.000Z07:00"), b.Tick, b.Requests})
	if err != nil {
		return err
	}
	j.w.Write(line)
	return j.w.WriteByte('\n')
}

func (j *jsonFile) close() error {
	if err := j.w.Flush(); err != nil {
		j.f.Close()
		return err
	}
	return j.f.Close()
}

// csvFile writes one row per tick.
type csvFile struct {
	f *os.File
	w *csv.Writer
}

var csvHeader = []string{"time", "elapsed_s", "vus", "vus_max", "requests", "rps", "error_rate", "iterations_ps", "dropped",
	"received_bps", "ws_msgs_ps", "latency_avg_ms", "latency_p50_ms", "latency_p90_ms", "latency_p95_ms", "latency_p99_ms", "latency_max_ms", "thresholds_failing"}

func newCSVFile(path string) (*csvFile, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	c := &csvFile{f: f, w: csv.NewWriter(f)}
	return c, c.w.Write(csvHeader)
}

func (c *csvFile) write(_ context.Context, b Batch) error {
	t := b.Tick
	failing := 0
	for _, th := range t.Thresholds {
		if !th.Passed {
			failing++
		}
	}
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	return c.w.Write([]string{
		b.Time.UTC().Format("2006-01-02T15:04:05.000Z"), f(t.Elapsed), strconv.Itoa(t.VUs), strconv.Itoa(t.VUsMax),
		strconv.FormatInt(t.Requests, 10), f(t.RPS), f(t.ErrorRate), f(t.IterationsPS), f(t.Dropped),
		f(t.ReceivedPS), f(t.WSMessagesPS), f(t.Latency.Avg), f(t.Latency.P50), f(t.Latency.P90), f(t.Latency.P95),
		f(t.Latency.P99), f(t.Latency.Max), strconv.Itoa(failing),
	})
}

func (c *csvFile) close() error {
	c.w.Flush()
	if err := c.w.Error(); err != nil {
		c.f.Close()
		return err
	}
	return c.f.Close()
}
