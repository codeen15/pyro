package load

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/codeen15/pyro/pkg/engine/metrics"
	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/protocol/httpdriver"
)

// Worker runs one segment of a distributed test, usually a remote agent
// (see pkg/engine/agent).
type Worker interface {
	Name() string
	// Prepare sends the job; the worker validates it and waits for Run.
	Prepare(ctx context.Context, job Job) error
	// Run starts the prepared job and calls report once per tick, the last
	// time when the job ends. Cancelling ctx aborts the job.
	Run(ctx context.Context, report func(Report)) error
	// Stop asks a running job to end gracefully (like Test.Stop).
	Stop()
	// Close releases a worker that was prepared but not run.
	Close()
}

// Job is everything a worker needs: it has no access to the workspace.
type Job struct {
	Plan     *Plan `json:"plan"`
	Segment  int   `json:"segment"`
	Segments int   `json:"segments"`
	FirstVU  int   `json:"firstVU"`
	// Targets are the plan's requests by path, with their collection chains.
	Targets     map[string]Target  `json:"targets"`
	Environment *model.Environment `json:"environment,omitempty"`
	Globals     map[string]string  `json:"globals,omitempty"`
	// Rows are data rows by scenario. Shared data is split between workers
	// (each row is used by one worker); per-vu and random data is not.
	Rows map[string][]map[string]string `json:"rows,omitempty"`
	// Files are body files the requests reference, by workspace path.
	Files map[string][]byte `json:"files,omitempty"`
	// Plugins lists the plugin types the requests need, as kind:id
	// (protocol:tcp, auth:hmac).
	Plugins     []string `json:"plugins,omitempty"`
	InsecureTLS bool     `json:"insecureTLS,omitempty"`
}

// Report is a worker's progress for one tick.
type Report struct {
	Active    int                   `json:"active"`
	Allocated int                   `json:"allocated"`
	Deltas    []metrics.SeriesDelta `json:"deltas,omitempty"`
	// Errors are cumulative counts.
	Errors []ErrorCount `json:"errors,omitempty"`
	// CPU (0..1) and MemMB are the worker's own load-generator health.
	CPU   float64 `json:"cpu"`
	MemMB float64 `json:"memMB"`
}

// AgentStatus is one worker's part of a distributed test.
type AgentStatus struct {
	Name     string  `json:"name"`
	Segment  int     `json:"segment"`
	VUs      int     `json:"vus"`
	Requests int64   `json:"requests"`
	RPS      float64 `json:"rps,omitempty"` // in the last tick
	State    string  `json:"state"`         // running | done | failed
	Error    string  `json:"error,omitempty"`
	// CPU (0..1) and MemMB are the latest reported; PeakCPU the highest.
	CPU     float64 `json:"cpu"`
	PeakCPU float64 `json:"peakCPU"`
	MemMB   float64 `json:"memMB"`
}

// maxJobFiles caps the body files sent to each worker.
const maxJobFiles = 64 << 20

type workerState struct {
	w       Worker
	segment int

	mu        sync.Mutex
	reports   int
	active    int
	allocated int
	requests  int64
	lastReqs  int64
	errors    []ErrorCount
	state     string
	err       string
	cpu, mem  float64
	peakCPU   float64
	sat       saturation
}

func (w *workerState) vus() (int, int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.active, w.allocated
}

func (w *workerState) apply(r Report) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reports++
	w.active, w.allocated = r.Active, max(w.allocated, r.Allocated)
	w.errors = r.Errors
	w.cpu, w.mem = r.CPU, r.MemMB
	w.peakCPU = max(w.peakCPU, r.CPU)
	w.sat.observe(r.CPU)
	for _, d := range r.Deltas {
		if d.Metric == "http_reqs" {
			w.requests += int64(d.Sum)
		}
	}
}

