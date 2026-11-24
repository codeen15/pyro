package team

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/codeen15/pyro/internal/server"
	"github.com/codeen15/pyro/pkg/engine/agent"
	"github.com/codeen15/pyro/pkg/engine/load"
)

const (
	// probeEvery is how often the registry checks direct agents.
	probeEvery = 15 * time.Second
	// After connectMaxFails failed dial-in attempts from one address within
	// connectWindow, that address is refused for connectWindow.
	connectMaxFails = 5
	connectWindow   = time.Minute
	flagAgentPrefix = "flag:"
)

// agentRegistry is the team server's server.AgentSource: agents registered
// in the database (direct ones it dials, connecting ones held by its hub)
// plus the read-only --agent flags. It keeps the last status of each direct
// agent, refreshed by a background prober.
type agentRegistry struct {
	st          *Store
	log         *slog.Logger
	flags       []string
	flagToken   string
	insecure    bool
	publicHTTPS bool // the server's public address is https (TLS may end at a proxy)

	hub        *agent.Hub
	hubHandler http.Handler
	limit      *connectLimiter

	mu     sync.Mutex
	cache  map[string]server.AgentStatus // last probe of each direct agent, by id
	names  map[string]string             // display name by id
	secure map[string]bool               // connecting agents: whether they came in over TLS
	checks map[string]bool               // background checks in flight, by id
	subs   map[int]func()
	next   int
	closed bool

	// ctx ends when the registry closes; background probes use it.
	ctx    context.Context
	cancel context.CancelFunc

	notify    chan struct{}
	stop      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

var _ server.AgentSource = (*agentRegistry)(nil)

func newAgentRegistry(st *Store, flagAgents []string, flagToken string, insecure bool, log *slog.Logger) *agentRegistry {
	if log == nil {
		log = slog.Default()
	}
	r := &agentRegistry{
		st: st, log: log, flags: flagAgents, flagToken: flagToken, insecure: insecure,
		limit: newConnectLimiter(connectMaxFails, connectWindow),
		cache: map[string]server.AgentStatus{}, names: map[string]string{}, secure: map[string]bool{},
		checks: map[string]bool{},
		subs:   map[int]func(){}, notify: make(chan struct{}, 1), stop: make(chan struct{}),
	}
	r.ctx, r.cancel = context.WithCancel(context.Background())
	r.hub = agent.NewHub("")
	r.hub.Authenticate = r.authenticate
	r.hub.NameFor = r.nameFor
	r.hub.OnChange = r.changed
	r.hubHandler = r.hub.Handler()
	r.goBackground(r.publish)
	return r
}

// Start runs the prober: every direct agent now, then every 15 s.
func (r *agentRegistry) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(r.ctx, cancel)
	r.goBackground(func() {
		defer stop()
		defer cancel()
		t := time.NewTicker(probeEvery)
		defer t.Stop()
		for {
			r.probeAll(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	})
}

// goBackground runs f unless the registry has closed (Close waits for it).
func (r *agentRegistry) goBackground(f func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.wg.Go(f)
}

// Close stops the prober and drops every connected agent (they reconnect
// to the next server by themselves).
func (r *agentRegistry) Close() {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.mu.Unlock()
		close(r.stop)
		r.cancel()
		for id := range r.hub.ConnectedIDs() {
			r.hub.Disconnect(id)
		}
		r.wg.Wait()
	})
}

