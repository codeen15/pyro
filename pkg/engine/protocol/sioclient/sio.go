// Package sioclient is a small Socket.IO client: Engine.IO v4 over the
// WebSocket transport carrying Socket.IO v5 packets (servers 3.x and 4.x).
// It supports namespaces, CONNECT auth, events and acks. Binary attachments
// and the long-polling transport are not supported yet.
package sioclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/protocol"
)

// Engine.IO packet types.
const (
	eioOpen    = '0'
	eioClose   = '1'
	eioPing    = '2'
	eioPong    = '3'
	eioMessage = '4'
)

// Socket.IO packet types.
const (
	sioConnect      = '0'
	sioDisconnect   = '1'
	sioEvent        = '2'
	sioAck          = '3'
	sioConnectError = '4'
	sioBinaryEvent  = '5'
	sioBinaryAck    = '6'
)

type Options struct {
	Path      string // default /socket.io/
	Namespace string // default /
	// Auth is the JSON object sent with CONNECT (Socket.IO v4 auth).
	Auth    string
	Headers []model.KeyValue
	Client  *http.Client
}

// Dial connects and waits until the namespace is joined.
func Dial(ctx context.Context, rawURL string, opts Options) (*Client, error) {
	wsURL, err := engineURL(rawURL, opts.Path)
	if err != nil {
		return nil, err
	}
	ns := opts.Namespace
	if ns == "" {
		ns = "/"
	}
	if !strings.HasPrefix(ns, "/") {
		ns = "/" + ns
	}
	if opts.Auth != "" && !json.Valid([]byte(opts.Auth)) {
		return nil, errors.New("Socket.IO auth must be a JSON object")
	}
	header := http.Header{}
	for _, h := range opts.Headers {
		if !h.Disabled && h.Key != "" {
			header.Add(h.Key, h.Value)
		}
	}

	start := time.Now()
	conn, res, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: header, HTTPClient: opts.Client})
	if err != nil {
		if res != nil {
			return nil, fmt.Errorf("Socket.IO handshake failed: server answered %s (is the path %q right?)", res.Status, pathOr(opts.Path))
		}
		return nil, fmt.Errorf("could not connect to %s: %w", wsURL, err)
	}
	conn.SetReadLimit(16 << 20)
	c := &Client{
		conn: conn, ns: ns, msgs: make(chan protocol.Message, 1024), done: make(chan struct{}),
		pendingAcks: map[int]time.Time{}, info: protocol.SessionInfo{URL: wsURL},
	}
	if res != nil {
		c.info.Status = res.StatusCode
	}

	// Engine.IO OPEN.
	_, data, err := conn.Read(ctx)
	if err != nil || len(data) == 0 || data[0] != eioOpen {
		conn.Close(websocket.StatusProtocolError, "")
		return nil, fmt.Errorf("not a Socket.IO (Engine.IO v4) server: expected an open packet")
	}
	var open struct {
		SID          string `json:"sid"`
		PingInterval int    `json:"pingInterval"`
		PingTimeout  int    `json:"pingTimeout"`
	}
	_ = json.Unmarshal(data[1:], &open)
	c.info.SID = open.SID

	// Socket.IO CONNECT to the namespace.
	connect := "0"
	if ns != "/" {
		connect += ns + ","
	}
	connect += opts.Auth
	if err := c.writeEIO(ctx, eioMessage, connect); err != nil {
		conn.Close(websocket.StatusInternalError, "")
		return nil, err
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return nil, fmt.Errorf("waiting for Socket.IO connect: %w", err)
		}
		if len(data) == 0 {
			continue
		}
		switch data[0] {
		case eioPing:
			_ = c.writeEIO(ctx, eioPong, string(data[1:]))
			continue
		case eioMessage:
		default:
			continue
		}
		p, err := parsePacket(string(data[1:]))
		if err != nil || p.ns != ns {
			continue
		}
		switch p.typ {
		case sioConnect:
			var ack struct {
				SID string `json:"sid"`
			}
			_ = json.Unmarshal([]byte(p.payload), &ack)
			if ack.SID != "" {
				c.info.SID = ack.SID
			}
			c.info.ConnectTime = time.Since(start)
			go c.readLoop()
			return c, nil
		case sioConnectError:
			conn.Close(websocket.StatusNormalClosure, "")
			var e struct {
				Message string `json:"message"`
			}
			if json.Unmarshal([]byte(p.payload), &e) == nil && e.Message != "" {
				return nil, fmt.Errorf("server refused namespace %s: %s", ns, e.Message)
			}
			return nil, fmt.Errorf("server refused namespace %s: %s", ns, p.payload)
		}
	}
}

