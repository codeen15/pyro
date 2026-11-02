package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/codeen15/pyro/pkg/engine/importer"
	"github.com/codeen15/pyro/pkg/engine/mock"
	"github.com/codeen15/pyro/pkg/engine/vars"
)

func mockCmd() *cobra.Command {
	var (
		wsDir, host string
		port        int
		delay       time.Duration
		quiet       bool
	)
	cmd := &cobra.Command{
		Use:   "mock <collection>",
		Short: "Serve a collection's saved examples as a mock API",
		Long: "Each request with saved examples becomes a route (method and path; {{var}}, :var and\n" +
			"{var} segments match any value). The first 2xx example answers by default; pick another\n" +
			"with the X-Mock-Example (name) or X-Mock-Status (code) header. Examples saved while the\n" +
			"mock runs are picked up within a second.",
		Example: "  pyro mock shop --port 8090\n  curl -H 'X-Mock-Status: 404' localhost:8090/v1/orders/42",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(wsDir)
			if err != nil {
				return err
			}
			slug := args[0]
			m := &mock.Server{
				Delay: delay,
				Source: func() (*importer.Bundle, map[string]string, error) {
					b, err := ws.Snapshot(slug)
					if err != nil {
						return nil, nil, err
					}
					v := ws.GlobalVars()
					for k, val := range vars.Map(b.Collection.Variables) {
						v[k] = val
					}
					return b, v, nil
				},
			}
			if !quiet {
				m.OnHit = func(h mock.Hit) {
					ex := h.Example
					if ex == "" {
						ex = "-"
					}
					fmt.Printf("%s  %3d  %-6s %s  (%s)\n", h.At.Format("15:04:05"), h.Status, h.Method, h.Path, ex)
				}
			}
			routes, err := m.Routes()
			if err != nil {
				return err
			}
			ln, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprint(port)))
			if err != nil {
				return err
			}
			fmt.Printf("Mock of %q on http://%s\n", slug, ln.Addr())
			if len(routes) == 0 {
				fmt.Println("  no routes yet: save examples on requests (Save as example in the UI)")
			}
			for _, r := range routes {
				fmt.Printf("  %-6s %-40s %d example(s)\n", r.Method, r.Path, len(r.Examples))
			}
			fmt.Println()
			srv := &http.Server{Handler: m, ReadHeaderTimeout: 10 * time.Second}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			errc := make(chan error, 1)
			go func() { errc <- srv.Serve(ln) }()
			select {
			case err := <-errc:
				if errors.Is(err, http.ErrServerClosed) {
					return nil
				}
				return err
			case <-ctx.Done():
			}
			sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			return srv.Shutdown(sctx)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&wsDir, "workspace", "w", "", "workspace directory (same default as serve)")
	f.StringVar(&host, "host", "127.0.0.1", "address to bind")
	f.IntVarP(&port, "port", "p", 8090, "port to listen on")
	f.DurationVar(&delay, "delay", 0, "wait this long before each response, e.g. 200ms")
	f.BoolVarP(&quiet, "quiet", "q", false, "don't log requests")
	return cmd
}