// changed tells subscribers, without blocking the caller (the hub calls it
// from its own goroutines; changes coalesce).
func (r *agentRegistry) changed() {
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

func (r *agentRegistry) publish() {
	for {
		select {
		case <-r.stop:
			return
		case <-r.notify:
		}
		r.mu.Lock()
		fs := make([]func(), 0, len(r.subs))
		for _, f := range r.subs {
			fs = append(fs, f)
		}
		r.mu.Unlock()
		for _, f := range fs {
			f()
		}
	}
}

func (r *agentRegistry) Subscribe(f func()) func() {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := r.next
	r.next++
	r.subs[id] = f
	return func() {
		r.mu.Lock()
		delete(r.subs, id)
		r.mu.Unlock()
	}
}

func (r *agentRegistry) nameFor(id string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.names[id]
}

// authenticate is the hub's check of a connecting agent's token.
func (r *agentRegistry) authenticate(token string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, ok, err := r.st.AgentByToken(ctx, token)
	if err != nil {
		r.log.Warn("checking an agent token failed", "err", err)
		return "", false
	}
	if !ok {
		return "", false
	}
	if err := r.st.TouchAgent(ctx, a.ID, time.Now()); err != nil {
		r.log.Warn("recording agent connection failed", "agent", a.Name, "err", err)
	}
	r.mu.Lock()
	r.names[a.ID] = a.Name
	r.mu.Unlock()
	return a.ID, true
}

// serveConnect is GET /agent/v1/connect: agents that connect out to the
// server. Addresses that keep presenting wrong tokens are refused a while.
func (r *agentRegistry) serveConnect(w http.ResponseWriter, req *http.Request) {
	ip := clientIP(req)
	if r.limit.blocked(ip, time.Now()) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many failed agent connections from this address; try again in a minute", http.StatusTooManyRequests)
		return
	}
	token, _ := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	a, ok, err := r.st.AgentByToken(req.Context(), token)
	if err != nil {
		r.log.Warn("checking an agent token failed", "err", err)
		http.Error(w, "the server could not check the agent token", http.StatusInternalServerError)
		return
	}
	if !ok {
		r.limit.fail(ip, time.Now())
		http.Error(w, "invalid or missing agent token", http.StatusUnauthorized)
		return
	}
	r.limit.reset(ip)
	r.mu.Lock()
	r.secure[a.ID] = req.TLS != nil
	r.mu.Unlock()
	// The hub checks the token again (and records the connection).
	r.hubHandler.ServeHTTP(w, req)
}

// Forget drops a removed agent (or one whose token was rotated): its
// connections close, which ends any run using it, and its status goes.
func (r *agentRegistry) Forget(id string) {
	r.hub.Disconnect(id)
	r.mu.Lock()
	delete(r.cache, id)
	delete(r.secure, id)
	r.mu.Unlock()
	r.changed()
}

func (r *agentRegistry) isFlag(id string) (string, bool) {
	addr, ok := strings.CutPrefix(id, flagAgentPrefix)
	if !ok {
		return "", false
	}
	for _, a := range r.flags {
		if a == addr {
			return addr, true
		}
	}
	return "", false
}

// target is a direct agent ready to be probed or run on.
type target struct {
	base   server.AgentStatus
	client *agent.Client
	err    error // why it can't be reached at all (e.g. an unreadable token)
	flag   bool
}

func flagBase(addr string) server.AgentStatus {
	return server.AgentStatus{ID: flagAgentPrefix + addr, Name: addr, Mode: AgentDirect, Address: addr,
		Encrypted: server.EncryptedAddress(addr), FromFlags: true}
}

func (r *agentRegistry) flagTarget(addr string) target {
	return target{base: flagBase(addr), client: agent.NewClient(addr, r.flagToken, r.insecure), flag: true}
}

func directBase(a Agent) server.AgentStatus {
	st := server.AgentStatus{ID: a.ID, Name: a.Name, Mode: a.Mode, Address: a.Address, Encrypted: server.EncryptedAddress(a.Address)}
	if !a.LastSeen.IsZero() {
		st.LastSeen = a.LastSeen.Unix()
	}
	return st
}

func (r *agentRegistry) directTarget(ctx context.Context, a Agent) target {
	t := target{base: directBase(a)}
	token, err := r.st.AgentToken(ctx, a.ID)
	if err != nil {
		t.err = err
		return t
	}
	t.client = agent.NewClient(a.Address, token, a.InsecureTLS)
	return t
}

