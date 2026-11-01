package sioclient

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// transport moves Engine.IO packets. Text packets start with their type
// character; binary packets are raw attachment bytes (Engine.IO v4 sends
// them without a type).
type transport interface {
	read(ctx context.Context) (data []byte, binary bool, err error)
	writeText(ctx context.Context, packets ...string) error
	writeBinary(ctx context.Context, data []byte) error
	close(normal bool)
	name() string
}

// --- WebSocket ---------------------------------------------------------------

type wsTransport struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (t *wsTransport) name() string { return "websocket" }

func (t *wsTransport) read(ctx context.Context) ([]byte, bool, error) {
	typ, data, err := t.conn.Read(ctx)
	return data, typ == websocket.MessageBinary, err
}

func (t *wsTransport) writeText(ctx context.Context, packets ...string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, p := range packets {
		if err := t.conn.Write(ctx, websocket.MessageText, []byte(p)); err != nil {
			return err
		}
	}
	return nil
}

func (t *wsTransport) writeBinary(ctx context.Context, data []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.conn.Write(ctx, websocket.MessageBinary, data)
}

func (t *wsTransport) close(normal bool) {
	if normal {
		_ = t.conn.Close(websocket.StatusNormalClosure, "")
	} else {
		_ = t.conn.CloseNow()
	}
}

// --- HTTP long-polling ---------------------------------------------------------

// pollTransport is Engine.IO v4 long-polling: GET waits for packets, POST
// sends them; packets are separated by \x1e and binary ones are "b"+base64.
type pollTransport struct {
	client *http.Client
	base   *url.URL // with EIO=4&transport=polling (and sid once known)
	header http.Header

	mu      sync.Mutex // serialises POSTs
	life    context.Context
	cancel  context.CancelFunc
	queue   [][]byte // packets received but not yet read
	binary  []bool
	closed  chan struct{}
	closeMu sync.Once
}

const recordSep = "\x1e"

func (t *pollTransport) name() string { return "polling" }

func (t *pollTransport) url() string {
	u := *t.base
	q := u.Query()
	q.Set("t", fmt.Sprintf("%d", time.Now().UnixNano())) // defeat caches
	u.RawQuery = q.Encode()
	return u.String()
}

func (t *pollTransport) do(ctx context.Context, method string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, t.url(), body)
	if err != nil {
		return nil, err
	}
	for k, v := range t.header {
		req.Header[k] = v
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("polling %s: server answered %s: %.200s", method, resp.Status, data)
	}
	return data, nil
}

// handshake opens a polling session and returns the open packet.
func (t *pollTransport) handshake(ctx context.Context) ([]byte, error) {
	data, err := t.do(ctx, http.MethodGet, nil)
	if err != nil {
		return nil, err
	}
	t.enqueue(data)
	if len(t.queue) == 0 || len(t.queue[0]) == 0 || t.queue[0][0] != eioOpen {
		return nil, errors.New("not a Socket.IO (Engine.IO v4) server: expected an open packet")
	}
	open := t.queue[0]
	t.queue, t.binary = t.queue[1:], t.binary[1:]
	return open, nil
}

func (t *pollTransport) setSID(sid string) {
	q := t.base.Query()
	q.Set("sid", sid)
	t.base.RawQuery = q.Encode()
}

func (t *pollTransport) enqueue(payload []byte) {
	for _, p := range strings.Split(string(payload), recordSep) {
		if p == "" {
			continue
		}
		if p[0] == 'b' {
			if b, err := base64.StdEncoding.DecodeString(p[1:]); err == nil {
				t.queue, t.binary = append(t.queue, b), append(t.binary, true)
			}
			continue
		}
		t.queue, t.binary = append(t.queue, []byte(p)), append(t.binary, false)
	}
}

// read returns queued packets first, then long-polls for more. Only the
// read loop calls it.
func (t *pollTransport) read(ctx context.Context) ([]byte, bool, error) {
	for len(t.queue) == 0 {
		select {
		case <-t.closed:
			return nil, false, io.EOF
		default:
		}
		data, err := t.do(t.life, http.MethodGet, nil)
		if err != nil {
			select {
			case <-t.closed:
				return nil, false, io.EOF
			default:
			}
			return nil, false, err
		}
		t.enqueue(data)
	}
	p, b := t.queue[0], t.binary[0]
	t.queue, t.binary = t.queue[1:], t.binary[1:]
	return p, b, nil
}

func (t *pollTransport) post(ctx context.Context, packets []string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, err := t.do(ctx, http.MethodPost, strings.NewReader(strings.Join(packets, recordSep)))
	return err
}

func (t *pollTransport) writeText(ctx context.Context, packets ...string) error {
	return t.post(ctx, packets)
}

func (t *pollTransport) writeBinary(ctx context.Context, data []byte) error {
	return t.post(ctx, []string{"b" + base64.StdEncoding.EncodeToString(data)})
}

func (t *pollTransport) close(normal bool) {
	t.closeMu.Do(func() {
		if normal {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = t.post(ctx, []string{string(eioClose)})
			cancel()
		}
		close(t.closed)
		t.cancel()
	})
}

func newPollTransport(base *url.URL, header http.Header, client *http.Client) *pollTransport {
	t := &pollTransport{client: client, base: base, header: header, closed: make(chan struct{})}
	t.life, t.cancel = context.WithCancel(context.Background())
	return t
}
