package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/codeen15/pyro/pkg/engine/check"
	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/protocol"
)

// Socket steps: WebSocket and Socket.IO requests that connect, send and
// wait, ping or close, on connections that stay open between steps, so a
// run can go Connect → HTTP login → Send (with the token) → wait → Close.

// maxInbox bounds the unread messages a connection keeps.
const maxInbox = 1000

// defaultWait bounds message and ack waits when the step gives no timeout.
const defaultWait = 10 * time.Second

// Conn is an open connection with an inbox of messages not yet read by a
// step. It owns reading the session.
type Conn struct {
	Name string
	sess protocol.Session

	mu     sync.Mutex
	inbox  []protocol.Message // unread received messages
	notify chan struct{}      // closed and replaced when something arrives
	closed bool
	err    error
	done   chan struct{}
}

// NewConn starts reading a session. onMessage, when set, sees every message
// (received and system) as it arrives, for logs and live views.
func NewConn(name string, s protocol.Session, onMessage func(protocol.Message)) *Conn {
	c := &Conn{Name: name, sess: s, notify: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(c.done)
		for m := range s.Messages() {
			if onMessage != nil {
				onMessage(m)
			}
			c.mu.Lock()
			if m.Direction == "received" {
				c.inbox = append(c.inbox, m)
				if len(c.inbox) > maxInbox {
					c.inbox = c.inbox[len(c.inbox)-maxInbox:]
				}
			}
			close(c.notify)
			c.notify = make(chan struct{})
			c.mu.Unlock()
		}
		c.mu.Lock()
		c.closed, c.err = true, s.Err()
		close(c.notify)
		c.notify = make(chan struct{})
		c.mu.Unlock()
	}()
	return c
}

// Session is the underlying session.
func (c *Conn) Session() protocol.Session { return c.sess }

// Done is closed when the connection has ended and its reader stopped.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err is why the connection ended (nil after a clean close).
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// MarkRead drops the unread messages, so the next wait only sees what
// arrives after now (used when someone works the connection by hand).
func (c *Conn) MarkRead() {
	c.mu.Lock()
	c.inbox = nil
	c.mu.Unlock()
}

// Closed reports whether the connection has ended.
func (c *Conn) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// next returns the oldest unread message, waiting until deadline.
func (c *Conn) next(ctx context.Context, deadline time.Time) (protocol.Message, error) {
	for {
		c.mu.Lock()
		if len(c.inbox) > 0 {
			m := c.inbox[0]
			c.inbox = c.inbox[1:]
			c.mu.Unlock()
			return m, nil
		}
		if c.closed {
			err := c.err
			c.mu.Unlock()
			if err == nil {
				err = errors.New("the connection was closed")
			}
			return protocol.Message{}, err
		}
		wake := c.notify
		c.mu.Unlock()
		t := time.NewTimer(time.Until(deadline))
		select {
		case <-wake:
			t.Stop()
		case <-t.C:
			return protocol.Message{}, errWaitTimeout
		case <-ctx.Done():
			t.Stop()
			return protocol.Message{}, ctx.Err()
		}
	}
}

var errWaitTimeout = errors.New("timeout")

// close ends the connection (with a code and reason when the protocol
// supports one) and waits for the reader.
func (c *Conn) close(code int, reason string) error {
	var err error
	if cw, ok := c.sess.(protocol.CloserWith); ok && (code != 0 || reason != "") {
		err = cw.CloseWith(code, reason)
	} else {
		err = c.sess.Close()
	}
	<-c.done
	return err
}

// Conns holds a run's (or a VU's, or a user's) named connections.
type Conns struct {
	mu     sync.Mutex
	byName map[string]*Conn
	// OnMessage, when set, sees every connection's traffic (by name).
	OnMessage func(name string, m protocol.Message)
	// OnOpen and OnClose, when set, are told about connections.
	OnOpen  func(c *Conn)
	OnClose func(name string)
}