// probe checks a direct agent and caches the result.
func (r *agentRegistry) probe(ctx context.Context, t target) server.AgentStatus {
	var st server.AgentStatus
	if t.err != nil {
		st = t.base
		st.Status, st.Detail = "error", t.err.Error()
	} else {
		var name string
		st, name = server.ProbeAgent(ctx, t.client, t.base)
		if t.flag && name != "" {
			st.Name = name
		}
		if st.Status != "offline" && st.Status != "error" && !t.flag {
			if err := r.st.TouchAgent(ctx, st.ID, time.Now()); err != nil {
				r.log.Warn("recording agent status failed", "agent", st.Name, "err", err)
			}
		}
	}
	if st.LastSeen < t.base.LastSeen {
		st.LastSeen = t.base.LastSeen
	}
	r.store(st)
	return st
}

// store caches a direct agent's status and tells subscribers if it changed.
func (r *agentRegistry) store(st server.AgentStatus) {
	r.mu.Lock()
	old, had := r.cache[st.ID]
	r.cache[st.ID] = st
	r.names[st.ID] = st.Name
	r.mu.Unlock()
	if !had || old.Status != st.Status || old.Detail != st.Detail || old.Name != st.Name ||
		old.Version != st.Version || old.CPUs != st.CPUs || old.MaxVUs != st.MaxVUs {
		r.changed()
	}
}

// connectStatus is a connecting agent's status from the hub.
func (r *agentRegistry) connectStatus(a Agent, conns map[string]agent.Info) server.AgentStatus {
	st := directBase(a)
	st.Encrypted = r.publicHTTPS || r.secure[a.ID]
	in, ok := conns[a.ID]
	if !ok {
		st.Status, st.Detail = "offline", "not connected"
		return st
	}
	st.Status = "available"
	if in.Busy {
		st.Status = "busy"
	}
	st.Version, st.CPUs, st.MaxVUs, st.LastSeen = in.Version, in.CPUs, in.MaxVUs, time.Now().Unix()
	return st
}

// Probe checks one agent now: a direct agent is asked for its status, a
// connecting one is looked up in the hub.
func (r *agentRegistry) Probe(ctx context.Context, id string) server.AgentStatus {
	if addr, ok := r.isFlag(id); ok {
		return r.probe(ctx, r.flagTarget(addr))
	}
	row, err := r.st.loadAgent(ctx, r.st.db, id)
	if err != nil {
		st := server.AgentStatus{ID: id, Name: id, Status: "error", Detail: "unknown agent"}
		if !errors.Is(err, ErrNotFound) {
			st.Detail = err.Error()
		}
		return st
	}
	a := row.agent()
	r.mu.Lock()
	r.names[a.ID] = a.Name
	r.mu.Unlock()
	if a.Mode == AgentConnect {
		conns := r.hub.ConnectedIDs()
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.connectStatus(a, conns)
	}
	return r.probe(ctx, r.directTarget(ctx, a))
}

// probeAll refreshes every direct agent's status.
func (r *agentRegistry) probeAll(ctx context.Context) {
	agents, err := r.st.Agents(ctx)
	if err != nil {
		if ctx.Err() == nil {
			r.log.Warn("listing agents failed", "err", err)
		}
		return
	}
	keep := map[string]bool{}
	var targets []target
	for _, a := range agents {
		if a.Mode == AgentDirect {
			keep[a.ID] = true
			targets = append(targets, r.directTarget(ctx, a))
		}
	}
	for _, addr := range r.flags {
		keep[flagAgentPrefix+addr] = true
		targets = append(targets, r.flagTarget(addr))
	}
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Go(func() { r.probe(ctx, t) })
	}
	wg.Wait()
	r.mu.Lock()
	for id := range r.cache {
		if !keep[id] {
			delete(r.cache, id)
		}
	}
	r.mu.Unlock()
}

// reprobe refreshes one direct agent in the background (after a run on
// it ends).
func (r *agentRegistry) reprobe(id string) {
	r.mu.Lock()
	if r.checks[id] {
		r.mu.Unlock()
		return
	}
	r.checks[id] = true
	r.mu.Unlock()
	r.goBackground(func() {
		defer func() {
			r.mu.Lock()
			delete(r.checks, id)
			r.mu.Unlock()
		}()
		r.Probe(r.ctx, id)
	})
}

