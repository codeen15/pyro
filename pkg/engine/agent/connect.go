package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// Connect runs the agent in dial-out mode: it connects to a controller's
// agent listener, waits for a job, runs it, and reconnects (with backoff
// when the controller is unreachable) until ctx ends.
func (s *Server) Connect(ctx context.Context, controller string, insecureTLS bool) error {
	u := controller
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + u[8:]
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + u[7:]
	case !strings.Contains(u, "://"):
		u = "ws://" + u
	}
	u = strings.TrimRight(u, "/") + "/agent/v1/connect"
	client := http.DefaultClient
	if insecureTLS {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		client = &http.Client{Transport: tr}
	}
	backoff := time.Second
	for ctx.Err() == nil {
		err := s.connectOnce(ctx, u, client)
		switch {
		case err == nil:
			backoff = time.Second
			continue
		case ctx.Err() != nil:
			return nil
		case errors.Is(err, errRejected):
			return err // a wrong token won't get better by retrying
		}
		s.logf("controller %s: %v; retrying in %s", controller, err, backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return nil
		}
		backoff = min(backoff*2, 30*time.Second)
	}
	return nil
}

var errRejected = errors.New("the controller rejected this agent")

func (s *Server) connectOnce(ctx context.Context, url string, client *http.Client) error {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+s.Token)
	h.Set(protocolHeader, strconv.Itoa(Protocol))
	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: client, HTTPHeader: h})
	if err != nil {
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusBadRequest) {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
			return fmt.Errorf("%w: %s %s", errRejected, resp.Status, strings.TrimSpace(string(b)))
		}
		return err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(512 << 20)
	info := s.Info()
	if err := wsjson.Write(ctx, conn, message{Type: "hello", Info: &info}); err != nil {
		return err
	}
	s.logf("connected to %s; waiting for a test", url)
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return errors.New("busy")
	}
	s.busy = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.busy = false
		s.mu.Unlock()
	}()
	if err := s.runJob(ctx, conn, url, 0); err != nil && ctx.Err() == nil {
		// Report the problem; if even that fails, the connection is gone
		// and the caller backs off before reconnecting.
		if werr := wsjson.Write(ctx, conn, message{Type: "error", Error: err.Error()}); werr != nil {
			return err
		}
	}
	conn.Close(websocket.StatusNormalClosure, "")
	return nil
}
