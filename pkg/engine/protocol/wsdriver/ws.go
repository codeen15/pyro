// Package wsdriver opens WebSocket sessions.
package wsdriver

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/protocol"
)

// MaxMessageBytes caps one received message.
const MaxMessageBytes = 16 << 20

// Dial connects using the request's URL and headers (variables resolved,
// auth applied). A Sec-WebSocket-Protocol header selects subprotocols.
func Dial(ctx context.Context, req *model.Request, client *http.Client) (*Session, error) {
	u := strings.TrimSpace(req.URL)
	switch {
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case !strings.HasPrefix(u, "ws://") && !strings.HasPrefix(u, "wss://"):
		u = "ws://" + u
	}
	opts := &websocket.DialOptions{HTTPHeader: http.Header{}, HTTPClient: client}
	for _, h := range req.Headers {
		if h.Disabled || h.Key == "" {
			continue
		}
		if strings.EqualFold(h.Key, "Sec-WebSocket-Protocol") {
			for _, p := range strings.Split(h.Value, ",") {
				opts.Subprotocols = append(opts.Subprotocols, strings.TrimSpace(p))
			}
			continue
		}
		opts.HTTPHeader.Add(h.Key, h.Value)
	}
	start := time.Now()
	conn, res, err := websocket.Dial(ctx, u, opts)
	if err != nil {
		if res != nil {
			return nil, fmt.Errorf("handshake failed: server answered %s", res.Status)
		}
		return nil, fmt.Errorf("could not connect to %s: %w", u, err)
	}
	conn.SetReadLimit(MaxMessageBytes)
	s := newSession(conn, protocol.SessionInfo{URL: u, Subprotocol: conn.Subprotocol(), ConnectTime: time.Since(start)})
	if res != nil {
		s.info.Status = res.StatusCode
		for k, vs := range res.Header {
			for _, v := range vs {
				s.info.Headers = append(s.info.Headers, model.KeyValue{Key: k, Value: v})
			}
		}
	}
	go s.readLoop()
	return s, nil
}

// Session is a WebSocket session. Received messages are buffered; if the
// consumer falls behind, the oldest undelivered messages are dropped and a
// system message reports how many.
type Session struct {
	conn *websocket.Conn
	info protocol.SessionInfo
	msgs chan protocol.Message
	done chan struct{}

	mu      sync.Mutex
	seq     int
	err     error
	closing bool
	dropped int
}

func newSession(conn *websocket.Conn, info protocol.SessionInfo) *Session {
	return &Session{conn: conn, info: info, msgs: make(chan protocol.Message, 1024), done: make(chan struct{})}
}

func (s *Session) Messages() <-chan protocol.Message { return s.msgs }
func (s *Session) Done() <-chan struct{}             { return s.done }
func (s *Session) Info() protocol.SessionInfo        { return s.info }

func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *Session) next(m protocol.Message) protocol.Message {
	s.mu.Lock()
	s.seq++
	m.Seq = s.seq
	s.mu.Unlock()
	if m.At.IsZero() {
		m.At = time.Now()
	}
	return m
}

func (s *Session) deliver(m protocol.Message) {
	m = s.next(m)
	select {
	case s.msgs <- m:
		return
	default:
	}
	// Buffer full: drop the oldest and note it.
	select {
	case <-s.msgs:
		s.mu.Lock()
		s.dropped++
		s.mu.Unlock()
	default:
	}
	select {
	case s.msgs <- m:
	default:
	}
}

func (s *Session) readLoop() {
	defer close(s.msgs)
	defer close(s.done)
	ctx := context.Background()
	for {
		typ, data, err := s.conn.Read(ctx)
		if err != nil {
			s.finish(err)
			return
		}
		m := protocol.Message{Direction: "received", Kind: "text", Data: string(data), Size: len(data)}
		if typ == websocket.MessageBinary {
			m.Kind, m.Data = "binary", base64.StdEncoding.EncodeToString(data)
		}
		s.deliver(m)
	}
}

func (s *Session) finish(err error) {
	s.mu.Lock()
	closing := s.closing
	dropped := s.dropped
	s.mu.Unlock()

	status := websocket.CloseStatus(err)
	var reason string
	var ce websocket.CloseError
	if errors.As(err, &ce) {
		reason = ce.Reason
	}
	switch {
	case closing || status == websocket.StatusNormalClosure:
		err = nil
	case status != -1:
		err = fmt.Errorf("server closed the connection: %d %s", status, reason)
	default:
		err = fmt.Errorf("connection lost: %v", err)
	}
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
	msg := "closed"
	if status != -1 {
		msg = fmt.Sprintf("closed (%d%s)", status, prefix(" ", reason))
	}
	if err != nil {
		msg = err.Error()
	}
	if dropped > 0 {
		msg += fmt.Sprintf("; %d messages were dropped because the reader fell behind", dropped)
	}
	kind := "close"
	if err != nil {
		kind = "error"
	}
	s.deliver(protocol.Message{Direction: "system", Kind: kind, Data: msg})
}

func prefix(p, s string) string {
	if s == "" {
		return ""
	}
	return p + s
}

func (s *Session) Send(ctx context.Context, out protocol.Outgoing) (protocol.Message, error) {
	m := protocol.Message{Direction: "sent", Kind: "text", Data: out.Data, Size: len(out.Data)}
	typ, payload := websocket.MessageText, []byte(out.Data)
	if out.Binary {
		b, err := base64.StdEncoding.DecodeString(out.Data)
		if err != nil {
			return m, fmt.Errorf("binary messages must be base64: %w", err)
		}
		typ, payload, m.Kind, m.Size = websocket.MessageBinary, b, "binary", len(b)
	}
	// Numbered before writing: a fast reply must not be numbered (and shown)
	// before the message that caused it.
	m = s.next(m)
	if err := s.conn.Write(ctx, typ, payload); err != nil {
		return m, fmt.Errorf("send failed: %w", err)
	}
	return m, nil
}

// Close performs the closing handshake and waits for the reader to finish.
func (s *Session) Close() error {
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	_ = s.conn.Close(websocket.StatusNormalClosure, "")
	<-s.done
	return nil
}

// Ping sends a ping frame and waits for its pong; the result is the round
// trip.
func (s *Session) Ping(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	if err := s.conn.Ping(ctx); err != nil {
		return 0, fmt.Errorf("ping: %w", err)
	}
	return time.Since(start), nil
}

// CloseWith closes with a status code (1000, or 3000–4999) and reason.
func (s *Session) CloseWith(code int, reason string) error {
	if code == 0 {
		code = int(websocket.StatusNormalClosure)
	}
	if code != int(websocket.StatusNormalClosure) && (code < 3000 || code > 4999) {
		return fmt.Errorf("close code %d isn't allowed: use 1000, or 3000–4999 for your own codes", code)
	}
	if len(reason) > 123 {
		return fmt.Errorf("close reason is %d bytes; WebSocket allows 123", len(reason))
	}
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	_ = s.conn.Close(websocket.StatusCode(code), reason)
	<-s.done
	return nil
}
