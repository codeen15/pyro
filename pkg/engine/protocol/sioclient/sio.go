// Package sioclient is a small Socket.IO client: Engine.IO v4 (WebSocket or
// HTTP long-polling transport) carrying Socket.IO v5 packets (servers 3.x
// and 4.x). It supports namespaces, CONNECT auth, events, acks and binary
// attachments. Binary values appear in message data as {"$binary": base64}
// and are sent the same way.
package sioclient

import (
	"bytes"
	"context"
	"encoding/base64"
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
	eioNoop    = '6'
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

// Transports.
const (
	TransportAuto      = "auto" // WebSocket, falling back to polling
	TransportWebSocket = "websocket"
	TransportPolling   = "polling"
)

type Options struct {
	Path      string // default /socket.io/
	Namespace string // default /
	// Auth is the JSON object sent with CONNECT (Socket.IO v4 auth).
	Auth    string
	Headers []model.KeyValue
	Client  *http.Client
	// Transport is auto (default), websocket or polling.
	Transport string
}

// Dial connects and waits until the namespace is joined.
func Dial(ctx context.Context, rawURL string, opts Options) (*Client, error) {
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
	mode := opts.Transport
	if mode == "" {
		mode = TransportAuto
	}
	if mode != TransportAuto && mode != TransportWebSocket && mode != TransportPolling {
		return nil, fmt.Errorf("unknown Socket.IO transport %q (auto, websocket or polling)", mode)
	}

	start := time.Now()
	var (
		tr    transport
		open  []byte
		info  protocol.SessionInfo
		wsErr error
	)
	if mode != TransportPolling {
		tr, open, info, wsErr = dialWS(ctx, rawURL, opts.Path, header, opts.Client)
		if wsErr != nil && (mode == TransportWebSocket || ctx.Err() != nil) {
			return nil, wsErr
		}
	}
	if tr == nil {
		var err error
		tr, open, info, err = dialPolling(ctx, rawURL, opts.Path, header, opts.Client)
		if err != nil {
			if wsErr != nil {
				return nil, fmt.Errorf("%v; polling fallback: %v", wsErr, err)
			}
			return nil, err
		}
	}
	info.Transport = tr.name()

	c := &Client{
		tr: tr, ns: ns, msgs: make(chan protocol.Message, 1024), done: make(chan struct{}),
		pendingAcks: map[int]time.Time{}, info: info,
	}
	var hello struct {
		SID string `json:"sid"`
	}
	_ = json.Unmarshal(open[1:], &hello)
	c.info.SID = hello.SID

	// Socket.IO CONNECT to the namespace.
	connect := string(eioMessage) + "0"
	if ns != "/" {
		connect += ns + ","
	}
	connect += opts.Auth
	if err := tr.writeText(ctx, connect); err != nil {
		tr.close(false)
		return nil, err
	}
	for {
		data, binary, err := tr.read(ctx)
		if err != nil {
			tr.close(false)
			return nil, fmt.Errorf("waiting for Socket.IO connect: %w", err)
		}
		if binary || len(data) == 0 {
			continue
		}
		switch data[0] {
		case eioPing:
			_ = tr.writeText(ctx, string(eioPong)+string(data[1:]))
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
			tr.close(true)
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

func dialWS(ctx context.Context, rawURL, path string, header http.Header, client *http.Client) (transport, []byte, protocol.SessionInfo, error) {
	var info protocol.SessionInfo
	wsURL, err := engineURL(rawURL, path, TransportWebSocket)
	if err != nil {
		return nil, nil, info, err
	}
	info.URL = wsURL
	conn, res, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: header, HTTPClient: client})
	if err != nil {
		if res != nil {
			return nil, nil, info, fmt.Errorf("Socket.IO handshake failed: server answered %s (is the path %q right?)", res.Status, pathOr(path))
		}
		return nil, nil, info, fmt.Errorf("could not connect to %s: %w", wsURL, err)
	}
	conn.SetReadLimit(16 << 20)
	if res != nil {
		info.Status = res.StatusCode
	}
	tr := &wsTransport{conn: conn}
	data, _, err := tr.read(ctx)
	if err != nil || len(data) == 0 || data[0] != eioOpen {
		tr.close(false)
		return nil, nil, info, fmt.Errorf("not a Socket.IO (Engine.IO v4) server: expected an open packet")
	}
	return tr, data, info, nil
}

func dialPolling(ctx context.Context, rawURL, path string, header http.Header, client *http.Client) (transport, []byte, protocol.SessionInfo, error) {
	var info protocol.SessionInfo
	raw, err := engineURL(rawURL, path, TransportPolling)
	if err != nil {
		return nil, nil, info, err
	}
	u, _ := url.Parse(raw)
	info.URL = raw
	if client == nil {
		client = http.DefaultClient
	}
	tr := newPollTransport(u, header, client)
	open, err := tr.handshake(ctx)
	if err != nil {
		tr.close(false)
		return nil, nil, info, fmt.Errorf("Socket.IO polling handshake (%s): %w", raw, err)
	}
	var hello struct {
		SID string `json:"sid"`
	}
	if json.Unmarshal(open[1:], &hello) != nil || hello.SID == "" {
		tr.close(false)
		return nil, nil, info, errors.New("Socket.IO polling handshake: no session id")
	}
	tr.setSID(hello.SID)
	info.Status = http.StatusOK
	return tr, open, info, nil
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
func engineURL(raw, path, transport string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid Socket.IO URL %q", raw)
	}
	secure := false
	switch u.Scheme {
	case "http", "ws":
	case "https", "wss":
		secure = true
	default:
		return "", fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	switch {
	case transport == TransportPolling && secure:
		u.Scheme = "https"
	case transport == TransportPolling:
		u.Scheme = "http"
	case secure:
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	u.Path = pathOr(path)
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	q := u.Query()
	q.Set("EIO", "4")
	q.Set("transport", transport)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

type packet struct {
	typ         byte
	attachments int
	ns          string
	ackID       *int
	payload     string
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
			p.attachments, _ = strconv.Atoi(s[:i])
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
	tr   transport
	ns   string
	info protocol.SessionInfo
	msgs chan protocol.Message
	done chan struct{}

	mu      sync.Mutex
	seq     int
	nextAck int
	// pendingAcks maps ack id to emit time, for round-trip measurement.
	pendingAcks map[int]time.Time
	err         error
	closing     bool

	// A binary packet waiting for its attachments (read loop only).
	pending     *packet
	attachments [][]byte
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
	return c.tr.writeText(ctx, string(typ)+payload)
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
		data, binary, err := c.tr.read(ctx)
		if err != nil {
			c.finish(err, "")
			return
		}
		if binary {
			c.attachment(data)
			continue
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

// attachment collects a binary frame for the pending binary packet.
func (c *Client) attachment(data []byte) {
	if c.pending == nil {
		return
	}
	c.attachments = append(c.attachments, append([]byte(nil), data...))
	if len(c.attachments) < c.pending.attachments {
		return
	}
	p := *c.pending
	payload, size := fillPlaceholders(p.payload, c.attachments)
	c.pending, c.attachments = nil, nil
	if p.typ == sioBinaryEvent {
		p.typ = sioEvent
	} else {
		p.typ = sioAck
	}
	p.payload = payload
	c.dispatch(p, size)
}

func (c *Client) handle(raw string) {
	p, err := parsePacket(raw)
	if err != nil || p.ns != c.ns {
		return
	}
	if (p.typ == sioBinaryEvent || p.typ == sioBinaryAck) && p.attachments > 0 {
		c.pending, c.attachments = &p, nil
		return
	}
	switch p.typ {
	case sioBinaryEvent:
		p.typ = sioEvent
	case sioBinaryAck:
		p.typ = sioAck
	}
	c.dispatch(p, len(p.payload))
}

func (c *Client) dispatch(p packet, size int) {
	switch p.typ {
	case sioEvent:
		event, args := splitEvent(p.payload)
		c.deliver(protocol.Message{Direction: "received", Kind: "event", Event: event, Data: args, AckID: p.ackID, Size: size})
		// The server asked for an ack; answer with no arguments so it doesn't hang.
		if p.ackID != nil {
			_ = c.writeEIO(context.Background(), eioMessage, c.frame(sioAck, 0, p.ackID, "[]"))
		}
	case sioAck:
		m := protocol.Message{Direction: "received", Kind: "ack", Data: p.payload, AckID: p.ackID, Size: size}
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
		c.tr.close(true)
	}
}

// fillPlaceholders replaces {"_placeholder":true,"num":i} with
// {"$binary": base64(attachment i)} and returns the total attachment size.
func fillPlaceholders(payload string, atts [][]byte) (string, int) {
	dec := json.NewDecoder(strings.NewReader(payload))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return payload, len(payload)
	}
	size := len(payload)
	for _, a := range atts {
		size += len(a)
	}
	var walk func(any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case map[string]any:
			if ph, _ := t["_placeholder"].(bool); ph {
				if n, err := t["num"].(json.Number).Int64(); err == nil && int(n) < len(atts) {
					return map[string]any{"$binary": base64.StdEncoding.EncodeToString(atts[n])}
				}
			}
			for k, e := range t {
				t[k] = walk(e)
			}
		case []any:
			for i, e := range t {
				t[i] = walk(e)
			}
		}
		return x
	}
	out, err := marshalNoEscape(walk(v))
	if err != nil {
		return payload, size
	}
	return out, size
}

// extractBinary replaces {"$binary": base64} values with placeholders and
// returns the attachments, for sending.
func extractBinary(payload string) (string, [][]byte, error) {
	if !strings.Contains(payload, `"$binary"`) {
		return payload, nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(payload))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return payload, nil, nil
	}
	var atts [][]byte
	var bad error
	var walk func(any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case map[string]any:
			if b64, ok := t["$binary"].(string); ok && len(t) == 1 {
				b, err := base64.StdEncoding.DecodeString(b64)
				if err != nil {
					bad = fmt.Errorf("$binary must be base64: %w", err)
					return x
				}
				atts = append(atts, b)
				return map[string]any{"_placeholder": true, "num": len(atts) - 1}
			}
			for k, e := range t {
				t[k] = walk(e)
			}
		case []any:
			for i, e := range t {
				t[i] = walk(e)
			}
		}
		return x
	}
	w := walk(v)
	if bad != nil {
		return "", nil, bad
	}
	out, err := marshalNoEscape(w)
	return out, atts, err
}

func marshalNoEscape(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
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

// frame builds a Socket.IO packet: type, attachment count for binary types,
// namespace, ack id, payload.
func (c *Client) frame(typ byte, attachments int, ackID *int, payload string) string {
	var b strings.Builder
	b.WriteByte(typ)
	if typ == sioBinaryEvent || typ == sioBinaryAck {
		b.WriteString(strconv.Itoa(attachments))
		b.WriteByte('-')
	}
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
// Values written {"$binary": base64} go as binary attachments; with Binary
// set, Data is base64 and is sent as one binary argument.
func (c *Client) Send(ctx context.Context, out protocol.Outgoing) (protocol.Message, error) {
	if out.Event == "" {
		return protocol.Message{}, errors.New("Socket.IO messages need an event name")
	}
	args := argsArray(out.Data)
	if out.Binary {
		if _, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out.Data)); err != nil {
			return protocol.Message{}, fmt.Errorf("binary messages must be base64: %w", err)
		}
		args = `[{"$binary":"` + strings.TrimSpace(out.Data) + `"}]`
	}
	name, _ := json.Marshal(out.Event)
	payload := "[" + string(name)
	if args != "[]" {
		payload += "," + args[1:len(args)-1]
	}
	payload += "]"
	wire, atts, err := extractBinary(payload)
	if err != nil {
		return protocol.Message{}, err
	}
	size := len(wire)
	for _, a := range atts {
		size += len(a)
	}

	m := protocol.Message{Direction: "sent", Kind: "event", Event: out.Event, Data: args, Size: size}
	var ackID *int
	if out.Ack {
		c.mu.Lock()
		id := c.nextAck
		c.nextAck++
		c.pendingAcks[id] = time.Now()
		c.mu.Unlock()
		ackID, m.AckID = &id, &id
	}
	typ := byte(sioEvent)
	if len(atts) > 0 {
		typ = sioBinaryEvent
	}
	// Stamped before writing: a fast reply or ack must come after the emit.
	m = c.stamp(m)
	if err := c.writeEIO(ctx, eioMessage, c.frame(typ, len(atts), ackID, wire)); err != nil {
		return m, fmt.Errorf("emit failed: %w", err)
	}
	for _, a := range atts {
		if err := c.tr.writeBinary(ctx, a); err != nil {
			return m, fmt.Errorf("emit failed: %w", err)
		}
	}
	return m, nil
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
	_ = c.writeEIO(context.Background(), eioMessage, c.frame(sioDisconnect, 0, nil, ""))
	c.tr.close(true)
	<-c.done
	return nil
}
