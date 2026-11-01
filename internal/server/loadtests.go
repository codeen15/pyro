package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/codeen15/pyro/internal/history"
	"github.com/codeen15/pyro/internal/store"
	"github.com/codeen15/pyro/pkg/engine/agent"
	"github.com/codeen15/pyro/pkg/engine/load"
	"github.com/codeen15/pyro/pkg/engine/metrics"
	"github.com/codeen15/pyro/pkg/engine/output"
	"github.com/codeen15/pyro/pkg/engine/report"
)

type liveLoad struct {
	id   string
	name string // load test name (file)
	test *load.Test
}

type loadRegistry struct {
	mu      sync.Mutex
	current *liveLoad // one load test at a time per server
	byID    map[string]*liveLoad
}

type loadTestView struct {
	Name    string     `json:"name"`
	Title   string     `json:"title,omitempty"`
	YAML    string     `json:"yaml,omitempty"`
	Plan    *load.Plan `json:"plan,omitempty"`
	PeakVUs int        `json:"peakVUs,omitempty"`
	Error   string     `json:"error,omitempty"`
}

func (s *Server) viewLoadTest(name string, withYAML bool) (loadTestView, error) {
	data, err := s.ws.ReadLoadTest(name)
	if err != nil {
		return loadTestView{}, err
	}
	v := loadTestView{Name: name}
	if withYAML {
		v.YAML = string(data)
	}
	p, err := load.ParsePlan(data, s.ws.RequestExists)
	if err != nil {
		v.Error = err.Error()
		return v, nil
	}
	v.Title, v.PeakVUs = p.Name, load.PeakVUs(p)
	if withYAML {
		v.Plan = p
	}
	return v, nil
}

func (s *Server) listLoadTests(w http.ResponseWriter, r *http.Request) {
	names, err := s.ws.ListLoadTests()
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]loadTestView, 0, len(names))
	for _, n := range names {
		v, err := s.viewLoadTest(n, false)
		if err != nil {
			s.fail(w, err)
			return
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getLoadTest(w http.ResponseWriter, r *http.Request) {
	v, err := s.viewLoadTest(chi.URLParam(r, "name"), true)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) putLoadTest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		YAML string `json:"yaml"`
	}
	if !decode(w, r, &body) {
		return
	}
	name := chi.URLParam(r, "name")
	if err := s.ws.WriteLoadTest(name, []byte(body.YAML)); err != nil {
		s.fail(w, err)
		return
	}
	v, _ := s.viewLoadTest(name, true)
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) deleteLoadTest(w http.ResponseWriter, r *http.Request) {
	if err := s.ws.DeleteLoadTest(chi.URLParam(r, "name")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type liveSnapshot struct {
	Tick     load.Tick             `json:"tick"`
	Requests []metrics.RequestStat `json:"requests"`
	Errors   []load.ErrorCount     `json:"errors"`
}

func (s *Server) startLoadTest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Environment  string `json:"environment,omitempty"`
		ConfirmHosts bool   `json:"confirmHosts,omitempty"`
		// Distributed runs the test on the configured agents.
		Distributed bool `json:"distributed,omitempty"`
	}
	if !decode(w, r, &body) {
		return
	}
	name := chi.URLParam(r, "name")
	plan, err := s.ws.LoadPlan(name)
	if err != nil {
		s.fail(w, err)
		return
	}
	if plan.Name == "" {
		plan.Name = name
	}
	envName := body.Environment
	if envName == "" {
		envName = plan.Environment
	}
	opts := load.Options{Resolve: s.ws.Target, LoadData: s.ws.LoadData, FileRoot: s.ws.Root, AllowFilesOutsideRoot: s.ws.Meta.AllowFilesOutsideWorkspace, Globals: s.ws.GlobalVars(), MaxRPS: s.ws.Meta.LoadMaxRPS}
	if s.cfg.Engine != nil {
		opts.Plugins = s.cfg.Engine.Plugins
	}
	if body.Distributed {
		if len(s.cfg.Agents) == 0 {
			writeError(w, http.StatusBadRequest, "no agents are configured: start pyro serve with --agent")
			return
		}
		for _, a := range s.cfg.Agents {
			opts.Workers = append(opts.Workers, agent.NewClient(a, s.cfg.AgentToken, s.cfg.AgentInsecure))
		}
	}
	if envName != "" {
		env, err := s.ws.ReadEnvironment(envName)
		if err != nil {
			s.fail(w, err)
			return
		}
		env.Name = envName
		opts.Environment = env
	}
	if hosts := s.ws.UnconfirmedHosts(load.Hosts(plan, s.ws.Target, opts.Environment, opts.Globals)); len(hosts) > 0 && !body.ConfirmHosts {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "confirm the hosts this test will send traffic to", "needsConfirmation": true, "hosts": hosts,
		})
		return
	}

	s.loads.mu.Lock()
	if cur := s.loads.current; cur != nil {
		s.loads.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]any{"error": "a load test is already running", "running": cur.id})
		return
	}
	id := newID()
	topic := "load:" + id
	sinks, err := output.OpenAll(s.cfg.Outputs, plan.Name, id)
	if err != nil {
		s.loads.mu.Unlock()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The first tick can fire before Start returns, so publish through an
	// atomic pointer rather than the variable being assigned.
	var started atomic.Pointer[load.Test]
	opts.OnTick = func(t load.Tick) {
		if lt := started.Load(); lt != nil {
			reqs, errs := lt.Snapshot()
			s.hub.publish(topic, "tick", liveSnapshot{t, reqs, errs})
			sinks.Tick(t, reqs)
		}
	}
	test, err := load.Start(plan, opts)
	if err != nil {
		sinks.Close()
		s.loads.mu.Unlock()
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	started.Store(test)
	ll := &liveLoad{id: id, name: name, test: test}
	s.loads.current = ll
	s.loads.byID[id] = ll
	s.loads.mu.Unlock()

	go func() {
		res := test.Wait()
		if err := sinks.Close(); err != nil {
			s.log.Warn("load test outputs", "err", err)
		}
		if s.cfg.History != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := s.cfg.History.AddLoadRun(ctx, s.ws.Root, id, name, res); err != nil {
				s.log.Warn("saving load run failed", "err", err)
			}
			cancel()
		}
		s.loads.mu.Lock()
		if s.loads.current == ll {
			s.loads.current = nil
		}
		s.loads.mu.Unlock()
		s.hub.publish(topic, "finished", loadRunView{ID: id, Test: name, Status: res.Status, Result: res})
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "test": name})
}

