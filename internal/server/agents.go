package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/codeen15/pyro/pkg/engine/agent"
	"github.com/codeen15/pyro/pkg/engine/load"
)

// AgentStatus is one load agent as the project sees it.
type AgentStatus struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Mode    string `json:"mode"` // "direct" (the server dials it) | "connect" (it connected out to the server)
	Address string `json:"address"`
	Status  string `json:"status"` // available | busy | offline | error
	Detail  string `json:"detail"` // why it is offline or in error
	Version string `json:"version"`
	CPUs    int    `json:"cpus"`
	MaxVUs  int    `json:"maxVUs"`
	// Encrypted is false for plain http:// and ws:// addresses.
	Encrypted bool `json:"encrypted"`
	// FromFlags marks agents given on the command line (read-only).
	FromFlags bool  `json:"fromFlags"`
	LastSeen  int64 `json:"lastSeen"` // unix seconds, 0 = never
}

// AgentSource supplies the load agents a project can run distributed tests
// on: the command-line flags locally, the team server's registry in team
// mode.
type AgentSource interface {
	List(ctx context.Context) []AgentStatus
	// Claim takes the agents with these ids as workers for one test (nil or
	// empty ids: every available agent). If any is unavailable it claims
	// nothing and returns an *UnavailableError.
	Claim(ctx context.Context, ids []string) ([]load.Worker, error)
	// Release hands back claimed workers that a test never started on.
	Release(workers []load.Worker)
	// Subscribe calls f (which must not block) when agents change.
	Subscribe(f func()) (cancel func())
}

// UnavailableError says which agents can't take a test, and why (by id).
type UnavailableError struct{ Agents map[string]string }

func (e *UnavailableError) Error() string {
	ids := make([]string, 0, len(e.Agents))
	for id := range e.Agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = id + " (" + e.Agents[id] + ")"
	}
	return "agents not available: " + strings.Join(parts, ", ")
}

const (
	flagPrefix = "flag:"
	hubPrefix  = "hub:"
)

// localAgents is pyro serve's AgentSource: --agent addresses (ids
// "flag:<address>") and agents connected to --agent-listen (ids
// "hub:<name>@<remote address>").
type localAgents struct {
	addrs    []string
	token    string
	insecure bool
	hub      atomic.Pointer[agent.Hub]

	mu   sync.Mutex
	subs map[int]func()
	next int
}

func newLocalAgents(addrs []string, token string, insecure bool) *localAgents {
	return &localAgents{addrs: addrs, token: token, insecure: insecure, subs: map[int]func(){}}
}

// setHub attaches the hub; call it before the hub serves, since it sets
// the hub's OnChange.
func (l *localAgents) setHub(h *agent.Hub) {
	if h == nil {
		return
	}
	prev := h.OnChange
	h.OnChange = func() {
		if prev != nil {
			prev()
		}
		l.changed()
	}
	l.hub.Store(h)
	l.changed()
}

func (l *localAgents) changed() {
	l.mu.Lock()
	fs := make([]func(), 0, len(l.subs))
	for _, f := range l.subs {
		fs = append(fs, f)
	}
	l.mu.Unlock()
	for _, f := range fs {
		f()
	}
}

func (l *localAgents) Subscribe(f func()) func() {
	l.mu.Lock()
	defer l.mu.Unlock()
	id := l.next
	l.next++
	l.subs[id] = f
	return func() {
		l.mu.Lock()
		delete(l.subs, id)
		l.mu.Unlock()
	}
}

func (l *localAgents) client(addr string) *agent.Client {
	return agent.NewClient(addr, l.token, l.insecure)
}