func (r *agentRegistry) List(ctx context.Context) []server.AgentStatus {
	agents, err := r.st.Agents(ctx)
	if err != nil {
		r.log.Warn("listing agents failed", "err", err)
	}
	conns := r.hub.ConnectedIDs()
	var unchecked []string
	out := make([]server.AgentStatus, 0, len(agents)+len(r.flags))
	r.mu.Lock()
	for _, a := range agents {
		r.names[a.ID] = a.Name
		if a.Mode == AgentConnect {
			out = append(out, r.connectStatus(a, conns))
			continue
		}
		st := directBase(a)
		if c, ok := r.cache[a.ID]; ok {
			st.Status, st.Detail, st.Version, st.CPUs, st.MaxVUs = c.Status, c.Detail, c.Version, c.CPUs, c.MaxVUs
			st.LastSeen = max(st.LastSeen, c.LastSeen)
		} else {
			st.Status, st.Detail = "offline", "not checked yet"
			unchecked = append(unchecked, a.ID)
		}
		out = append(out, st)
	}
	for _, addr := range r.flags {
		id := flagAgentPrefix + addr
		if c, ok := r.cache[id]; ok {
			out = append(out, c)
			continue
		}
		st := flagBase(addr)
		st.Status, st.Detail = "offline", "not checked yet"
		unchecked = append(unchecked, id)
		out = append(out, st)
	}
	r.mu.Unlock()
	for _, id := range unchecked {
		r.reprobe(id)
	}
	return out
}

// unavailableReason says why a probed agent can't take a test.
func unavailableReason(st server.AgentStatus) string {
	if st.Status == "error" && st.Detail != "" {
		return "error: " + st.Detail
	}
	return st.Status
}

// directWorker runs a test on a direct agent under its registry name and
// rechecks the agent when it's done.
type directWorker struct {
	load.Worker
	name string
	done func()
	once sync.Once
}

func (w *directWorker) Name() string { return w.name }

func (w *directWorker) Run(ctx context.Context, report func(load.Report)) error {
	defer w.finish()
	return w.Worker.Run(ctx, report)
}

func (w *directWorker) Close() {
	w.Worker.Close()
	w.finish()
}

func (w *directWorker) finish() { w.once.Do(w.done) }

// claimDirect probes direct agents and returns workers for the available
// ones, marking them busy until they finish; the rest are returned by id
// with the reason.
func (r *agentRegistry) claimDirect(ctx context.Context, targets []target) ([]load.Worker, map[string]string) {
	states := make([]server.AgentStatus, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Go(func() { states[i] = r.probe(ctx, t) })
	}
	wg.Wait()
	var workers []load.Worker
	why := map[string]string{}
	for i, st := range states {
		if st.Status != "available" {
			why[st.ID] = unavailableReason(st)
			continue
		}
		id := st.ID
		workers = append(workers, &directWorker{Worker: targets[i].client, name: st.Name, done: func() { r.reprobe(id) }})
	}
	return workers, why
}

// markBusy shows claimed direct agents as busy until their next check.
func (r *agentRegistry) markBusy(ids []string) {
	r.mu.Lock()
	for _, id := range ids {
		if c, ok := r.cache[id]; ok {
			c.Status = "busy"
			r.cache[id] = c
		}
	}
	r.mu.Unlock()
	if len(ids) > 0 {
		r.changed()
	}
}

func (r *agentRegistry) Claim(ctx context.Context, ids []string) ([]load.Worker, error) {
	agents, err := r.st.Agents(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]Agent{}
	for _, a := range agents {
		byID[a.ID] = a
	}
	if len(ids) == 0 {
		return r.claimAll(ctx, agents)
	}
	var targets []target
	var connIDs []string
	unavailable := map[string]string{}
	names := map[string]string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if addr, ok := r.isFlag(id); ok {
			targets = append(targets, r.flagTarget(addr))
			continue
		}
		a, ok := byID[id]
		switch {
		case !ok:
			unavailable[id] = "unknown agent"
		case a.Mode == AgentConnect:
			names[id] = a.Name
			connIDs = append(connIDs, id)
		default:
			names[id] = a.Name
			targets = append(targets, r.directTarget(ctx, a))
		}
	}
	// Direct agents hold no claim, so nothing needs handing back if one of
	// them is unavailable.
	workers, why := r.claimDirect(ctx, targets)
	for id, reason := range why {
		unavailable[id] = reason
	}
	for _, t := range targets {
		if n := r.nameFor(t.base.ID); n != "" {
			names[t.base.ID] = n
		}
	}
	if len(unavailable) > 0 {
		return nil, &server.UnavailableError{Agents: unavailable, Names: names}
	}
	if len(connIDs) > 0 {
		hw, why := r.hub.ClaimIDs(connIDs)
		if why != nil {
			return nil, &server.UnavailableError{Agents: why, Names: names}
		}
		workers = append(workers, hw...)
	}
	r.markBusy(directIDs(targets))
	return workers, nil
}

