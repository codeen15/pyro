package agent

import (
	"context"
	"crypto/subtle"
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
type Hub struct {
	// Token must be presented by agents. Required.
	Token string

	mu    sync.Mutex
	idle  map[*hubConn]bool
	ready chan struct{} // signalled when an agent connects
}

type hubConn struct {
	conn *websocket.Conn
	info Info
	addr string
	done chan struct{} // closed when the handler returns
	used bool
}

func NewHub(token string) *Hub {
	return &Hub{Token: token, idle: map[*hubConn]bool{}, ready: make(chan struct{}, 1)}
}

// Handler serves GET /agent/v1/connect.
func (h *Hub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /agent/v1/connect", h.connect)
	return mux
}

func (h *Hub) connect(w http.ResponseWriter, r *http.Request) {
	got, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if h.Token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(h.Token)) != 1 {
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
	hc := &hubConn{conn: c, info: *hello.Info, addr: r.RemoteAddr, done: make(chan struct{})}
	h.mu.Lock()
	h.idle[hc] = true
	h.mu.Unlock()
	select {
	case h.ready <- struct{}{}:
	default:
	}
	// Keep idle connections honest; a claimed one is driven by its worker.
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	defer close(hc.done)
	for {
		select {
		case <-t.C:
			h.mu.Lock()
			used := hc.used
			h.mu.Unlock()
			if used {
				continue
			}
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Ping(pctx)
			cancel()
			if err != nil {
				h.drop(hc)
				return
			}
		case <-ctx.Done():
			h.drop(hc)
			return
		}
	}
}

func (h *Hub) drop(hc *hubConn) {
	h.mu.Lock()
	delete(h.idle, hc)
	h.mu.Unlock()
	hc.conn.CloseNow()
}

// Connected lists the agents waiting for work.
func (h *Hub) Connected() []Info {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Info
	for hc := range h.idle {
		if !hc.used {
			out = append(out, hc.info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
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
	defer h.mu.Unlock()
	var conns []*hubConn
	for hc := range h.idle {
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
		delete(h.idle, hc)
		out[i] = &connWorker{hc: hc}
	}
	return out
}

// connWorker runs one job over a connection an agent opened.
type connWorker struct {
	hc *hubConn
}

func (w *connWorker) Name() string {
	if w.hc.info.Name != "" {
		return w.hc.info.Name
	}
	return w.hc.addr
}

func (w *connWorker) Prepare(ctx context.Context, job load.Job) error {
	return prepareConn(ctx, w.hc.conn, job)
}

func (w *connWorker) Run(ctx context.Context, report func(load.Report)) error {
	defer w.Close()
	return runConn(ctx, w.hc.conn, report)
}

func (w *connWorker) Stop() { stopConn(w.hc.conn) }

func (w *connWorker) Close() {
	w.hc.conn.Close(websocket.StatusNormalClosure, "")
}
