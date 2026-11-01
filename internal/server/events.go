package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Event is pushed to subscribers of a topic over /api/v1/events.
type Event struct {
	Topic string `json:"topic"`
	Type  string `json:"type"`
	Data  any    `json:"data,omitempty"`
}

// hub fans events out to WebSocket subscribers. Slow subscribers lose events
// rather than slowing the publisher (a load test must never wait on the UI).
type hub struct {
	mu   sync.Mutex
	subs map[*subscriber]struct{}
}

type subscriber struct {
	mu     sync.Mutex
	topics map[string]bool
	ch     chan Event
}

func newHub() *hub { return &hub{subs: map[*subscriber]struct{}{}} }

func (h *hub) publish(topic, typ string, data any) {
	ev := Event{Topic: topic, Type: typ, Data: data}
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		s.mu.Lock()
		want := s.topics[topic]
		s.mu.Unlock()
		if !want {
			continue
		}
		select {
		case s.ch <- ev:
		default:
		}
	}
}

func (h *hub) add(s *subscriber) {
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
}

func (h *hub) remove(s *subscriber) {
	h.mu.Lock()
	delete(h.subs, s)
	h.mu.Unlock()
}

// events upgrades to a WebSocket. Browsers can't set headers on WebSocket
// requests, so the session token arrives as ?token=.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Token != "" && subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(s.cfg.Token)) != 1 {
		writeError(w, http.StatusUnauthorized, "missing or invalid session token")
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	sub := &subscriber{topics: map[string]bool{}, ch: make(chan Event, 512)}
	s.hub.add(sub)
	defer s.hub.remove(sub)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		defer cancel()
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var msg struct {
				Op    string `json:"op"`
				Topic string `json:"topic"`
			}
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			sub.mu.Lock()
			switch msg.Op {
			case "subscribe":
				sub.topics[msg.Topic] = true
			case "unsubscribe":
				delete(sub.topics, msg.Topic)
			}
			sub.mu.Unlock()
		}
	}()

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			conn.Close(websocket.StatusNormalClosure, "")
			return
		case ev := <-sub.ch:
			b, _ := json.Marshal(ev)
			wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Write(wctx, websocket.MessageText, b)
			wcancel()
			if err != nil {
				return
			}
		case <-ping.C:
			pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pctx)
			pcancel()
			if err != nil {
				return
			}
		}
	}
}
