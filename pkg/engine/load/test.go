package load

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
	"github.com/expr-lang/expr/vm"

	"github.com/codeen15/pyro/pkg/engine"
	"github.com/codeen15/pyro/pkg/engine/auth"
	"github.com/codeen15/pyro/pkg/engine/metrics"
	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/plugin"
	"github.com/codeen15/pyro/pkg/engine/protocol/httpdriver"
)

// DefaultMaxVUs caps how many VUs one test may allocate.
const DefaultMaxVUs = 50000

type Options struct {
	Environment *model.Environment
	// Globals are workspace-wide variables.
	Globals map[string]string
	// Resolve loads a request by workspace path.
	Resolve func(path string) (Target, error)
	// LoadData loads a data file named in the plan.
	LoadData func(file string) ([]map[string]string, error)
	MaxVUs   int
	// TickInterval defaults to 1s.
	TickInterval time.Duration
	OnTick       func(Tick)
	FileRoot     string
	// AllowFilesOutsideRoot lets body files live outside FileRoot.
	AllowFilesOutsideRoot bool
	InsecureTLS           bool
	// Plugins provides plugin request and auth types.
	Plugins *plugin.Manager
	// AllowHosts are hosts script steps may call besides the plan's own
	// (the workspace's loadAllowHosts).
	AllowHosts []string
	// MaxRPS caps request starts per second (with the plan's maxRPS, the
	// lower applies; 0 = no cap). Workspaces set it from loadMaxRPS.
	MaxRPS float64

	// Workers run the test distributed: each runs a segment of the plan and
	// reports metric deltas, which are merged here, so thresholds and
	// percentiles cover every sample. Nothing runs locally.
	Workers []Worker
	// The options below are for running one segment on a worker (agent).
	// Rows are data rows by scenario, used instead of LoadData.
	Rows map[string][]map[string]string
	// FirstVU offsets VU ids, so they don't repeat across workers.
	FirstVU int
	// ReadFile replaces reading body files from FileRoot.
	ReadFile func(path string) ([]byte, error)
	// OnReport receives each tick's metric deltas and cumulative errors.
	OnReport func(Report)
}

