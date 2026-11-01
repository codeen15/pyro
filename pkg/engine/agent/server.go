package agent

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/codeen15/pyro/pkg/engine/load"
	"github.com/codeen15/pyro/pkg/engine/plugin"
)

// Server is the agent side. It runs one job at a time.
type Server struct {
	// Token must be presented by controllers as a bearer token. Required.
	Token   string
	Name    string
	Version string
	MaxVUs  int
	Plugins *plugin.Manager
	// TickInterval defaults to 1s.
	TickInterval time.Duration
	Logf         func(format string, args ...any)

	mu   sync.Mutex
	busy bool
}

func (s *Server) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// Handler serves /agent/v1/info and /agent/v1/job.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /agent/v1/info", s.auth(s.info))
	mux.HandleFunc("GET /agent/v1/job", s.auth(s.job))
	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if s.Token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) != 1 {
			http.Error(w, "invalid or missing agent token", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) Info() Info {
	s.mu.Lock()
	busy := s.busy
	s.mu.Unlock()
	in := Info{Name: s.Name, Version: s.Version, Protocol: Protocol, CPUs: runtime.NumCPU(),
		OS: runtime.GOOS, Arch: runtime.GOARCH, Busy: busy, MaxVUs: s.maxVUs()}
	if s.Plugins != nil {
		for _, p := range s.Plugins.List() {
			if p.Enabled && p.Approved && p.Error == "" {
				for _, pr := range p.Provides {
					in.Plugins = append(in.Plugins, pr.Kind+":"+pr.ID)
				}
			}
		}
	}
	return in
}

func (s *Server) maxVUs() int {
	if s.MaxVUs > 0 {
		return s.MaxVUs
	}
	return load.DefaultMaxVUs
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.Info())
}

func (s *Server) job(w http.ResponseWriter, r *http.Request) {
	if v := r.Header.Get(protocolHeader); v != strconv.Itoa(Protocol) {
		http.Error(w, fmt.Sprintf("agent protocol %d required, controller speaks %q: use the same Pyro version on both", Protocol, v), http.StatusBadRequest)
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(512 << 20) // jobs carry data rows and body files
	defer c.CloseNow()
	ctx := r.Context()

	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		wsjson.Write(ctx, c, message{Type: "error", Error: "the agent is busy with another test"})
		c.Close(websocket.StatusTryAgainLater, "busy")
		return
	}
	s.busy = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.busy = false
		s.mu.Unlock()
	}()

	if err := s.runJob(ctx, c, r.RemoteAddr, time.Minute); err != nil {
		s.logf("job from %s: %v", r.RemoteAddr, err)
		wsjson.Write(ctx, c, message{Type: "error", Error: err.Error()})
		c.Close(websocket.StatusNormalClosure, "")
		return
	}
	c.Close(websocket.StatusNormalClosure, "")
}

// runJob reads a job from c (waiting up to wait; 0 waits until the
// connection ends), runs it and reports back.
func (s *Server) runJob(ctx context.Context, c *websocket.Conn, from string, wait time.Duration) error {
	var msg message
	rctx, cancel := ctx, context.CancelFunc(func() {})
	if wait > 0 {
		rctx, cancel = context.WithTimeout(ctx, wait)
	}
	err := wsjson.Read(rctx, c, &msg)
	cancel()
	if err != nil {
		return fmt.Errorf("reading job: %w", err)
	}
	if msg.Type != "job" || msg.Job == nil {
		return fmt.Errorf("expected a job, got %q", msg.Type)
	}
	job := *msg.Job
	for _, p := range job.Plugins {
		kind, id, _ := strings.Cut(p, ":")
		if s.Plugins == nil {
			return fmt.Errorf("the test uses plugin type %s, but this agent has no plugins", p)
		}
		if err := s.Plugins.Check(ctx, kind, id); err != nil {
			return fmt.Errorf("the test uses plugin type %s: %w", p, err)
		}
	}

	reports := make(chan load.Report, 256)
	test, err := load.PrepareJob(job, load.Options{
		MaxVUs: s.maxVUs(), Plugins: s.Plugins, TickInterval: s.TickInterval,
	}, func(r load.Report) { reports <- r })
	if err != nil {
		return err
	}
	if err := wsjson.Write(ctx, c, message{Type: "ready"}); err != nil {
		test.Close()
		return err
	}
	rctx, cancel = context.WithTimeout(ctx, 5*time.Minute)
	err = wsjson.Read(rctx, c, &msg)
	cancel()
	if err != nil || msg.Type != "begin" {
		test.Close()
		if err == nil {
			err = fmt.Errorf("expected begin, got %q", msg.Type)
		}
		return err
	}

	s.logf("running segment %d/%d of %q for %s", job.Segment+1, job.Segments, job.Plan.Name, from)
	test.Begin()
	// Read stop requests; a broken connection aborts the job.
	go func() {
		for {
			var m message
			if err := wsjson.Read(ctx, c, &m); err != nil {
				select {
				case <-test.Done():
				default:
					s.logf("controller %s went away: aborting", from)
					test.Abort()
				}
				return
			}
			if m.Type == "stop" {
				test.Stop()
			}
		}
	}()
	send := func(r load.Report) error {
		return wsjson.Write(ctx, c, message{Type: "report", Report: &r})
	}
	for {
		select {
		case r := <-reports:
			if err := send(r); err != nil {
				test.Abort()
				return err
			}
		case <-test.Done():
			for {
				select {
				case r := <-reports:
					if err := send(r); err != nil {
						return err
					}
				default:
					res := test.Wait()
					s.logf("segment done: %s, %.0f requests", res.Status, res.Metrics["http_reqs"].Value)
					return wsjson.Write(ctx, c, message{Type: "done", Status: res.Status})
				}
			}
		}
	}
}