func pathOr(p string) string {
	if p == "" {
		return "/socket.io/"
	}
	return p
}

// engineURL converts http(s)://host/ns?query into the Engine.IO websocket
// endpoint. The URL path is ignored (Socket.IO uses it for nothing); the
// namespace comes from Options.
func engineURL(raw, path string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid Socket.IO URL %q", raw)
	}
	switch u.Scheme {
	case "http", "ws":
		u.Scheme = "ws"
	case "https", "wss":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	u.Path = pathOr(path)
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	q := u.Query()
	q.Set("EIO", "4")
	q.Set("transport", "websocket")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

type packet struct {
	typ     byte
	ns      string
	ackID   *int
	payload string
}

// parsePacket decodes "<type>[<n>-][<ns>,][<id>][<json>]".
func parsePacket(s string) (packet, error) {
	if s == "" {
		return packet{}, errors.New("empty packet")
	}
	p := packet{typ: s[0], ns: "/"}
	s = s[1:]
	if p.typ == sioBinaryEvent || p.typ == sioBinaryAck {
		if i := strings.IndexByte(s, '-'); i >= 0 {
			s = s[i+1:]
		}
	}
	if strings.HasPrefix(s, "/") {
		end := strings.IndexByte(s, ',')
		if end < 0 {
			p.ns, s = s, ""
		} else {
			p.ns, s = s[:end], s[end+1:]
		}
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i > 0 {
		id, _ := strconv.Atoi(s[:i])
		p.ackID = &id
		s = s[i:]
	}
	p.payload = s
	return p, nil
}

// Client is a connected Socket.IO namespace. It implements protocol.Session.
type Client struct {
	conn *websocket.Conn
	ns   string
	info protocol.SessionInfo
	msgs chan protocol.Message
	done chan struct{}

	writeMu sync.Mutex
	mu      sync.Mutex
	seq     int
	nextAck int
	// pendingAcks maps ack id to emit time, for round-trip measurement.
	pendingAcks map[int]time.Time
	err         error
	closing     bool
}

func (c *Client) Messages() <-chan protocol.Message { return c.msgs }
func (c *Client) Done() <-chan struct{}             { return c.done }
func (c *Client) Info() protocol.SessionInfo        { return c.info }

func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Client) writeEIO(ctx context.Context, typ byte, payload string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.Write(ctx, websocket.MessageText, []byte(string(typ)+payload))
}

func (c *Client) stamp(m protocol.Message) protocol.Message {
	c.mu.Lock()
	c.seq++
	m.Seq = c.seq
	c.mu.Unlock()
	m.At = time.Now()
	return m
}

func (c *Client) deliver(m protocol.Message) {
	m = c.stamp(m)
	select {
	case c.msgs <- m:
	default:
		// Reader fell behind: drop the oldest message to make room.
		select {
		case <-c.msgs:
		default:
		}
		select {
		case c.msgs <- m:
		default:
		}
	}
}

func (c *Client) readLoop() {
	defer close(c.msgs)
	defer close(c.done)
	ctx := context.Background()
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			c.finish(err, "")
			return
		}
		if len(data) == 0 {
			continue
		}
		switch data[0] {
		case eioPing:
			_ = c.writeEIO(ctx, eioPong, string(data[1:]))
		case eioClose:
			c.finish(nil, "server closed the Engine.IO session")
			return
		case eioMessage:
			c.handle(string(data[1:]))
		}
	}
}

func (c *Client) handle(raw string) {
	p, err := parsePacket(raw)
	if err != nil || p.ns != c.ns {
		return
	}
	switch p.typ {
	case sioEvent:
		event, args := splitEvent(p.payload)
		c.deliver(protocol.Message{Direction: "received", Kind: "event", Event: event, Data: args, AckID: p.ackID, Size: len(p.payload)})
		// The server asked for an ack; answer with no arguments so it doesn't hang.
		if p.ackID != nil {
			_ = c.writeEIO(context.Background(), eioMessage, c.frame(sioAck, p.ackID, "[]"))
		}
	case sioAck:
		m := protocol.Message{Direction: "received", Kind: "ack", Data: p.payload, AckID: p.ackID, Size: len(p.payload)}
		if p.ackID != nil {
			c.mu.Lock()
			if t, ok := c.pendingAcks[*p.ackID]; ok {
				m.RTT = time.Since(t)
				delete(c.pendingAcks, *p.ackID)
			}
			c.mu.Unlock()
		}
		c.deliver(m)
	case sioDisconnect:
		c.finish(nil, "server disconnected the namespace")
		c.conn.Close(websocket.StatusNormalClosure, "")
	case sioBinaryEvent, sioBinaryAck:
		c.deliver(protocol.Message{Direction: "system", Kind: "error", Data: "received a binary Socket.IO packet; binary attachments are not supported yet"})
	}
}