// Latency is a duration distribution in milliseconds.
type Latency struct {
	Avg float64 `json:"avg"`
	P50 float64 `json:"p50"`
	P90 float64 `json:"p90"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}

func latencyOf(s metrics.Stat) Latency {
	return Latency{Avg: s.Avg, P50: s.Med, P90: s.P90, P95: s.P95, P99: s.P99, Max: s.Max}
}

// Tick is one interval of live data.
type Tick struct {
	Elapsed      float64           `json:"elapsed"` // seconds since start
	VUs          int               `json:"vus"`
	VUsMax       int               `json:"vusMax"`
	RPS          float64           `json:"rps"`
	IterationsPS float64           `json:"iterationsPerSecond"`
	ErrorRate    float64           `json:"errorRate"`
	Requests     int64             `json:"requests"`
	Dropped      float64           `json:"dropped,omitempty"`
	ReceivedPS   float64           `json:"receivedPerSecond"`
	WSMessagesPS float64           `json:"wsMessagesPerSecond,omitempty"`
	Latency      Latency           `json:"latency"`
	Thresholds   []ThresholdResult `json:"thresholds,omitempty"`
	// CPU is this load generator's CPU use (0..1 of its usable cores) and
	// MemMB its memory; for distributed tests, see each agent's instead.
	CPU   float64 `json:"cpu"`
	MemMB float64 `json:"memMB"`
	// Agents is set for distributed tests.
	Agents []AgentStatus `json:"agents,omitempty"`
}

// Status of a finished test.
const (
	StatusPassed  = "passed"
	StatusFailed  = "failed"  // a threshold failed
	StatusAborted = "aborted" // an abortOnFail threshold ended it early
	StatusStopped = "stopped" // stopped by the user
)

type Result struct {
	Name       string                  `json:"name"`
	Status     string                  `json:"status"`
	Reason     string                  `json:"reason,omitempty"`
	StartedAt  time.Time               `json:"startedAt"`
	Duration   float64                 `json:"duration"` // seconds
	Ticks      []Tick                  `json:"ticks"`
	Metrics    map[string]metrics.Stat `json:"metrics"`
	Requests   []metrics.RequestStat   `json:"requests"`
	Thresholds []ThresholdResult       `json:"thresholds"`
	Errors     []ErrorCount            `json:"errors"`
	MaxVUs     int                     `json:"maxVUs"`
	// MaxRPS is the cap that applied (0 = none).
	MaxRPS float64       `json:"maxRPS,omitempty"`
	Agents []AgentStatus `json:"agents,omitempty"`
	// Warnings are about the run itself (e.g. a CPU-bound load generator).
	Warnings []string `json:"warnings,omitempty"`
}

// Test is a running load test.
type Test struct {
	plan     *Plan
	opts     Options
	agg      *metrics.Aggregator
	http     *httpdriver.Driver
	targets  map[string]Target
	scripted map[string]bool
	ths      thresholds
	errors   *errorCounter
	progs    sync.Map // expression -> *vm.Program
	feeders  map[string]*feeder
	workers  []*workerState
	reported chan struct{} // a worker sent a report
	scripts  map[*Step]*goja.Program
	allowed  map[string]bool // hosts pyro.http may call
	cpu      *cpuSampler
	oauth    *auth.OAuth2 // shared by every VU, so a token is fetched once
	pace     *pacer       // the requests-per-second cap, if any
	sat      saturation

	vuSeq     atomic.Int64
	allocated atomic.Int64 // VUs created
	active    atomic.Int64 // VUs running (VU executors) or busy (arrival-rate)
	busy      atomic.Int64

	soft     context.Context // done: stop starting iterations
	softStop context.CancelFunc
	hard     context.Context // done: cancel in-flight work
	hardStop context.CancelFunc

	mu      sync.Mutex
	ticks   []Tick
	status  string
	reason  string
	started time.Time
	done    chan struct{}
	result  *Result
}

// Start validates the plan's references, then runs it in the background.
func Start(p *Plan, opts Options) (*Test, error) {
	t, err := Prepare(p, opts)
	if err != nil {
		return nil, err
	}
	t.Begin()
	return t, nil
}

// Prepare validates the plan's references and loads its data (and, for a
// distributed test, sends every worker its segment) without starting.
// Call Begin to start, or Close to discard it.
func Prepare(p *Plan, opts Options) (*Test, error) {
	if opts.TickInterval == 0 {
		opts.TickInterval = time.Second
	}
	if opts.MaxVUs == 0 {
		opts.MaxVUs = DefaultMaxVUs
	}
	t := &Test{
		plan: p, opts: opts, agg: metrics.NewAggregator(), targets: map[string]Target{}, scripted: map[string]bool{},
		ths: compileThresholds(p), errors: newErrorCounter(), done: make(chan struct{}), reported: make(chan struct{}, 1),
		scripts: map[*Step]*goja.Program{},
	}
	t.oauth = auth.NewOAuth2()
	t.pace = newPacer(effectiveRPS(p.MaxRPS, opts.MaxRPS))
	t.http = httpdriver.New(httpdriver.Options{FileRoot: opts.FileRoot, AllowOutsideRoot: opts.AllowFilesOutsideRoot, ReadFile: opts.ReadFile, InsecureTLS: opts.InsecureTLS, MaxIdleConnsPerHost: 10000, MaxBodyBytes: 1 << 20})
	t.vuSeq.Store(int64(opts.FirstVU))

	// A distributed test's segments are checked by each agent.
	if need := PeakVUs(p); need > opts.MaxVUs && len(opts.Workers) == 0 {
		return nil, fmt.Errorf("this plan needs up to %d VUs; the limit is %d", need, opts.MaxVUs)
	}
	for _, name := range p.ScenarioNames() {
		if err := t.resolveSteps(p.Scenarios[name].Flow); err != nil {
			return nil, err
		}
	}
	t.feeders = map[string]*feeder{}
	rows := map[string][]map[string]string{}
	for _, name := range p.ScenarioNames() {
		if d := p.Scenarios[name].Data; d != nil {
			if r, ok := opts.Rows[name]; ok {
				t.feeders[name], rows[name] = newFeeder(r, d), r
				continue
			}
			if opts.LoadData == nil {
				return nil, fmt.Errorf("scenario %s: data files are not available here", name)
			}
			data, err := opts.LoadData(d.File)
			if err != nil {
				return nil, fmt.Errorf("scenario %s: %w", name, err)
			}
			if len(data) == 0 {
				return nil, fmt.Errorf("scenario %s: data file %s has no rows", name, d.File)
			}
			t.feeders[name] = newFeeder(data, d)
			rows[name] = data
		}
	}

	t.allowed = map[string]bool{}
	for _, h := range append(Hosts(p, opts.Resolve, opts.Environment, opts.Globals), opts.AllowHosts...) {
		t.allowed[strings.ToLower(h)] = true
	}

	t.soft, t.softStop = context.WithCancel(context.Background())
	t.hard, t.hardStop = context.WithCancel(context.Background())
	if len(opts.Workers) > 0 {
		if err := t.prepareWorkers(rows); err != nil {
			t.Close()
			return nil, err
		}
	}
	return t, nil
}

// Begin starts a prepared test in the background.
func (t *Test) Begin() {
	t.agg = metrics.NewAggregator()
	t.cpu = newCPUSampler()
	// The last tick covers a partial interval (in-flight requests finishing
	// at stop, or agents' final reports); rating it over a full tick keeps
	// it from drawing a spike.
	t.agg.MinWindow = t.opts.TickInterval
	if t.opts.OnReport != nil {
		t.agg.TrackDeltas()
	}
	t.started = time.Now()
	go t.run()
}

// Close releases a prepared test that was never begun.
func (t *Test) Close() {
	for _, w := range t.workers {
		w.w.Close()
	}
	t.softStop()
	t.hardStop()
}

func (t *Test) resolveSteps(steps []Step) error {
	for i := range steps {
		st := &steps[i] // VUs key compiled scripts by these same pointers
		paths := scriptRequests(st.Script)
		if st.Request != "" {
			paths = append(paths, st.Request)
		}
		for _, p := range paths {
			if _, ok := t.targets[p]; !ok {
				target, err := t.opts.Resolve(p)
				if err != nil {
					return fmt.Errorf("request %s: %w", p, err)
				}
				t.targets[p] = target
				t.scripted[p] = hasScripts(target)
			}
		}
		if st.ScriptFile != "" {
			return fmt.Errorf("script file %s wasn't loaded (load plans with the workspace)", st.ScriptFile)
		}
		if st.Script != "" {
			prog, err := compileScript(st.Script)
			if err != nil {
				return fmt.Errorf("script: %w", err)
			}
			t.scripts[st] = prog
		}
		children := [][]Step{st.Steps}
		if st.Loop != nil {
			children = append(children, st.Loop.Steps)
		}
		if st.If != nil {
			children = append(children, st.If.Then, st.If.Else)
		}
		for _, b := range st.Weighted {
			children = append(children, b.Steps)
		}
		for _, c := range children {
			if err := t.resolveSteps(c); err != nil {
				return err
			}
		}
	}
	return nil
}

func hasScripts(t Target) bool {
	if s := t.Request.Scripts; s != nil && (s.Pre != "" || s.Post != "") {
		return true
	}
	for _, c := range t.Chain {
		if c != nil && c.Scripts != nil && (c.Scripts.Pre != "" || c.Scripts.Post != "") {
			return true
		}
	}
	return false
}

func (t *Test) program(src string) (*vm.Program, error) {
	if p, ok := t.progs.Load(src); ok {
		return p.(*vm.Program), nil
	}
	p, err := compileExpr(src)
	if err != nil {
		return nil, err
	}
	t.progs.Store(src, p)
	return p, nil
}

func (t *Test) run() {
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		if len(t.workers) > 0 {
			t.runWorkers()
		} else {
			t.runScenarios()
		}
	}()

	onTick := func() {
		if abort := t.tick(); abort {
			t.end(StatusAborted, "a threshold with abortOnFail failed")
			t.softStop()
			t.hardStop()
		}
	}
	if len(t.workers) > 0 {
		t.distributedTicks(finished, onTick)
		return
	}
	ticker := time.NewTicker(t.opts.TickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			onTick()
		case <-finished:
			t.finish()
			return
		}
	}
}

func (t *Test) runScenarios() {
	var wg sync.WaitGroup
	for _, name := range t.plan.ScenarioNames() {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			sc := t.plan.Scenarios[name]
			if sleep(t.soft, sc.StartTime.D()) != nil {
				return
			}
			runScenario(t, name, sc, t.feeders[name])
		}(name)
	}
	wg.Wait()
}

// vus returns the running and allocated VU counts, over all workers in a
// distributed test.
func (t *Test) vus() (active, allocated int) {
	if len(t.workers) == 0 {
		return int(t.active.Load()), int(t.allocated.Load())
	}
	for _, w := range t.workers {
		a, al := w.vus()
		active, allocated = active+a, allocated+al
	}
	return active, allocated
}

// tick flushes metrics, evaluates thresholds and publishes a Tick.
func (t *Test) tick() (abort bool) {
	active, allocated := t.vus()
	t.agg.Record(metrics.Sample{Metric: "vus", Value: float64(active)})
	t.agg.Record(metrics.Sample{Metric: "vus_max", Value: float64(allocated)})
	iv := t.agg.Flush()
	cpu, mem := t.cpu.sample()
	if len(t.workers) == 0 {
		t.sat.observe(cpu)
	}
	if t.opts.OnReport != nil {
		t.opts.OnReport(Report{Active: active, Allocated: allocated, Deltas: t.agg.TakeDelta(), Errors: t.errors.top(maxDistinct), CPU: cpu, MemMB: mem})
	}
	results, abort := t.ths.evaluate(t.agg, time.Since(t.started))
	reqs := iv.Metric("http_reqs")
	tk := Tick{
		Elapsed:      iv.Elapsed.Seconds(),
		VUs:          active,
		VUsMax:       allocated,
		RPS:          reqs.PerSecond,
		Requests:     int64(reqs.Value),
		IterationsPS: iv.Metric("iterations").PerSecond,
		ErrorRate:    iv.Metric("http_req_failed").Value,
		Dropped:      iv.Metric("dropped_iterations").Value,
		ReceivedPS:   iv.Metric("data_received").PerSecond,
		WSMessagesPS: iv.Metric("ws_msgs_received").PerSecond,
		Latency:      latencyOf(iv.Metric("http_req_duration")),
		Thresholds:   results,
		CPU:          cpu,
		MemMB:        mem,
		Agents:       t.agentStatus(max(iv.End.Sub(iv.Start), t.opts.TickInterval).Seconds()),
	}
	t.mu.Lock()
	t.ticks = append(t.ticks, tk)
	t.mu.Unlock()
	if t.opts.OnTick != nil {
		t.opts.OnTick(tk)
	}
	return abort
}

func (t *Test) end(status, reason string) {
	t.mu.Lock()
	if t.status == "" {
		t.status, t.reason = status, reason
	}
	t.mu.Unlock()
}

func (t *Test) finish() {
	// A last tick for samples since the previous one; an empty one would
	// draw a drop to zero at the end of every chart.
	if t.agg.Pending() || len(t.Ticks()) == 0 {
		t.tick()
	}
	results, _ := t.ths.evaluate(t.agg, time.Since(t.started))
	// The verdict uses the final values (like k6): a p95 that spiked during
	// warm-up and recovered passes. Breached stays set for the UI.
	status := StatusPassed
	for _, r := range results {
		if !r.Passed {
			status = StatusFailed
		}
	}
	t.mu.Lock()
	if t.status == "" {
		t.status = status
		if status == StatusFailed {
			t.reason = "one or more thresholds failed"
		}
	} else if t.status == StatusStopped && status == StatusFailed {
		t.reason += "; one or more thresholds failed"
	}
	res := &Result{
		Name: t.plan.Name, Status: t.status, Reason: t.reason, StartedAt: t.started,
		Duration: time.Since(t.started).Seconds(), Ticks: append([]Tick(nil), t.ticks...),
		Metrics: t.agg.Totals(), Requests: t.agg.Requests(), Thresholds: results,
		Errors: t.errorsTop(20), MaxVUs: t.maxVUs(), Agents: t.agentStatus(0), Warnings: t.warnings(),
		MaxRPS: effectiveRPS(t.plan.MaxRPS, t.opts.MaxRPS),
	}
	t.result = res
	t.mu.Unlock()
	t.softStop()
	t.hardStop()
	close(t.done)
}

// Stop ends the test gracefully: no new iterations, in-flight ones finish
// within each scenario's gracefulStop.
func (t *Test) Stop() {
	t.end(StatusStopped, "stopped by the user")
	t.softStop()
}

// Abort cancels everything immediately.
func (t *Test) Abort() {
	t.end(StatusStopped, "stopped by the user")
	t.softStop()
	t.hardStop()
}

// Wait blocks until the test ends and returns its result.
func (t *Test) Wait() *Result {
	<-t.done
	return t.result
}

func (t *Test) Done() <-chan struct{} { return t.done }

// Ticks returns the intervals so far.
func (t *Test) Ticks() []Tick {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Tick(nil), t.ticks...)
}

// Snapshot returns the live per-request table and totals.
func (t *Test) Snapshot() ([]metrics.RequestStat, []ErrorCount) {
	return t.agg.Requests(), t.errorsTop(20)
}

func (t *Test) maxVUs() int {
	_, allocated := t.vus()
	return allocated
}

func (t *Test) Plan() *Plan { return t.plan }

// PeakVUs estimates the most VUs a plan can allocate at once.
func PeakVUs(p *Plan) int {
	total := 0
	for _, sc := range p.Scenarios {
		switch sc.Executor {
		case ConstantVUs, PerVUIterations:
			total += sc.VUs
		case RampingVUs:
			peak := sc.StartVUs
			for _, st := range sc.Stages {
				peak = max(peak, int(st.Target))
			}
			total += peak
		case ConstantArrivalRate, RampingArrivalRate:
			total += sc.MaxVUs
		}
	}
	return total
}

// Hosts lists the distinct hosts a plan's requests target (after resolving
// variables with the environment and globals), for the safety confirmation.
// A URL whose host can't be resolved is listed as it is, so it still needs
// confirming rather than slipping through.
func Hosts(p *Plan, resolve func(string) (Target, error), env *model.Environment, globals map[string]string) []string {
	seen := map[string]bool{}
	var walk func([]Step)
	walk = func(steps []Step) {
		for _, st := range steps {
			for _, h := range scriptHosts(st.Script) {
				seen[h] = true
			}
			paths := scriptRequests(st.Script)
			if st.Request != "" {
				paths = append(paths, st.Request)
			}
			for _, path := range paths {
				if target, err := resolve(path); err == nil {
					v := engine.NewVars(env, target.Chain)
					for k, val := range globals {
						v.Globals[k] = val
					}
					raw, _ := v.Resolver().Resolve(target.Request.URL)
					if !strings.Contains(raw, "://") {
						raw = "http://" + raw
					}
					if u, err := url.Parse(raw); err == nil && u.Hostname() != "" && !strings.Contains(u.Host, "{{") {
						seen[u.Hostname()] = true
					} else {
						seen[target.Request.URL] = true
					}
				}
			}
			walk(st.Steps)
			if st.Loop != nil {
				walk(st.Loop.Steps)
			}
			if st.If != nil {
				walk(st.If.Then)
				walk(st.If.Else)
			}
			for _, b := range st.Weighted {
				walk(b.Steps)
			}
		}
	}
	for _, sc := range p.Scenarios {
		walk(sc.Flow)
	}
	for _, h := range p.Hosts {
		seen[strings.ToLower(h)] = true
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// ErrorCount is a distinct error message and how often it occurred.
type ErrorCount struct {
	Message string `json:"message"`
	Count   int64  `json:"count"`
}

// errorCounter keeps counts for up to maxDistinct messages; errors are rare
// relative to requests, so a mutex is fine.
type errorCounter struct {
	mu     sync.Mutex
	counts map[string]int64
	other  int64
}

const maxDistinct = 200

func newErrorCounter() *errorCounter { return &errorCounter{counts: map[string]int64{}} }

func (e *errorCounter) add(msg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.counts[msg]; ok || len(e.counts) < maxDistinct {
		e.counts[msg]++
		return
	}
	e.other++
}

func (e *errorCounter) top(n int) []ErrorCount {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]ErrorCount, 0, len(e.counts))
	for m, c := range e.counts {
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
	if e.other > 0 {
		out = append(out, ErrorCount{"(other distinct errors)", e.other})
	}
	return out
}

// warnings collects problems with the run itself.
func (t *Test) warnings() []string {
	var out []string
	if w := t.sat.warning("This machine", t.opts.TickInterval); w != "" && t.opts.OnReport == nil {
		out = append(out, w)
	}
	for _, ws := range t.workers {
		ws.mu.Lock()
		if w := ws.sat.warning("Agent "+ws.w.Name(), t.opts.TickInterval); w != "" {
			out = append(out, w)
		}
		ws.mu.Unlock()
	}
	return out
}

// hostAllowed reports whether pyro.http may call host: loopback, or a host
// the test's confirmation covered.
func (t *Test) hostAllowed(host string) bool {
	host = strings.ToLower(host)
	if host == "localhost" || t.allowed[host] {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// InlineScripts replaces scriptFile steps with the files' contents, so the
// rest of the engine (and agents) only see script. read gets a
// workspace-relative path.
func InlineScripts(p *Plan, read func(string) ([]byte, error)) error {
	var walk func([]Step) error
	walk = func(steps []Step) error {
		for i := range steps {
			st := &steps[i]
			if st.ScriptFile != "" {
				b, err := read(st.ScriptFile)
				if err != nil {
					return fmt.Errorf("script file %s: %w", st.ScriptFile, err)
				}
				if _, err := compileScript(string(b)); err != nil {
					return fmt.Errorf("script file %s: %w", st.ScriptFile, err)
				}
				st.Script, st.ScriptFile = string(b), ""
			}
			kids := [][]Step{st.Steps}
			if st.Loop != nil {
				kids = append(kids, st.Loop.Steps)
			}
			if st.If != nil {
				kids = append(kids, st.If.Then, st.If.Else)
			}
			for _, b := range st.Weighted {
				kids = append(kids, b.Steps)
			}
			for _, k := range kids {
				if err := walk(k); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, sc := range p.Scenarios {
		if err := walk(sc.Flow); err != nil {
			return err
		}
	}
	return nil
}