// probe asks a direct agent for its status (5 s at most).
func (l *localAgents) probe(ctx context.Context, addr string) AgentStatus {
	st := AgentStatus{
		ID: flagPrefix + addr, Name: addr, Mode: "direct", Address: addr, FromFlags: true,
		Encrypted: strings.HasPrefix(addr, "https://") || strings.HasPrefix(addr, "wss://"),
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	in, err := l.client(addr).Info(ctx)
	if err != nil {
		st.Status, st.Detail = "error", err.Error()
		if _, transport := errors.AsType[*url.Error](err); transport {
			st.Status = "offline"
		}
		return st
	}
	st.Status = "available"
	if in.Busy {
		st.Status = "busy"
	}
	if in.Name != "" {
		st.Name = in.Name
	}
	st.Version, st.CPUs, st.MaxVUs, st.LastSeen = in.Version, in.CPUs, in.MaxVUs, time.Now().Unix()
	return st
}

func (l *localAgents) List(ctx context.Context) []AgentStatus {
	out := make([]AgentStatus, len(l.addrs))
	var wg sync.WaitGroup
	for i, a := range l.addrs {
		wg.Go(func() { out[i] = l.probe(ctx, a) })
	}
	wg.Wait()
	if h := l.hub.Load(); h != nil {
		now := time.Now().Unix()
		conns := h.ConnectedIDs()
		ids := make([]string, 0, len(conns))
		for id := range conns {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			in := conns[id]
			st := AgentStatus{
				ID: hubPrefix + id, Name: in.Name, Mode: "connect", Status: "available",
				Version: in.Version, CPUs: in.CPUs, MaxVUs: in.MaxVUs, FromFlags: true, LastSeen: now,
			}
			if _, addr, ok := strings.Cut(id, "@"); ok {
				st.Address = addr
			}
			if in.Busy {
				st.Status = "busy"
			}
			out = append(out, st)
		}
	}
	return out
}

func (l *localAgents) Claim(ctx context.Context, ids []string) ([]load.Worker, error) {
	h := l.hub.Load()
	if len(ids) == 0 {
		var workers []load.Worker
		for _, a := range l.addrs {
			workers = append(workers, l.client(a))
		}
		if h != nil {
			workers = append(workers, h.Claim(0)...)
		}
		return workers, nil
	}
	known := map[string]bool{}
	for _, a := range l.addrs {
		known[a] = true
	}
	var direct, hubIDs []string
	unavailable := map[string]string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		switch {
		case strings.HasPrefix(id, flagPrefix) && known[id[len(flagPrefix):]]:
			direct = append(direct, id[len(flagPrefix):])
		case strings.HasPrefix(id, hubPrefix) && h != nil:
			hubIDs = append(hubIDs, id[len(hubPrefix):])
		default:
			unavailable[id] = "unknown agent"
		}
	}
	// Direct agents are checked first: they hold no claim, so nothing needs
	// handing back if one of them is unavailable.
	states := make([]AgentStatus, len(direct))
	var wg sync.WaitGroup
	for i, a := range direct {
		wg.Go(func() { states[i] = l.probe(ctx, a) })
	}
	wg.Wait()
	for _, st := range states {
		if st.Status != "available" {
			unavailable[st.ID] = st.Status
		}
	}
	if len(unavailable) > 0 {
		return nil, &UnavailableError{Agents: unavailable}
	}
	var workers []load.Worker
	for _, a := range direct {
		workers = append(workers, l.client(a))
	}
	if len(hubIDs) > 0 {
		hw, why := h.ClaimIDs(hubIDs)
		if why != nil {
			for id, reason := range why {
				unavailable[hubPrefix+id] = reason
			}
			return nil, &UnavailableError{Agents: unavailable}
		}
		workers = append(workers, hw...)
	}
	return workers, nil
}

func (l *localAgents) Release(workers []load.Worker) {
	if h := l.hub.Load(); h != nil {
		h.Release(workers)
	}
}

// listAgents reports each agent's status.
func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	out := s.agents.List(r.Context())
	if out == nil {
		out = []AgentStatus{}
	}
	writeJSON(w, http.StatusOK, out)
}

// noAgentsMessage explains how to add agents.
func (s *Server) noAgentsMessage() string {
	if s.cfg.Team {
		return "no agents available: an admin can add agents in the Agents tab"
	}
	return "no agents: start pyro serve with --agent, or --agent-listen and connect agents to it"
}

// claimAgents takes the workers for a distributed run; on failure it has
// written the response.
func (s *Server) claimAgents(w http.ResponseWriter, ctx context.Context, ids []string) ([]load.Worker, bool) {
	workers, err := s.agents.Claim(ctx, ids)
	if err != nil {
		if _, ok := errors.AsType[*UnavailableError](err); ok {
			writeError(w, http.StatusConflict, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("claiming agents: %v", err))
		}
		return nil, false
	}
	if len(workers) == 0 {
		writeError(w, http.StatusBadRequest, s.noAgentsMessage())
		return nil, false
	}
	return workers, true
}
