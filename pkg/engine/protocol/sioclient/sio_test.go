package sioclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/codeen15/pyro/pkg/engine/protocol"
)

func TestParsePacket(t *testing.T) {
	cases := map[string]packet{
		`2["chat","hi"]`:         {typ: '2', ns: "/", payload: `["chat","hi"]`},
		`2/admin,12["x"]`:        {typ: '2', ns: "/admin", payload: `["x"]`},
		`312["ok"]`:              {typ: '3', ns: "/", payload: `["ok"]`},
		`0{"sid":"a"}`:           {typ: '0', ns: "/", payload: `{"sid":"a"}`},
		`0/admin,`:               {typ: '0', ns: "/admin"},
		`51-["img",{"_p":true}]`: {typ: '5', attachments: 1, ns: "/", payload: `["img",{"_p":true}]`},
		`1/admin`:                {typ: '1', ns: "/admin"},
	}
	for in, want := range cases {
		got, err := parsePacket(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		got.ackID = nil
		if got != want {
			t.Errorf("%s: got %+v want %+v", in, got, want)
		}
	}
	if p, _ := parsePacket(`2/admin,12["x"]`); p.ackID == nil || *p.ackID != 12 {
		t.Errorf("ack id = %v", p.ackID)
	}
}

func TestArgsAndURL(t *testing.T) {
	for in, want := range map[string]string{
		``:            `[]`,
		`["a", 1]`:    `["a",1]`,
		`{"x":1}`:     `[{"x":1}]`,
		`42`:          `[42]`,
		`hello world`: `["hello world"]`,
	} {
		if got := argsArray(in); got != want {
			t.Errorf("argsArray(%q) = %s, want %s", in, got, want)
		}
	}
	u, err := engineURL("https://chat.example.com/ignored?token=abc", "", TransportWebSocket)
	if err != nil || u != "wss://chat.example.com/socket.io/?EIO=4&token=abc&transport=websocket" {
		t.Errorf("url = %s, %v", u, err)
	}
	if u, _ := engineURL("localhost:3000", "/realtime", TransportWebSocket); u != "ws://localhost:3000/realtime/?EIO=4&transport=websocket" {
		t.Errorf("custom path url = %s", u)
	}
	if _, err := engineURL("ftp://x", "", TransportWebSocket); err == nil {
		t.Error("expected scheme error")
	}
}

// fakeServer speaks just enough Engine.IO/Socket.IO: it accepts "/" and
// "/admin" (which requires auth), refuses others, echoes events as "echo"
// and acks events that ask for one.
func fakeServer(t *testing.T) *httptest.Server {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/socket.io/" || r.URL.Query().Get("EIO") != "4" {
			http.NotFound(w, r)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := context.Background()
		send := func(s string) { c.Write(ctx, websocket.MessageText, []byte(s)) }
		send(`0{"sid":"eio-1","pingInterval":25000,"pingTimeout":20000}`)
		send("2") // a ping the client must answer
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			s := string(data)
			if s == "3" || s[0] != '4' {
				continue
			}
			p, _ := parsePacket(s[1:])
			prefix := ""
			if p.ns != "/" {
				prefix = p.ns + ","
			}
			switch p.typ {
			case '0':
				switch {
				case p.ns == "/":
					send(`40{"sid":"sock-1"}`)
				case p.ns == "/admin" && strings.Contains(p.payload, "secret"):
					send(`40/admin,{"sid":"sock-2"}`)
				default:
					send(`44` + prefix + `{"message":"not authorized"}`)
				}
			case '2':
				event, args := splitEvent(p.payload)
				if event == "bye" {
					send("41" + prefix)
					continue
				}
				send(`42` + prefix + `["echo",` + strings.TrimSuffix(strings.TrimPrefix(args, "["), "]") + `]`)
				if p.ackID != nil {
					send("43" + prefix + itoa(*p.ackID) + `["got ` + event + `"]`)
				}
			}
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func itoa(i int) string { return strconv.Itoa(i) }

func next(t *testing.T, c *Client) protocol.Message {
	t.Helper()
	select {
	case m := <-c.Messages():
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("timed out")
	}
	return protocol.Message{}
}

func TestEventsAcksAndNamespaces(t *testing.T) {
	ts := fakeServer(t)
	ctx := context.Background()
	c, err := Dial(ctx, ts.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Info().SID != "sock-1" {
		t.Fatalf("sid = %q", c.Info().SID)
	}
	sent, err := c.Send(ctx, protocol.Outgoing{Event: "chat message", Data: `["hi", {"n": 1}]`, Ack: true})
	if err != nil || sent.AckID == nil || *sent.AckID != 0 {
		t.Fatalf("send = %+v %v", sent, err)
	}
	if m := next(t, c); m.Kind != "event" || m.Event != "echo" || m.Data != `["hi",{"n":1}]` {
		t.Fatalf("echo = %+v", m)
	}
	if m := next(t, c); m.Kind != "ack" || *m.AckID != 0 || m.Data != `["got chat message"]` || m.RTT <= 0 {
		t.Fatalf("ack = %+v", m)
	}
	if _, err := c.Send(ctx, protocol.Outgoing{Data: "x"}); err == nil {
		t.Fatal("expected error without event name")
	}
	c.Close()

	if _, err := Dial(ctx, ts.URL, Options{Namespace: "admin"}); err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("refused namespace err = %v", err)
	}
	a, err := Dial(ctx, ts.URL, Options{Namespace: "/admin", Auth: `{"token":"secret"}`})
	if err != nil {
		t.Fatal(err)
	}
	a.Send(ctx, protocol.Outgoing{Event: "bye"})
	if m := next(t, a); m.Direction != "system" || !strings.Contains(m.Data, "disconnected the namespace") {
		t.Fatalf("server disconnect = %+v", m)
	}
	<-a.Done()

	if _, err := Dial(ctx, ts.URL, Options{Path: "/wrong/"}); err == nil || !strings.Contains(err.Error(), "path") {
		t.Fatalf("wrong path err = %v", err)
	}
	if _, err := Dial(ctx, ts.URL, Options{Auth: "not json"}); err == nil {
		t.Fatal("expected auth JSON error")
	}
}

// TestRealServer checks conformance against the official Node server. Start
// it (see testdata/server.js) and set PYRO_SIO_URL, and PYRO_SIO_POLLING_URL
// for a server that only allows long-polling.
func TestRealServer(t *testing.T) {
	url := os.Getenv("PYRO_SIO_URL")
	if url == "" {
		t.Skip("PYRO_SIO_URL not set")
	}
	for _, tc := range []struct {
		url, transport, want string
	}{
		{url, TransportAuto, "websocket"},
		{url, TransportPolling, "polling"},
		{os.Getenv("PYRO_SIO_POLLING_URL"), TransportAuto, "polling"}, // WebSocket refused: falls back
	} {
		if tc.url == "" {
			continue
		}
		t.Run(tc.transport+"→"+tc.want, func(t *testing.T) { conformance(t, tc.url, tc.transport, tc.want) })
	}
	if _, err := Dial(context.Background(), url, Options{Namespace: "/admin"}); err == nil || !strings.Contains(err.Error(), "admins only") {
		t.Fatalf("admin namespace err = %v", err)
	}
	if p := os.Getenv("PYRO_SIO_POLLING_URL"); p != "" {
		if _, err := Dial(context.Background(), p, Options{Transport: TransportWebSocket}); err == nil {
			t.Fatal("websocket-only dial to a polling-only server should fail")
		}
	}
}

func conformance(t *testing.T, url, transport, want string) {
	ctx := context.Background()
	c, err := Dial(ctx, url, Options{Auth: `{"token":"pyro"}`, Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Info().Transport != want {
		t.Errorf("transport = %s, want %s", c.Info().Transport, want)
	}
	if m := next(t, c); m.Event != "welcome" || m.Data != `["pyro","`+want+`"]` {
		t.Fatalf("welcome = %+v", m)
	}
	c.Send(ctx, protocol.Outgoing{Event: "echo", Data: `[1, "two", {"three": 3}]`, Ack: true})
	got := map[string]protocol.Message{}
	for len(got) < 2 {
		m := next(t, c)
		got[m.Kind] = m
	}
	if got["event"].Event != "echo" || got["event"].Data != `[1,"two",{"three":3}]` {
		t.Fatalf("echo = %+v", got["event"])
	}
	if got["ack"].Data != `["ok",3]` {
		t.Fatalf("ack = %+v", got["ack"])
	}

	// Binary both ways: a buffer argument, echoed with its length, and a
	// binary ack.
	if _, err := c.Send(ctx, protocol.Outgoing{Event: "bin", Data: `[{"$binary":"AAEC/w=="}, "meta"]`, Ack: true}); err != nil {
		t.Fatal(err)
	}
	got = map[string]protocol.Message{}
	for len(got) < 2 {
		m := next(t, c)
		got[m.Kind] = m
	}
	if got["event"].Data != `[{"$binary":"AAEC/w=="},{"len":4,"meta":"meta"}]` || got["event"].Size < 4 {
		t.Fatalf("binary echo = %+v", got["event"])
	}
	if got["ack"].Data != `[{"$binary":"CQgH"}]` {
		t.Fatalf("binary ack = %+v", got["ack"])
	}
	// A binary value nested in an object.
	c.Send(ctx, protocol.Outgoing{Event: "sendme"})
	if m := next(t, c); m.Event != "file" || m.Data != `[{"data":{"$binary":"aGkgdGhlcmU="},"name":"a.bin"}]` {
		t.Fatalf("nested binary = %+v", m)
	}
}

// TestRealServerKeepalive idles past several pings (start the server with
// PING_MS=500) and checks the session is still alive on each transport.
func TestRealServerKeepalive(t *testing.T) {
	url := os.Getenv("PYRO_SIO_FASTPING_URL")
	if url == "" {
		t.Skip("PYRO_SIO_FASTPING_URL not set")
	}
	for _, tr := range []string{TransportWebSocket, TransportPolling} {
		t.Run(tr, func(t *testing.T) {
			ctx := context.Background()
			c, err := Dial(ctx, url, Options{Transport: tr})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			next(t, c) // welcome
			time.Sleep(3 * time.Second)
			if c.Err() != nil {
				t.Fatalf("session died while idle: %v", c.Err())
			}
			c.Send(ctx, protocol.Outgoing{Event: "echo", Data: `["still here"]`})
			if m := next(t, c); m.Data != `["still here"]` {
				t.Fatalf("after idle: %+v", m)
			}
		})
	}
}