// prepareWorkers builds each worker's job and prepares them in parallel.
func (t *Test) prepareWorkers(rows map[string][]map[string]string) error {
	files, err := t.jobFiles()
	if err != nil {
		return err
	}
	plugins := t.jobPlugins()
	n := len(t.opts.Workers)
	var jobs []Job
	for i, w := range t.opts.Workers {
		seg := t.plan.Segment(i, n)
		// Each agent gets its share of the cap, whether it came from the
		// plan or from this run's options.
		seg.MaxRPS = effectiveRPS(t.plan.MaxRPS, t.opts.MaxRPS) / float64(n)
		if len(seg.Scenarios) == 0 {
			continue // too little load to give this worker any
		}
		job := Job{
			Plan: seg, Segment: i, Segments: n, FirstVU: firstVU(t.plan, i, n), Targets: t.targets,
			Environment: t.opts.Environment, Globals: t.opts.Globals, Files: files, Plugins: plugins, InsecureTLS: t.opts.InsecureTLS,
			Rows: map[string][]map[string]string{},
		}
		for name, r := range rows {
			if _, ok := seg.Scenarios[name]; !ok {
				continue
			}
			if d := seg.Scenarios[name].Data; (d.Mode == "" || d.Mode == "shared") && len(r) >= n {
				var part []map[string]string
				for j := i; j < len(r); j += n {
					part = append(part, r[j])
				}
				r = part
			}
			job.Rows[name] = r
		}
		jobs = append(jobs, job)
		t.workers = append(t.workers, &workerState{w: w, segment: i, state: "running"})
	}
	ctx, cancel := context.WithTimeout(t.hard, 2*time.Minute)
	defer cancel()
	errs := make([]error, len(t.workers))
	var wg sync.WaitGroup
	for i, ws := range t.workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := ws.w.Prepare(ctx, jobs[i]); err != nil {
				errs[i] = fmt.Errorf("agent %s: %w", ws.w.Name(), err)
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// runWorkers runs every prepared worker and merges their reports. A worker
// that fails stops the whole test: its share of the load is missing.
func (t *Test) runWorkers() {
	go func() {
		<-t.soft.Done()
		for _, ws := range t.workers {
			ws.w.Stop()
		}
	}()
	var wg sync.WaitGroup
	for _, ws := range t.workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := ws.w.Run(t.hard, func(r Report) {
				t.agg.Merge(r.Deltas)
				ws.apply(r)
				select {
				case t.reported <- struct{}{}:
				default:
				}
			})
			ws.mu.Lock()
			ws.active = 0
			switch {
			case err != nil && t.hard.Err() == nil:
				ws.state, ws.err = "failed", err.Error()
			default:
				ws.state = "done"
			}
			ws.mu.Unlock()
			if err != nil && t.hard.Err() == nil {
				t.end(StatusFailed, fmt.Sprintf("agent %s failed: %v", ws.w.Name(), err))
				t.softStop()
			}
		}()
	}
	wg.Wait()
}

// distributedTicks ticks once every running worker has reported the next
// interval, so each controller tick holds the same interval from every
// agent. A worker that is late by half an interval doesn't hold it up.
func (t *Test) distributedTicks(finished <-chan struct{}, onTick func()) {
	n := 0
	wait := t.opts.TickInterval * 3 / 2
	timer := time.NewTimer(wait)
	defer timer.Stop()
	tick := func() {
		onTick()
		n++
		timer.Reset(wait)
	}
	for {
		select {
		case <-t.reported:
			if t.allReported(n + 1) {
				tick()
			}
		case <-timer.C:
			tick()
		case <-finished:
			t.finish()
			return
		}
	}
}

// allReported reports whether every running worker has sent n reports.
func (t *Test) allReported(n int) bool {
	for _, ws := range t.workers {
		ws.mu.Lock()
		behind := ws.state == "running" && ws.reports < n
		ws.mu.Unlock()
		if behind {
			return false
		}
	}
	return true
}

// agentStatus summarises the workers; secs > 0 computes per-tick RPS.
func (t *Test) agentStatus(secs float64) []AgentStatus {
	if len(t.workers) == 0 {
		return nil
	}
	out := make([]AgentStatus, len(t.workers))
	for i, ws := range t.workers {
		ws.mu.Lock()
		out[i] = AgentStatus{Name: ws.w.Name(), Segment: ws.segment, VUs: ws.active, Requests: ws.requests, State: ws.state, Error: ws.err,
			CPU: ws.cpu, PeakCPU: ws.peakCPU, MemMB: ws.mem}
		if secs > 0 {
			out[i].RPS = float64(ws.requests-ws.lastReqs) / secs
			ws.lastReqs = ws.requests
		} else {
			out[i].VUs = ws.allocated
		}
		ws.mu.Unlock()
	}
	return out
}

