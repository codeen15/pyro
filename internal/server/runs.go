package server

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/codeen15/pyro/pkg/engine/runner"
)

// maxRuns bounds how many finished runs are kept in memory.
const maxRuns = 20

// eventBodyLimit trims response bodies in live run events; the full result
// is available from GET /runs/{id} once the run ends.
const eventBodyLimit = 64 << 10

type liveRun struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Status string `json:"status"` // running | finished | cancelled
	Total  int    `json:"total"`

	cancel  context.CancelFunc
	mu      sync.Mutex
	done    int
	results []runner.ItemResult // trimmed, for clients that subscribe late
	sum     *runner.Summary
}

type runRegistry struct {
	mu    sync.Mutex
	byID  map[string]*liveRun
	order []string
}

type runBody struct {
	Path        string              `json:"path"`
	Environment string              `json:"environment,omitempty"`
	Iterations  int                 `json:"iterations,omitempty"`
	Data        []map[string]string `json:"data,omitempty"`
	DelayMS     int                 `json:"delayMs,omitempty"`
	Bail        bool                `json:"bail,omitempty"`
}

func (s *Server) startRun(w http.ResponseWriter, r *http.Request) {
	var body runBody
	if !decode(w, r, &body) {
		return
	}
	refs, err := s.ws.Requests(body.Path)
	if err != nil {
		s.fail(w, err)
		return
	}
	opts := runner.Options{Iterations: body.Iterations, Data: body.Data, Bail: body.Bail, Globals: s.ws.GlobalVars()}
	opts.Delay = msDuration(body.DelayMS)
	if body.Environment != "" {
		env, err := s.ws.ReadEnvironment(body.Environment)
		if err != nil {
			s.fail(w, err)
			return
		}
		env.Name = body.Environment
		opts.Environment = env
	}
	items := make([]runner.Item, len(refs))
	for i, ref := range refs {
		items[i] = runner.Item{Path: ref.Path, Request: ref.Request, Chain: ref.Chain}
	}
	iterations := body.Iterations
	if iterations <= 0 {
		iterations = max(len(body.Data), 1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	run := &liveRun{ID: newID(), Path: body.Path, Status: "running", Total: len(items) * iterations, cancel: cancel}
	topic := "run:" + run.ID
	opts.OnResult = func(ir runner.ItemResult) {
		t := trimmed(ir)
		run.mu.Lock()
		run.done++
		done := run.done
		run.results = append(run.results, t)
		run.mu.Unlock()
		s.hub.publish(topic, "result", map[string]any{"done": done, "total": run.Total, "item": t})
	}
	s.runs.mu.Lock()
	s.runs.byID[run.ID] = run
	s.runs.order = append(s.runs.order, run.ID)
	for len(s.runs.order) > maxRuns {
		delete(s.runs.byID, s.runs.order[0])
		s.runs.order = s.runs.order[1:]
	}
	s.runs.mu.Unlock()

	go func() {
		sum := runner.Run(ctx, s.cfg.Engine, items, opts)
		run.mu.Lock()
		run.sum = sum
		run.Status = "finished"
		if sum.Stopped == "cancelled" {
			run.Status = "cancelled"
		}
		run.mu.Unlock()
		cancel()
		s.hub.publish(topic, "finished", s.runView(run))
	}()
	writeJSON(w, http.StatusAccepted, s.runView(run))
}

// trimmed copies an item result with a capped response body.
func trimmed(ir runner.ItemResult) runner.ItemResult {
	if ir.Result == nil || ir.Result.Response == nil || len(ir.Result.Response.Body) <= eventBodyLimit {
		return ir
	}
	res := *ir.Result
	resp := *res.Response
	resp.Body = resp.Body[:eventBodyLimit]
	resp.BodyTruncated = true
	res.Response = &resp
	ir.Result = &res
	return ir
}

type runView struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Status string `json:"status"`
	Done   int    `json:"done"`
	Total  int    `json:"total"`
	// Results so far (bodies trimmed); Summary is set once the run ends.
	Results []runner.ItemResult `json:"results"`
	Summary *runSummary         `json:"summary,omitempty"`
}

// runSummary is the summary without its per-item results, which Results carries.
type runSummary struct {
	Iterations  int     `json:"iterations"`
	Requests    int     `json:"requests"`
	Failed      int     `json:"failed"`
	TestsPassed int     `json:"testsPassed"`
	TestsFailed int     `json:"testsFailed"`
	DurationMS  float64 `json:"durationMs"`
	Stopped     string  `json:"stopped,omitempty"`
}

func (s *Server) runView(run *liveRun) runView {
	run.mu.Lock()
	defer run.mu.Unlock()
	v := runView{ID: run.ID, Path: run.Path, Status: run.Status, Done: run.done, Total: run.Total,
		Results: append([]runner.ItemResult{}, run.results...)}
	if sm := run.sum; sm != nil {
		v.Summary = &runSummary{sm.Iterations, sm.Requests, sm.Failed, sm.TestsPassed, sm.TestsFailed,
			float64(sm.Duration.Microseconds()) / 1000, sm.Stopped}
	}
	return v
}

func (s *Server) lookupRun(w http.ResponseWriter, r *http.Request) *liveRun {
	s.runs.mu.Lock()
	run := s.runs.byID[chi.URLParam(r, "id")]
	s.runs.mu.Unlock()
	if run == nil {
		writeError(w, http.StatusNotFound, "run not found")
	}
	return run
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	if run := s.lookupRun(w, r); run != nil {
		writeJSON(w, http.StatusOK, s.runView(run))
	}
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) {
	if run := s.lookupRun(w, r); run != nil {
		run.cancel()
		w.WriteHeader(http.StatusNoContent)
	}
}

func msDuration(ms int) time.Duration { return time.Duration(ms) * time.Millisecond }