func NewConns() *Conns { return &Conns{byName: map[string]*Conn{}} }

// Get returns an open connection.
func (cs *Conns) Get(name string) *Conn {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	c := cs.byName[name]
	if c != nil && c.Closed() {
		delete(cs.byName, name)
		return nil
	}
	return c
}

// Open registers a session under name, closing any connection it replaces.
func (cs *Conns) Open(name string, s protocol.Session) *Conn {
	var onMsg func(protocol.Message)
	if cs.OnMessage != nil {
		onMsg = func(m protocol.Message) { cs.OnMessage(name, m) }
	}
	c := NewConn(name, s, onMsg)
	cs.mu.Lock()
	old := cs.byName[name]
	cs.byName[name] = c
	cs.mu.Unlock()
	if old != nil {
		_ = old.close(0, "")
	}
	if cs.OnOpen != nil {
		cs.OnOpen(c)
	}
	return c
}

// Put registers a connection made with NewConn, closing any it replaces.
func (cs *Conns) Put(name string, c *Conn) {
	cs.mu.Lock()
	old := cs.byName[name]
	cs.byName[name] = c
	cs.mu.Unlock()
	if old != nil && old != c {
		_ = old.close(0, "")
	}
	if cs.OnOpen != nil {
		cs.OnOpen(c)
	}
}

// Close ends a named connection.
func (cs *Conns) Close(name string, code int, reason string) error {
	cs.mu.Lock()
	c := cs.byName[name]
	delete(cs.byName, name)
	cs.mu.Unlock()
	if c == nil {
		return fmt.Errorf("connection %q isn't open", name)
	}
	err := c.close(code, reason)
	if cs.OnClose != nil {
		cs.OnClose(name)
	}
	return err
}

// CloseAll ends every connection (at the end of a run or a VU).
func (cs *Conns) CloseAll() {
	cs.mu.Lock()
	all := cs.byName
	cs.byName = map[string]*Conn{}
	cs.mu.Unlock()
	for name, c := range all {
		_ = c.close(0, "")
		if cs.OnClose != nil {
			cs.OnClose(name)
		}
	}
}

// Names lists the open connections.
func (cs *Conns) Names() []string {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	out := make([]string, 0, len(cs.byName))
	for n, c := range cs.byName {
		if !c.Closed() {
			out = append(out, n)
		}
	}
	return out
}

// SocketResult describes what a socket step did.
type SocketResult struct {
	Action     string `json:"action"`
	Connection string `json:"connection,omitempty"`
	// Own is set when the step used its own short connection.
	Own      bool                  `json:"own,omitempty"`
	Info     *protocol.SessionInfo `json:"info,omitempty"`
	Sent     []protocol.Message    `json:"sent,omitempty"`
	Received []protocol.Message    `json:"received,omitempty"`
	// Matched is the message (or ack) the wait was for.
	Matched *protocol.Message `json:"matched,omitempty"`
	WaitMS  float64           `json:"waitMs,omitempty"`
	RTTMS   float64           `json:"rttMs,omitempty"`
}

// maxStepMessages bounds the messages a step result records.
const maxStepMessages = 200

func isSocket(k model.Kind) bool { return k == model.KindWebSocket || k == model.KindSocketIO }

// dial opens a session for a resolved request.
func (e *Engine) dial(ctx context.Context, sent *model.Request) (protocol.Session, error) {
	switch sent.Kind {
	case model.KindWebSocket:
		return wsDial(ctx, sent)
	case model.KindSocketIO:
		return sioDial(ctx, sent)
	}
	return nil, fmt.Errorf("%s requests are sent, not opened as sessions", sent.Kind)
}