// splitEvent turns `["name", a, b]` into ("name", `[a,b]`).
func splitEvent(payload string) (string, string) {
	var arr []json.RawMessage
	if json.Unmarshal([]byte(payload), &arr) != nil || len(arr) == 0 {
		return "", payload
	}
	var name string
	_ = json.Unmarshal(arr[0], &name)
	rest, _ := json.Marshal(arr[1:])
	return name, string(rest)
}

func (c *Client) frame(typ byte, ackID *int, payload string) string {
	var b strings.Builder
	b.WriteByte(typ)
	if c.ns != "/" {
		b.WriteString(c.ns)
		b.WriteByte(',')
	}
	if ackID != nil {
		b.WriteString(strconv.Itoa(*ackID))
	}
	b.WriteString(payload)
	return b.String()
}

// Send emits an event. Data is a JSON array of arguments; any other JSON
// value is sent as the single argument, and non-JSON text as a string.
func (c *Client) Send(ctx context.Context, out protocol.Outgoing) (protocol.Message, error) {
	if out.Event == "" {
		return protocol.Message{}, errors.New("Socket.IO messages need an event name")
	}
	if out.Binary {
		return protocol.Message{}, errors.New("binary Socket.IO payloads are not supported yet")
	}
	args := argsArray(out.Data)
	name, _ := json.Marshal(out.Event)
	payload := "[" + string(name)
	if args != "[]" {
		payload += "," + args[1:len(args)-1]
	}
	payload += "]"

	m := protocol.Message{Direction: "sent", Kind: "event", Event: out.Event, Data: args, Size: len(payload)}
	var ackID *int
	if out.Ack {
		c.mu.Lock()
		id := c.nextAck
		c.nextAck++
		c.pendingAcks[id] = time.Now()
		c.mu.Unlock()
		ackID, m.AckID = &id, &id
	}
	if err := c.writeEIO(ctx, eioMessage, c.frame(sioEvent, ackID, payload)); err != nil {
		return m, fmt.Errorf("emit failed: %w", err)
	}
	return c.stamp(m), nil
}

func argsArray(data string) string {
	data = strings.TrimSpace(data)
	if data == "" {
		return "[]"
	}
	if json.Valid([]byte(data)) {
		if strings.HasPrefix(data, "[") {
			var compact []json.RawMessage
			if json.Unmarshal([]byte(data), &compact) == nil {
				b, _ := json.Marshal(compact)
				return string(b)
			}
		}
		return "[" + data + "]"
	}
	b, _ := json.Marshal(data)
	return "[" + string(b) + "]"
}

func (c *Client) finish(err error, why string) {
	c.mu.Lock()
	if c.err != nil || (c.closing && err != nil) {
		c.mu.Unlock()
		if c.closing {
			c.deliver(protocol.Message{Direction: "system", Kind: "close", Data: "disconnected"})
		}
		return
	}
	if err != nil {
		if s := websocket.CloseStatus(err); s == websocket.StatusNormalClosure {
			err = nil
			why = "server closed the connection"
		} else {
			err = fmt.Errorf("connection lost: %v", err)
		}
	}
	c.err = err
	c.mu.Unlock()
	if err != nil {
		c.deliver(protocol.Message{Direction: "system", Kind: "error", Data: err.Error()})
		return
	}
	c.deliver(protocol.Message{Direction: "system", Kind: "close", Data: why})
}

// Close leaves the namespace and closes the connection.
func (c *Client) Close() error {
	c.mu.Lock()
	c.closing = true
	c.mu.Unlock()
	_ = c.writeEIO(context.Background(), eioMessage, c.frame(sioDisconnect, nil, ""))
	_ = c.conn.Close(websocket.StatusNormalClosure, "")
	<-c.done
	return nil
}