// errorsTop merges this process's errors with every worker's.
func (t *Test) errorsTop(n int) []ErrorCount {
	if len(t.workers) == 0 {
		return t.errors.top(n)
	}
	sum := map[string]int64{}
	for _, ws := range t.workers {
		ws.mu.Lock()
		for _, e := range ws.errors {
			sum[e.Message] += e.Count
		}
		ws.mu.Unlock()
	}
	out := make([]ErrorCount, 0, len(sum))
	for m, c := range sum {
		out = append(out, ErrorCount{m, c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Message < out[j].Message
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// jobFiles reads the body files the plan's requests reference. Paths with
// variables can't be known in advance and are left out.
func (t *Test) jobFiles() (map[string][]byte, error) {
	files := map[string][]byte{}
	total := 0
	add := func(p string) error {
		if p == "" || strings.Contains(p, "{{") || files[p] != nil {
			return nil
		}
		full, err := httpdriver.ResolveFile(t.opts.FileRoot, p, t.opts.AllowFilesOutsideRoot)
		if err != nil {
			return nil // reported by the agent when a request needs it
		}
		b, err := os.ReadFile(full)
		if err != nil {
			return nil // reported by the agent when a request needs it
		}
		if total += len(b); total > maxJobFiles {
			return fmt.Errorf("body files are larger than %d MiB, too much to send to agents", maxJobFiles>>20)
		}
		files[p] = b
		return nil
	}
	for _, tg := range t.targets {
		b := tg.Request.Body
		if b == nil {
			continue
		}
		if b.Type == model.BodyBinaryFile {
			if err := add(b.File); err != nil {
				return nil, err
			}
		}
		for _, f := range b.Fields {
			if f.File && !f.Disabled {
				if err := add(f.Value); err != nil {
					return nil, err
				}
			}
		}
	}
	return files, nil
}

// jobPlugins lists the plugin request and auth types the plan uses.
func (t *Test) jobPlugins() []string {
	seen := map[string]bool{}
	note := func(kind, s string) {
		if id, ok := strings.CutPrefix(s, "plugin:"); ok {
			seen[kind+":"+id] = true
		}
	}
	for _, tg := range t.targets {
		note("protocol", string(tg.Request.Kind))
		if tg.Request.Auth != nil {
			note("auth", tg.Request.Auth.Type)
		}
		for _, c := range tg.Chain {
			if c != nil && c.Auth != nil {
				note("auth", c.Auth.Type)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// PrepareJob prepares a worker's segment as a local test that reports
// deltas to report. The caller calls Begin, and Stop/Abort as asked.
func PrepareJob(job Job, opts Options, report func(Report)) (*Test, error) {
	if job.Plan == nil || len(job.Plan.Scenarios) == 0 {
		return nil, fmt.Errorf("the job has no scenarios")
	}
	opts.Environment = job.Environment
	opts.Globals = job.Globals
	opts.Rows = job.Rows
	opts.FirstVU = job.FirstVU
	opts.InsecureTLS = job.InsecureTLS
	opts.OnReport = report
	opts.Workers = nil
	opts.LoadData = func(file string) ([]map[string]string, error) {
		return nil, fmt.Errorf("data file %s wasn't sent with the job", file)
	}
	opts.Resolve = func(path string) (Target, error) {
		tg, ok := job.Targets[path]
		if !ok || tg.Request == nil {
			return Target{}, fmt.Errorf("request %s wasn't sent with the job", path)
		}
		return tg, nil
	}
	opts.ReadFile = func(p string) ([]byte, error) {
		if b, ok := job.Files[p]; ok {
			return b, nil
		}
		return nil, fmt.Errorf("file %s isn't available on the agent (paths with variables can't be sent)", p)
	}
	plan := *job.Plan
	plan.Thresholds = nil
	if err := plan.Validate(nil); err != nil {
		return nil, err
	}
	return Prepare(&plan, opts)
}
