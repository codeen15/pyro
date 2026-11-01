package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/codeen15/pyro/pkg/engine/load"
)

// silence is how long a running agent may go without reporting before the
// controller gives up on it (it reports every tick).
const silence = 30 * time.Second

// Client is the controller's handle on one agent; it implements load.Worker.
type Client struct {
	addr  string // http(s)://host:port
	token string
	http  *http.Client

	mu   sync.Mutex
	conn *websocket.Conn
}

// NewClient connects to an agent at addr: host:port, or a URL with an
// http(s):// or ws(s):// scheme.
func NewClient(addr, token string, insecureTLS bool) *Client {
	a := addr
	switch {
	case strings.HasPrefix(a, "ws://"):
		a = "http://" + a[5:]
	case strings.HasPrefix(a, "wss://"):
		a = "https://" + a[6:]
	case !strings.Contains(a, "://"):
		a = "http://" + a
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecureTLS {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &Client{addr: strings.TrimRight(a, "/"), token: token, http: &http.Client{Transport: tr}}
}

func (c *Client) Name() string {
	_, host, _ := strings.Cut(c.addr, "://")
	return host
}

func (c *Client) header() http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+c.token)
	h.Set(protocolHeader, strconv.Itoa(Protocol))
	return h
}

// Info asks the agent about itself (and checks the token).
func (c *Client) Info(ctx context.Context) (*Info, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.addr+"/agent/v1/info", nil)
	if err != nil {
		return nil, err
	}
	req.Header = c.header()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var in Info
	if err := json.NewDecoder(resp.Body).Decode(&in); err != nil {
		return nil, err
	}
	return &in, nil
}

func (c *Client) Prepare(ctx context.Context, job load.Job) error {
	conn, resp, err := websocket.Dial(ctx, c.addr+"/agent/v1/job", &websocket.DialOptions{HTTPClient: c.http, HTTPHeader: c.header()})
	if err != nil {
		if resp != nil && resp.Body != nil {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			if len(b) > 0 {
				return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
			}
		}
		return err
	}
	if err := prepareConn(ctx, conn, job); err != nil {
		conn.CloseNow()
		return err
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	return nil
}

// prepareConn sends a job over an agent connection and waits for "ready".
func prepareConn(ctx context.Context, conn *websocket.Conn, job load.Job) error {
	conn.SetReadLimit(64 << 20)
	if err := wsjson.Write(ctx, conn, message{Type: "job", Job: &job}); err != nil {
		return err
	}
	var m message
	if err := wsjson.Read(ctx, conn, &m); err != nil {
		return err
	}
	switch m.Type {
	case "ready":
		return nil
	case "error":
		return errors.New(m.Error)
	}
	return fmt.Errorf("unexpected %q from the agent", m.Type)
}

func (c *Client) Run(ctx context.Context, report func(load.Report)) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return errors.New("not prepared")
	}
	defer c.Close()
	return runConn(ctx, conn, report)
}

// runConn starts a prepared job and relays its reports until "done".
func runConn(ctx context.Context, conn *websocket.Conn, report func(load.Report)) error {
	if err := wsjson.Write(ctx, conn, message{Type: "begin"}); err != nil {
		return err
	}
	for {
		rctx, cancel := context.WithTimeout(ctx, silence)
		var m message
		err := wsjson.Read(rctx, conn, &m)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("no report for %s", silence)
			}
			if errors.Is(err, io.EOF) || websocket.CloseStatus(err) != -1 {
				return errors.New("connection lost")
			}
			return fmt.Errorf("connection lost: %w", err)
		}
		switch m.Type {
		case "report":
			if m.Report != nil {
				report(*m.Report)
			}
		case "done":
			return nil
		case "error":
			return errors.New(m.Error)
		}
	}
}

func (c *Client) Stop() {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	stopConn(conn)
}

func stopConn(conn *websocket.Conn) {
	if conn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		wsjson.Write(ctx, conn, message{Type: "stop"})
	}
}

func (c *Client) Close() {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()
	if conn != nil {
		conn.Close(websocket.StatusNormalClosure, "")
	}
}
