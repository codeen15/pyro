package agent

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/codeen15/pyro/pkg/engine/load"
)

// Hub is the controller side of agents that connect out (pyro agent
// --connect), for agents behind NAT or firewalls. Each agent holds one idle
// connection; a test claims connections as workers, and an agent reconnects
// after every job. The job protocol is the same as for dialled agents.
//
// Every connection has an id: what Authenticate returns, or, with a single
// shared Token, "<agent name>@<remote address>". Set the optional fields
// before serving.
type Hub struct {
	// Token must be presented by agents when Authenticate is nil.
	Token string
	// Authenticate, when set, checks an agent's token instead of Token and
	// returns the agent's id. A new connection with an id that already has
	// an idle connection replaces it.
	Authenticate func(token string) (id string, ok bool)
	// NameFor, when set, names the worker for an agent id (in results).
	NameFor func(id string) string
	// OnChange is called (outside the hub's lock) after an agent connects,
	// disconnects, or is claimed or released.
	OnChange func()

	pingEvery time.Duration

	mu    sync.Mutex
	conns map[*hubConn]struct{} // live connections, idle and claimed
	// seq counts Disconnect calls; dropped is the seq of each id's last
	// one. A connection that authenticated before its id was disconnected
	// is refused when its hello arrives.
	seq     uint64
	dropped map[string]uint64
	ready   chan struct{} // signalled when a connection becomes idle
}

type hubConn struct {
	conn *websocket.Conn
	id   string
	info Info
	addr string
	used bool // claimed as a worker; guarded by Hub.mu

	// The pump is the connection's only reader (so pings get their pongs
	// while it's idle); workers read its messages from msgs.
	msgs chan message
	lost chan struct{} // closed when the pump stops; err says why
	err  error
	stop chan struct{} // closed when the hub lets go of the connection
	once sync.Once
}

func NewHub(token string) *Hub {
	return &Hub{Token: token, pingEvery: 15 * time.Second, conns: map[*hubConn]struct{}{}, ready: make(chan struct{}, 1)}
}

// Handler serves GET /agent/v1/connect.
func (h *Hub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /agent/v1/connect", h.connect)
	return mux
}

func (h *Hub) authenticate(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	if h.Authenticate != nil {
		return h.Authenticate(token)
	}
	return "", h.Token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(h.Token)) == 1
}

func (h *Hub) connect(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	since := h.seq // before the token check: a later Disconnect revokes this connection
	h.mu.Unlock()
	got, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	id, ok := h.authenticate(got)
	if !ok {
		http.Error(w, "invalid or missing agent token", http.StatusUnauthorized)
		return
	}
	if v := r.Header.Get(protocolHeader); v != strconv.Itoa(Protocol) {
		http.Error(w, "agent protocol mismatch: use the same Pyro version on both", http.StatusBadRequest)
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(64 << 20)
	ctx := r.Context()
	var hello message
	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = wsjson.Read(hctx, c, &hello)
	cancel()
	if err != nil || hello.Type != "hello" || hello.Info == nil {
		c.Close(websocket.StatusPolicyViolation, "expected hello")
		return
	}
	if id == "" {
		id = hello.Info.Name + "@" + r.RemoteAddr
	}
	hc := &hubConn{
		conn: c, id: id, info: *hello.Info, addr: r.RemoteAddr,
		msgs: make(chan message), lost: make(chan struct{}), stop: make(chan struct{}),
	}
	go hc.pump()

	h.mu.Lock()
	if h.dropped[id] > since {
		h.mu.Unlock()
		hc.once.Do(func() { close(hc.stop) })
		c.Close(websocket.StatusPolicyViolation, "agent was removed or its token changed")
		return
	}
	var replaced []*hubConn
	for old := range h.conns {
		if old.id == id && !old.used {
			replaced = append(replaced, old)
		}
	}
	h.conns[hc] = struct{}{}
	h.mu.Unlock()
	for _, old := range replaced {
		h.forget(old)
		go old.conn.Close(websocket.StatusNormalClosure, "replaced by a newer connection")
	}
	h.signal()
	h.changed()

	// Keep idle connections honest; a claimed one is driven by its worker.
	every := h.pingEvery
	if every <= 0 {
		every = 15 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			h.mu.Lock()
			used := hc.used
			h.mu.Unlock()
			if used {
				continue
			}
			pctx, cancel := context.WithTimeout(context.Background(), min(every, 10*time.Second))
			err := c.Ping(pctx)
			cancel()
			if err != nil {
				h.forget(hc)
				c.CloseNow()
				return
			}
		case <-hc.lost:
			h.forget(hc)
			c.CloseNow()
			return
		case <-hc.stop:
			return
		}
	}
}

