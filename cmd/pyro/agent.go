package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/codeen15/pyro/internal/version"
	"github.com/codeen15/pyro/pkg/engine/agent"
	"github.com/codeen15/pyro/pkg/engine/load"
)

func agentCmd() *cobra.Command {
	var (
		host, token, name, certFile, keyFile string
		port, maxVUs                         int
	)
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Run a load agent for distributed tests",
		Long: "Waits for a controller (pyro load --agent, or pyro serve --agent) to send it a share\n" +
			"of a load test, runs it and streams metrics back. One test at a time.\n\n" +
			"Controllers must present the agent token. Without --token (or PYRO_AGENT_TOKEN)\n" +
			"a random token is generated and printed. Plugins used by the test must be\n" +
			"installed and approved on the agent too.",
		Example: "  pyro agent --token s3cret\n  pyro agent --port 7979 --tls-cert cert.pem --tls-key key.pem",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if token == "" {
				token = os.Getenv("PYRO_AGENT_TOKEN")
			}
			generated := token == ""
			if generated {
				token = randomToken()
			}
			if name == "" {
				name, _ = os.Hostname()
			}
			if (certFile == "") != (keyFile == "") {
				return errors.New("--tls-cert and --tls-key go together")
			}
			srv := &agent.Server{Token: token, Name: name, Version: version.Version, MaxVUs: maxVUs, Logf: func(f string, a ...any) {
				fmt.Printf("%s  %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(f, a...))
			}}
			if srv.Plugins = openPlugins(); srv.Plugins != nil {
				defer srv.Plugins.Close()
			}
			ln, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprint(port)))
			if err != nil {
				return err
			}
			hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
			scheme := "http"
			if certFile != "" {
				scheme = "https"
			}
			fmt.Printf("Pyro agent %s (%s)\n  listening  %s://%s\n", version.Version, name, scheme, ln.Addr())
			if generated {
				fmt.Printf("  token      %s\n", token)
			}
			if certFile == "" && !isLoopback(host) {
				fmt.Println("  note       plain HTTP: the token and test data travel unencrypted; use --tls-cert/--tls-key on untrusted networks")
			}
			fmt.Printf("\nUse it with: pyro load <test> --agent <this-host>:%d --agent-token <token>\n\n", ln.Addr().(*net.TCPAddr).Port)

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			errc := make(chan error, 1)
			go func() {
				if certFile != "" {
					errc <- hs.ServeTLS(ln, certFile, keyFile)
				} else {
					errc <- hs.Serve(ln)
				}
			}()
			select {
			case err := <-errc:
				return err
			case <-ctx.Done():
			}
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return hs.Shutdown(sctx)
		},
	}
	f := cmd.Flags()
	f.StringVar(&host, "host", "0.0.0.0", "address to bind")
	f.IntVarP(&port, "port", "p", 7979, "port to listen on")
	f.StringVar(&token, "token", "", "token controllers must present (default: $PYRO_AGENT_TOKEN, else random)")
	f.StringVar(&name, "name", "", "name shown in results (default: hostname)")
	f.IntVar(&maxVUs, "max-vus", load.DefaultMaxVUs, "refuse segments that need more VUs than this")
	f.StringVar(&certFile, "tls-cert", "", "serve TLS with this certificate (controllers use https://host:port)")
	f.StringVar(&keyFile, "tls-key", "", "TLS private key")
	return cmd
}

// connectAgents checks every agent (token, protocol, availability) in
// parallel and returns them as load workers.
func connectAgents(addrs []string, token string, insecure bool) ([]load.Worker, []*agent.Info, error) {
	if token == "" {
		token = os.Getenv("PYRO_AGENT_TOKEN")
	}
	if token == "" {
		return nil, nil, errors.New("agents need a token: pass --agent-token or set PYRO_AGENT_TOKEN")
	}
	workers := make([]load.Worker, len(addrs))
	infos := make([]*agent.Info, len(addrs))
	errs := make([]error, len(addrs))
	var wg sync.WaitGroup
	for i, a := range addrs {
		c := agent.NewClient(strings.TrimSpace(a), token, insecure)
		workers[i] = c
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			in, err := c.Info(ctx)
			switch {
			case err != nil:
				errs[i] = fmt.Errorf("agent %s: %w", c.Name(), err)
			case in.Protocol != agent.Protocol:
				errs[i] = fmt.Errorf("agent %s speaks protocol %d, this controller %d: use the same Pyro version", c.Name(), in.Protocol, agent.Protocol)
			case in.Busy:
				errs[i] = fmt.Errorf("agent %s is busy with another test", c.Name())
			}
			infos[i] = in
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, nil, err
	}
	if v := infos[0].Version; v != version.Version {
		slog.Warn("agent version differs from this controller", "agent", infos[0].Name, "version", v, "controller", version.Version)
	}
	return workers, infos, nil
}
