package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/protocol"
	"github.com/codeen15/pyro/pkg/engine/protocol/httpdriver"
	"github.com/codeen15/pyro/pkg/engine/vars"
)

// A chat-like WebSocket server: "hello" → {"type":"welcome"}; "push" sends
// {"type":"push","n":1} after 50 ms; anything else is echoed. /notify on
// HTTP pushes {"type":"notified"} to the last connection.
func chatServer(t *testing.T) *httptest.Server {
	t.Helper()
	var last *websocket.Conn
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		last = c
		ctx := context.Background()
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			switch string(data) {
			case "hello":
				_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"welcome","user":"ada"}`))
			case "push":
				go func() {
					time.Sleep(50 * time.Millisecond)
					_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"push","n":1}`))
				}()
			default:
				_ = c.Write(ctx, websocket.MessageText, data)
			}
		}
	})
	mux.HandleFunc("/notify", func(w http.ResponseWriter, r *http.Request) {
		if last != nil {
			_ = last.Write(r.Context(), websocket.MessageText, []byte(`{"type":"notified","token":"t-42"}`))
		}
		w.Write([]byte(`{"ok":true}`))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestSocketSteps(t *testing.T) {
	ts := chatServer(t)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	e := New(httpdriver.New(httpdriver.Options{}))
	conns := NewConns()
	defer conns.CloseAll()
	v := vars.NewSet()
	ctx := context.Background()
	run := func(r *model.Request) *Result {
		t.Helper()
		return e.Send(ctx, SendInput{Request: r, Vars: v, Conns: conns})
	}
	ws := func(r model.Request) *model.Request { r.Kind = model.KindWebSocket; return &r }

	// Connect a named connection.
	if res := run(ws(model.Request{Name: "Open", URL: wsURL, Connection: "chat"})); res.Error != "" || res.Socket.Connection != "chat" {
		t.Fatalf("connect: %+v", res)
	}

	// Send and wait for a matching message; extract from it.
	res := run(ws(model.Request{Action: model.ActionSend, Connection: "chat", Messages: []model.Message{{Content: "hello"}},
		Wait:    &model.Wait{For: "message", JSONPath: "$.type", Equals: "welcome"},
		Extract: []model.Extractor{{Var: "user", From: "jsonpath", Path: "$.user"}}}))
	if res.Error != "" || res.Socket.Matched == nil || v.Runtime["user"] != "ada" {
		t.Fatalf("send+wait: %+v (user=%q)", res, v.Runtime["user"])
	}

	// An HTTP request in between pushes a message; the next wait still sees it.
	if hr := run(&model.Request{Kind: model.KindHTTP, Method: "GET", URL: ts.URL + "/notify"}); hr.Error != "" {
		t.Fatalf("http: %v", hr.Error)
	}
	res = run(ws(model.Request{Action: model.ActionSend, Connection: "chat",
		Wait:    &model.Wait{For: "message", Contains: "notified"},
		Extract: []model.Extractor{{Var: "token", From: "jsonpath", Path: "$.token"}}}))
	if res.Error != "" || v.Runtime["token"] != "t-42" {
		t.Fatalf("wait for earlier push: %+v", res)
	}

	// Use the extracted value in a message ({{token}} is resolved).
	res = run(ws(model.Request{Action: model.ActionSend, Connection: "chat", Messages: []model.Message{{Content: `{"auth":"{{token}}"}`}},
		Wait:       &model.Wait{For: "message", Regex: `"auth":"t-42"`},
		Assertions: []model.Assertion{{Type: "jsonpath", Path: "$.auth", Value: "t-42"}}}))
	if res.Error != "" || len(res.Tests) != 1 || !res.Tests[0].Passed || res.Socket.Sent[0].Data != `{"auth":"t-42"}` {
		t.Fatalf("templated send: %+v", res)
	}

	// A wait that never matches times out and fails the step.
	res = run(ws(model.Request{Action: model.ActionSend, Connection: "chat", Messages: []model.Message{{Content: "x"}},
		Wait: &model.Wait{For: "message", Contains: "never", Timeout: "150ms"}}))
	if !strings.Contains(res.Error, `no message containing "never" within 150ms`) || res.Passed() {
		t.Fatalf("timeout: %q", res.Error)
	}

	// Listening for a while collects what arrives.
	res = run(ws(model.Request{Action: model.ActionSend, Connection: "chat", Messages: []model.Message{{Content: "push"}},
		Wait: &model.Wait{For: "time", Timeout: "200ms"}}))
	if res.Error != "" || !strings.Contains(string(res.Response.Body), `"push"`) {
		t.Fatalf("listen: %+v %s", res.Error, res.Response.Body)
	}

	// Ping measures a round trip.
	res = run(ws(model.Request{Action: model.ActionPing, Connection: "chat"}))
	if res.Error != "" || res.Socket.RTTMS < 0 {
		t.Fatalf("ping: %+v", res)
	}

	// Close; later steps on it fail clearly.
	if res := run(ws(model.Request{Action: model.ActionClose, Connection: "chat", CloseCode: 4000, CloseReason: "done"})); res.Error != "" {
		t.Fatalf("close: %v", res.Error)
	}
	if res := run(ws(model.Request{Action: model.ActionSend, Connection: "chat", Messages: []model.Message{{Content: "x"}}})); !strings.Contains(res.Error, `connection "chat" isn't open`) {
		t.Fatalf("send after close: %q", res.Error)
	}

	// A send without a connection uses its own, just for itself.
	res = run(ws(model.Request{Action: model.ActionSend, URL: wsURL, Messages: []model.Message{{Content: "hello"}}, Wait: &model.Wait{For: "message", Contains: "welcome"}}))
	if res.Error != "" || !res.Socket.Own || len(conns.Names()) != 0 {
		t.Fatalf("own connection: %+v %v", res, conns.Names())
	}
	if res := run(ws(model.Request{Action: model.ActionSend})); !strings.Contains(res.Error, "give a connection name, or a URL") {
		t.Fatalf("no connection, no URL: %q", res.Error)
	}
}

func TestWaitAckNeedsAnAckMessage(t *testing.T) {
	if _, err := matcher(&model.Wait{For: "ack"}, nil); err == nil {
		t.Fatal("ack wait without an ack message accepted")
	}
	id := 3
	m, _ := matcher(&model.Wait{For: "ack"}, &id)
	if !m(protocolAck(3)) || m(protocolAck(2)) {
		t.Fatal("ack matching")
	}
}

func protocolAck(id int) protocol.Message { return protocol.Message{Direction: "received", Kind: "ack", AckID: &id} }