// socketStep runs a WebSocket or Socket.IO request's action. Its response
// (for post-processors) is the matched message: Body is its data (for
// Socket.IO, the JSON array of the event's arguments), Timings.Total how
// long the step took.
func (e *Engine) socketStep(ctx context.Context, sent *model.Request, conns *Conns, res *Result) (*protocol.Response, error) {
	action := sent.SocketAction()
	sr := &SocketResult{Action: action, Connection: sent.Connection}
	res.Socket = sr
	start := time.Now()
	resp := &protocol.Response{StatusText: action}
	done := func() (*protocol.Response, error) {
		resp.Timings.Total = time.Since(start)
		return resp, nil
	}
	if conns == nil {
		conns = NewConns()
	}

	switch action {
	case model.ActionConnect:
		name := sent.Connection
		if name == "" {
			name = model.DefaultConnection
		}
		sr.Connection = name
		s, err := e.dial(ctx, sent)
		if err != nil {
			return nil, err
		}
		conns.Open(name, s)
		info := s.Info()
		sr.Info = &info
		resp.Status, resp.Headers, resp.StatusText = info.Status, info.Headers, "connected"
		return done()

	case model.ActionClose:
		name := sent.Connection
		if name == "" {
			name = model.DefaultConnection
		}
		sr.Connection = name
		if err := conns.Close(name, sent.CloseCode, sent.CloseReason); err != nil {
			return nil, err
		}
		resp.StatusText = "closed"
		return done()

	case model.ActionSend, model.ActionPing:
		conn, own, err := e.stepConn(ctx, sent, conns)
		if err != nil {
			return nil, err
		}
		if own {
			sr.Own = true
			defer conn.close(0, "")
			info := conn.sess.Info()
			sr.Info = &info
		}
		if action == model.ActionPing {
			p, ok := conn.sess.(protocol.Pinger)
			if !ok {
				return nil, errors.New("this connection can't be pinged (Socket.IO keeps itself alive with the server's heartbeat; use a send that waits for an ack instead)")
			}
			rtt, err := p.Ping(ctx)
			if err != nil {
				return nil, err
			}
			sr.RTTMS = ms(rtt)
			resp.StatusText = "pong"
			resp.Timings.Total = rtt
			return resp, nil
		}
		return e.sendAndWait(ctx, sent, conn, sr, resp, start)
	}
	return nil, fmt.Errorf("unknown socket action %q (use connect, send, ping or close)", action)
}

// stepConn finds the step's connection: the named one, or its own.
func (e *Engine) stepConn(ctx context.Context, sent *model.Request, conns *Conns) (*Conn, bool, error) {
	if sent.Connection != "" {
		c := conns.Get(sent.Connection)
		if c == nil {
			return nil, false, fmt.Errorf("connection %q isn't open: run its connect request first", sent.Connection)
		}
		return c, false, nil
	}
	if strings.TrimSpace(sent.URL) == "" {
		return nil, false, errors.New("give a connection name, or a URL for this request's own connection")
	}
	s, err := e.dial(ctx, sent)
	if err != nil {
		return nil, false, err
	}
	return NewConn("", s, nil), true, nil
}