func directIDs(ts []target) []string {
	ids := make([]string, len(ts))
	for i, t := range ts {
		ids[i] = t.base.ID
	}
	return ids
}

// claimAll takes every available agent.
func (r *agentRegistry) claimAll(ctx context.Context, agents []Agent) ([]load.Worker, error) {
	var targets []target
	registered := map[string]bool{}
	for _, a := range agents {
		if a.Mode == AgentDirect {
			targets = append(targets, r.directTarget(ctx, a))
		} else {
			registered[a.ID] = true
		}
	}
	for _, addr := range r.flags {
		targets = append(targets, r.flagTarget(addr))
	}
	workers, why := r.claimDirect(ctx, targets)
	var claimed []string
	for _, t := range targets {
		if _, out := why[t.base.ID]; !out {
			claimed = append(claimed, t.base.ID)
		}
	}
	// Connected agents can be claimed by another test in the meantime;
	// leave those out and try again.
	for range 3 {
		var idle []string
		for id, in := range r.hub.ConnectedIDs() {
			if registered[id] && !in.Busy {
				idle = append(idle, id)
			}
		}
		if len(idle) == 0 {
			break
		}
		hw, busy := r.hub.ClaimIDs(idle)
		if busy == nil {
			workers = append(workers, hw...)
			break
		}
		for id := range busy {
			delete(registered, id)
		}
	}
	r.markBusy(claimed)
	return workers, nil
}

// Release hands back workers a test never started on.
func (r *agentRegistry) Release(workers []load.Worker) {
	for _, w := range workers {
		if dw, ok := w.(*directWorker); ok {
			dw.Close()
		}
	}
	r.hub.Release(workers)
}

// connectLimiter refuses an address for a window after too many failed
// dial-in attempts within that window.
type connectLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	fails  map[string][]time.Time
	until  map[string]time.Time
}

func newConnectLimiter(max int, window time.Duration) *connectLimiter {
	return &connectLimiter{max: max, window: window, fails: map[string][]time.Time{}, until: map[string]time.Time{}}
}

func (l *connectLimiter) blocked(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	u, ok := l.until[ip]
	if ok && now.Before(u) {
		return true
	}
	delete(l.until, ip)
	return false
}

func (l *connectLimiter) fail(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.fails) > 4096 {
		l.sweep(now)
	}
	recent := l.recentLocked(ip, now)
	recent = append(recent, now)
	if len(recent) >= l.max {
		l.until[ip] = now.Add(l.window)
		delete(l.fails, ip)
		return
	}
	l.fails[ip] = recent
}

func (l *connectLimiter) recentLocked(ip string, now time.Time) []time.Time {
	var keep []time.Time
	for _, t := range l.fails[ip] {
		if now.Sub(t) < l.window {
			keep = append(keep, t)
		}
	}
	return keep
}

// sweep forgets addresses with no recent failures or blocks.
func (l *connectLimiter) sweep(now time.Time) {
	for ip := range l.fails {
		if r := l.recentLocked(ip, now); len(r) == 0 {
			delete(l.fails, ip)
		} else {
			l.fails[ip] = r
		}
	}
	for ip, u := range l.until {
		if !now.Before(u) {
			delete(l.until, ip)
		}
	}
}

func (l *connectLimiter) reset(ip string) {
	l.mu.Lock()
	delete(l.fails, ip)
	l.mu.Unlock()
}