// pump reads the connection until it fails or the hub lets go of it.
func (hc *hubConn) pump() {
	defer close(hc.lost)
	for {
		var m message
		if err := wsjson.Read(context.Background(), hc.conn, &m); err != nil {
			hc.err = err
			return
		}
		select {
		case hc.msgs <- m:
		case <-hc.stop:
			hc.err = net.ErrClosed
			return
		}
	}
}

// read is the readFunc for a hub connection.
func (hc *hubConn) read(ctx context.Context, m *message) error {
	select {
	case *m = <-hc.msgs:
		return nil
	case <-hc.lost:
		return hc.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// forget removes a connection from the hub (the caller closes it).
func (h *Hub) forget(hc *hubConn) {
	h.mu.Lock()
	_, live := h.conns[hc]
	delete(h.conns, hc)
	h.mu.Unlock()
	hc.once.Do(func() { close(hc.stop) })
	if live {
		h.changed()
	}
}

func (h *Hub) changed() {
	if h.OnChange != nil {
		h.OnChange()
	}
}

func (h *Hub) signal() {
	select {
	case h.ready <- struct{}{}:
	default:
	}
}

// Connected lists the agents waiting for work.
func (h *Hub) Connected() []Info {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Info
	for hc := range h.conns {
		if !hc.used {
			out = append(out, hc.info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ConnectedIDs lists every connected agent by id, idle or busy (claimed
// for a test, with Busy set).
func (h *Hub) ConnectedIDs() map[string]Info {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string]Info{}
	for hc := range h.conns {
		if prev, ok := out[hc.id]; ok && !prev.Busy {
			continue // an idle connection wins over a busy one
		}
		in := hc.info
		in.Busy = hc.used
		out[hc.id] = in
	}
	return out
}

// Wait blocks until at least n agents are connected or ctx ends.
func (h *Hub) Wait(ctx context.Context, n int) error {
	for {
		if len(h.Connected()) >= n {
			return nil
		}
		select {
		case <-h.ready:
		case <-time.After(250 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Claim takes every connected agent (or at most max, if max > 0) as load
// workers for one test.
func (h *Hub) Claim(max int) []load.Worker {
	h.mu.Lock()
	var conns []*hubConn
	for hc := range h.conns {
		if !hc.used {
			conns = append(conns, hc)
		}
	}
	sort.Slice(conns, func(i, j int) bool { return conns[i].info.Name < conns[j].info.Name })
	if max > 0 && len(conns) > max {
		conns = conns[:max]
	}
	out := make([]load.Worker, len(conns))
	for i, hc := range conns {
		hc.used = true
		out[i] = &connWorker{h: h, hc: hc}
	}
	h.mu.Unlock()
	if len(out) > 0 {
		h.changed()
	}
	return out
}

// ClaimIDs takes the agents with these ids as load workers, in order. If
// any of them is unavailable it claims nothing and says why, by id:
// "offline" (not connected) or "busy" (running another test).
func (h *Hub) ClaimIDs(ids []string) ([]load.Worker, map[string]string) {
	h.mu.Lock()
	idle := map[string]*hubConn{}
	online := map[string]bool{}
	for hc := range h.conns {
		online[hc.id] = true
		if !hc.used {
			idle[hc.id] = hc
		}
	}
	var unavailable map[string]string
	var conns []*hubConn
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		hc := idle[id]
		if hc == nil {
			if unavailable == nil {
				unavailable = map[string]string{}
			}
			unavailable[id] = "offline"
			if online[id] {
				unavailable[id] = "busy"
			}
			continue
		}
		conns = append(conns, hc)
	}
	if unavailable != nil {
		h.mu.Unlock()
		return nil, unavailable
	}
	out := make([]load.Worker, len(conns))
	for i, hc := range conns {
		hc.used = true
		out[i] = &connWorker{h: h, hc: hc}
	}
	h.mu.Unlock()
	if len(out) > 0 {
		h.changed()
	}
	return out, nil
}

// Release returns claimed workers that were never used to the idle pool.
// A worker that was already sent a job can't be reused; it is closed and
// its agent reconnects. Workers from elsewhere are ignored.
func (h *Hub) Release(workers []load.Worker) {
	var spent []*connWorker
	freed := false
	h.mu.Lock()
	for _, w := range workers {
		cw, ok := w.(*connWorker)
		if !ok || cw.h != h || cw.released {
			continue
		}
		cw.released = true
		if cw.sent {
			spent = append(spent, cw)
			continue
		}
		if _, live := h.conns[cw.hc]; live {
			cw.hc.used = false
			freed = true
		}
	}
	h.mu.Unlock()
	for _, cw := range spent {
		h.forget(cw.hc)
		cw.hc.conn.CloseNow()
	}
	if freed {
		h.signal()
		h.changed()
	}
}

// Disconnect closes every connection of the agent with this id, including
// one still waiting for its hello (it is refused when the hello arrives).
func (h *Hub) Disconnect(id string) {
	h.mu.Lock()
	h.seq++
	if h.dropped == nil {
		h.dropped = map[string]uint64{}
	}
	h.dropped[id] = h.seq
	var conns []*hubConn
	for hc := range h.conns {
		if hc.id == id {
			conns = append(conns, hc)
		}
	}
	h.mu.Unlock()
	for _, hc := range conns {
		h.forget(hc)
		hc.conn.CloseNow()
	}
}

var errReleased = errors.New("agent worker was released")

// connWorker runs one job over a connection an agent opened.
type connWorker struct {
	h  *Hub
	hc *hubConn
	// Guarded by h.mu.
	sent     bool // Prepare sent a job
	released bool // handed back by Release
}

func (w *connWorker) Name() string {
	if w.h.NameFor != nil {
		if n := w.h.NameFor(w.hc.id); n != "" {
			return n
		}
	}
	if w.hc.info.Name != "" {
		return w.hc.info.Name
	}
	return w.hc.addr
}

// live reports whether the worker still owns its connection.
func (w *connWorker) live(markSent bool) bool {
	w.h.mu.Lock()
	defer w.h.mu.Unlock()
	if w.released {
		return false
	}
	if markSent {
		w.sent = true
	}
	return true
}

func (w *connWorker) Prepare(ctx context.Context, job load.Job) error {
	if !w.live(true) {
		return errReleased
	}
	return prepareConn(ctx, w.hc.conn, w.hc.read, job)
}

func (w *connWorker) Run(ctx context.Context, report func(load.Report)) error {
	if !w.live(false) {
		return errReleased
	}
	defer w.Close()
	return runConn(ctx, w.hc.conn, w.hc.read, report)
}

func (w *connWorker) Stop() {
	if w.live(false) {
		stopConn(w.hc.conn)
	}
}

func (w *connWorker) Close() {
	if !w.live(false) {
		return
	}
	w.h.forget(w.hc)
	w.hc.conn.Close(websocket.StatusNormalClosure, "")
}
