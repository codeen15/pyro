package wsdriver

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/pyrohq/pyro/pkg/engine/model"
	"github.com/pyrohq/pyro/pkg/engine/protocol"
)

// echoServer echoes messages; "close-me" makes it close with 4001.
func echoServer(t *testing.T) *httptest.Server {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"chat.v2"}})
		if err != nil {
			return
		}
		ctx := context.Background()
		c.Write(ctx, websocket.MessageText, []byte("hello "+r.Header.Get("Authorization")))
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if string(data) == "close-me" {
				c.Close(4001, "asked to")
				return
			}
			c.Write(ctx, typ, data)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func recv(t *testing.T, s *Session) protocol.Message {
	t.Helper()
	select {
	case m, ok := <-s.Messages():
		if !ok {
			t.Fatal("channel closed")
		}
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a message")
	}
	return protocol.Message{}
}

func TestEchoTextBinaryAndClose(t *testing.T) {
	ts := echoServer(t)
	ctx := context.Background()
	s, err := Dial(ctx, &model.Request{URL: ts.URL, Headers: []model.KeyValue{
		{Key: "Authorization", Value: "Bearer t"},
		{Key: "Sec-WebSocket-Protocol", Value: "chat.v2, chat.v1"},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Info().Subprotocol != "chat.v2" || s.Info().Status != http.StatusSwitchingProtocols || !strings.HasPrefix(s.Info().URL, "ws://") {
		t.Fatalf("info = %+v", s.Info())
	}
	if m := recv(t, s); m.Data != "hello Bearer t" || m.Direction != "received" || m.Seq != 1 {
		t.Fatalf("greeting = %+v", m)
	}

	sent, err := s.Send(ctx, protocol.Outgoing{Data: `{"op":"ping"}`})
	if err != nil || sent.Direction != "sent" || sent.Seq != 2 {
		t.Fatalf("send = %+v, %v", sent, err)
	}
	if m := recv(t, s); m.Data != `{"op":"ping"}` || m.Kind != "text" {
		t.Fatalf("echo = %+v", m)
	}

	bin := base64.StdEncoding.EncodeToString([]byte{0, 1, 2, 255})
	s.Send(ctx, protocol.Outgoing{Data: bin, Binary: true})
	if m := recv(t, s); m.Kind != "binary" || m.Data != bin || m.Size != 4 {
		t.Fatalf("binary echo = %+v", m)
	}
	if _, err := s.Send(ctx, protocol.Outgoing{Data: "not base64!", Binary: true}); err == nil {
		t.Fatal("expected base64 error")
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s.Err() != nil {
		t.Fatalf("clean close err = %v", s.Err())
	}
}

func TestServerCloseReported(t *testing.T) {
	ts := echoServer(t)
	s, err := Dial(context.Background(), &model.Request{URL: strings.Replace(ts.URL, "http://", "", 1)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	recv(t, s)
	s.Send(context.Background(), protocol.Outgoing{Data: "close-me"})
	m := recv(t, s)
	if m.Direction != "system" || m.Kind != "error" || !strings.Contains(m.Data, "4001 asked to") {
		t.Fatalf("close message = %+v", m)
	}
	<-s.Done()
	if s.Err() == nil {
		t.Fatal("expected an error for an abnormal server close")
	}
}

func TestDialFailures(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer plain.Close()
	if _, err := Dial(context.Background(), &model.Request{URL: plain.URL}, nil); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("handshake err = %v", err)
	}
	if _, err := Dial(context.Background(), &model.Request{URL: "ws://127.0.0.1:1"}, nil); err == nil {
		t.Fatal("expected connect error")
	}
}