func (e *Engine) sendAndWait(ctx context.Context, sent *model.Request, conn *Conn, sr *SocketResult, resp *protocol.Response, start time.Time) (*protocol.Response, error) {
	var lastAck *int
	for _, m := range sent.Messages {
		out := protocol.Outgoing{Data: m.Content, Event: m.Event, Binary: m.Binary, Ack: m.Ack}
		got, err := conn.sess.Send(ctx, out)
		if err != nil {
			return nil, err
		}
		if len(sr.Sent) < maxStepMessages {
			sr.Sent = append(sr.Sent, got)
		}
		if got.AckID != nil {
			lastAck = got.AckID
		}
	}
	w := sent.Wait
	if w == nil || w.For == "" {
		resp.StatusText = "sent"
		resp.Timings.Total = time.Since(start)
		return resp, nil
	}
	timeout := defaultWait
	if w.Timeout != "" {
		d, err := time.ParseDuration(w.Timeout)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("wait timeout %q isn't a duration like 10s or 500ms", w.Timeout)
		}
		timeout = d
	}
	match, err := matcher(w, lastAck)
	if err != nil {
		return nil, err
	}
	waitStart := time.Now()
	deadline := waitStart.Add(timeout)
	var received []any // for wait: time, every message's data
	for {
		m, err := conn.next(ctx, deadline)
		if err != nil {
			sr.WaitMS = ms(time.Since(waitStart))
			if errors.Is(err, errWaitTimeout) {
				if w.For == "time" {
					body, _ := json.Marshal(received)
					resp.Body, resp.Size, resp.StatusText = body, int64(len(body)), fmt.Sprintf("%d received", len(received))
					resp.Timings.Total = time.Since(start)
					return resp, nil
				}
				return nil, fmt.Errorf("no %s within %s", describeWait(w), timeout)
			}
			return nil, err
		}
		if len(sr.Received) < maxStepMessages {
			sr.Received = append(sr.Received, m)
		}
		if w.For == "time" {
			received = append(received, decodeData(m.Data))
			continue
		}
		if match(m) {
			mm := m
			sr.Matched = &mm
			sr.WaitMS = ms(time.Since(waitStart))
			resp.Body, resp.Size = []byte(m.Data), int64(len(m.Data))
			resp.StatusText = m.Kind
			if m.Event != "" {
				resp.StatusText = m.Event
			}
			resp.Timings.Total = time.Since(start)
			return resp, nil
		}
	}
}

// matcher builds a wait's test for received messages.
func matcher(w *model.Wait, ack *int) (func(protocol.Message) bool, error) {
	switch w.For {
	case "time":
		return func(protocol.Message) bool { return false }, nil
	case "ack":
		if ack == nil {
			return nil, errors.New("waiting for an ack needs a message sent with ack (Socket.IO)")
		}
		id := *ack
		return func(m protocol.Message) bool { return m.Kind == "ack" && m.AckID != nil && *m.AckID == id }, nil
	case "message":
	default:
		return nil, fmt.Errorf("wait for %q isn't known (use message, ack or time)", w.For)
	}
	var re *regexp.Regexp
	if w.Regex != "" {
		var err error
		if re, err = regexp.Compile(w.Regex); err != nil {
			return nil, fmt.Errorf("wait regex: %w", err)
		}
	}
	return func(m protocol.Message) bool {
		if m.Kind == "ack" {
			return false // acks answer sends; waiting for a message means a pushed one
		}
		if w.Event != "" && m.Event != w.Event {
			return false
		}
		if w.Contains != "" && !strings.Contains(m.Data, w.Contains) {
			return false
		}
		if re != nil && !re.MatchString(m.Data) {
			return false
		}
		if w.JSONPath != "" {
			vals, _ := check.Extract([]model.Extractor{{Var: "v", From: "jsonpath", Path: w.JSONPath}},
				check.Wrap(&protocol.Response{Body: []byte(m.Data)}))
			v, ok := vals["v"]
			if !ok || (w.Equals != "" && v != w.Equals) {
				return false
			}
		}
		return true
	}, nil
}

func describeWait(w *model.Wait) string {
	switch w.For {
	case "ack":
		return "ack"
	}
	var parts []string
	if w.Event != "" {
		parts = append(parts, "event "+w.Event)
	}
	if w.Contains != "" {
		parts = append(parts, fmt.Sprintf("containing %q", w.Contains))
	}
	if w.Regex != "" {
		parts = append(parts, "matching /"+w.Regex+"/")
	}
	if w.JSONPath != "" {
		if w.Equals != "" {
			parts = append(parts, fmt.Sprintf("with %s = %q", w.JSONPath, w.Equals))
		} else {
			parts = append(parts, "with "+w.JSONPath)
		}
	}
	if len(parts) == 0 {
		return "message"
	}
	return "message " + strings.Join(parts, ", ")
}

// decodeData is a message's data as JSON when it parses, else the text.
func decodeData(s string) any {
	var v any
	if json.Unmarshal([]byte(s), &v) == nil {
		return v
	}
	return s
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