type loadRunView struct {
	ID     string        `json:"id"`
	Test   string        `json:"test"`
	Status string        `json:"status"` // running | passed | failed | aborted | stopped
	Ticks  []load.Tick   `json:"ticks,omitempty"`
	Live   *liveSnapshot `json:"live,omitempty"`
	Result *load.Result  `json:"result,omitempty"`
	Plan   *load.Plan    `json:"plan,omitempty"`
}

func (s *Server) getLoadRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s.loads.mu.Lock()
	ll := s.loads.byID[id]
	s.loads.mu.Unlock()
	if ll != nil {
		select {
		case <-ll.test.Done():
			res := ll.test.Wait()
			writeJSON(w, http.StatusOK, loadRunView{ID: id, Test: ll.name, Status: res.Status, Result: res})
		default:
			ticks := ll.test.Ticks()
			reqs, errs := ll.test.Snapshot()
			v := loadRunView{ID: id, Test: ll.name, Status: "running", Ticks: ticks, Plan: ll.test.Plan()}
			if len(ticks) > 0 {
				v.Live = &liveSnapshot{ticks[len(ticks)-1], reqs, errs}
			}
			writeJSON(w, http.StatusOK, v)
		}
		return
	}
	if s.cfg.History == nil {
		writeError(w, http.StatusNotFound, "load run not found")
		return
	}
	res, test, err := s.cfg.History.LoadRunResult(r.Context(), s.ws.Root, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, loadRunView{ID: id, Test: test, Status: res.Status, Result: res})
}

func (s *Server) listLoadRuns(w http.ResponseWriter, r *http.Request) {
	out := []history.LoadRun{}
	if s.cfg.History != nil {
		var err error
		if out, err = s.cfg.History.LoadRuns(r.Context(), s.ws.Root, r.URL.Query().Get("test"), 50); err != nil {
			s.fail(w, err)
			return
		}
	}
	s.loads.mu.Lock()
	cur := s.loads.current
	s.loads.mu.Unlock()
	if cur != nil && (r.URL.Query().Get("test") == "" || r.URL.Query().Get("test") == cur.name) {
		out = append([]history.LoadRun{{ID: cur.id, Test: cur.name, Name: cur.test.Plan().Name, Status: "running"}}, out...)
	}
	writeJSON(w, http.StatusOK, out)
}

// stopLoadRun stops gracefully; ?abort=1 cancels in-flight requests too.
func (s *Server) stopLoadRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s.loads.mu.Lock()
	ll := s.loads.byID[id]
	s.loads.mu.Unlock()
	if ll == nil {
		writeError(w, http.StatusNotFound, "no running load test with that id")
		return
	}
	if r.URL.Query().Get("abort") == "1" {
		ll.test.Abort()
	} else {
		ll.test.Stop()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteLoadRun(w http.ResponseWriter, r *http.Request) {
	if s.cfg.History != nil {
		if err := s.cfg.History.DeleteLoadRun(r.Context(), s.ws.Root, chi.URLParam(r, "id")); err != nil {
			s.fail(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// loadRunReport serves the HTML report of a finished run.
func (s *Server) loadRunReport(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var res *load.Result
	s.loads.mu.Lock()
	ll := s.loads.byID[id]
	s.loads.mu.Unlock()
	if ll != nil {
		select {
		case <-ll.test.Done():
			res = ll.test.Wait()
		default:
			writeError(w, http.StatusConflict, "the load test is still running")
			return
		}
	} else if s.cfg.History != nil {
		var err error
		if res, _, err = s.cfg.History.LoadRunResult(r.Context(), s.ws.Root, id); err != nil {
			s.fail(w, err)
			return
		}
	} else {
		writeError(w, http.StatusNotFound, "load run not found")
		return
	}
	b, err := report.LoadHTML(res)
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="pyro-load-report.html"`)
	}
	_, _ = io.WriteString(w, string(b))
}

func isValidation(err error) bool {
	var ve *store.ValidationError
	return errors.As(err, &ve)
}

type agentView struct {
	Address string      `json:"address"`
	Info    *agent.Info `json:"info,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// listAgents reports each configured agent's status.
func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	out := make([]agentView, len(s.cfg.Agents))
	var wg sync.WaitGroup
	for i, a := range s.cfg.Agents {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			out[i].Address = a
			in, err := agent.NewClient(a, s.cfg.AgentToken, s.cfg.AgentInsecure).Info(ctx)
			if err != nil {
				out[i].Error = err.Error()
				return
			}
			out[i].Info = in
		}()
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, out)
}
