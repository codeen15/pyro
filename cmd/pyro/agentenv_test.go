package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codeen15/pyro/pkg/engine/agent"
)

// runServe runs `pyro <args>` until ctx ends and returns its error.
func runServe(ctx context.Context, args ...string) <-chan error {
	errc := make(chan error, 1)
	go func() {
		root := rootCmd()
		root.SetArgs(args)
		errc <- root.ExecuteContext(ctx)
	}()
	return errc
}

// With PYRO_MODE=agent, the image's default command (serve --team) runs a
// load agent configured from the environment, so platforms that only set
// environment variables can start one.
func TestServeInAgentModeConnectsFromEnv(t *testing.T) {
	hub := agent.NewHub("s3cret")
	ts := httptest.NewServer(hub.Handler())
	defer ts.Close()

	t.Setenv("PYRO_PLUGINS_DIR", t.TempDir())
	t.Setenv("PYRO_DATA_DIR", t.TempDir())
	t.Setenv("PYRO_MODE", "agent")
	t.Setenv("PYRO_AGENT_CONNECT", ts.URL)
	t.Setenv("PYRO_AGENT_TOKEN", "s3cret")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := runServe(ctx, "serve", "--team")

	deadline := time.Now().Add(10 * time.Second)
	for len(hub.Connected()) == 0 {
		select {
		case err := <-errc:
			t.Fatalf("serve returned before the agent connected: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("agent never connected to the hub")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-errc:
	case <-time.After(10 * time.Second):
		t.Fatal("agent didn't stop when its context ended")
	}
}

func TestServeRejectsUnknownMode(t *testing.T) {
	t.Setenv("PYRO_MODE", "wrong")
	err := <-runServe(context.Background(), "serve", "--team")
	if err == nil || !strings.Contains(err.Error(), "PYRO_MODE") {
		t.Fatalf("err = %v, want one naming PYRO_MODE", err)
	}
}

func TestAgentConnectNeedsTokenFromEnv(t *testing.T) {
	t.Setenv("PYRO_PLUGINS_DIR", t.TempDir())
	t.Setenv("PYRO_AGENT_CONNECT", "http://127.0.0.1:1")
	t.Setenv("PYRO_AGENT_TOKEN", "")
	err := <-runServe(context.Background(), "agent")
	if err == nil || !strings.Contains(err.Error(), "PYRO_AGENT_TOKEN") {
		t.Fatalf("err = %v, want one asking for the token", err)
	}
}
