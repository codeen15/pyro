// Command pyro is the Pyro API client and load-testing tool.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/pyrohq/pyro/internal/history"
	"github.com/pyrohq/pyro/internal/server"
	"github.com/pyrohq/pyro/internal/store"
	"github.com/pyrohq/pyro/internal/version"
	"github.com/pyrohq/pyro/pkg/engine"
	"github.com/pyrohq/pyro/pkg/engine/importer"
	"github.com/pyrohq/pyro/pkg/engine/importer/postman"
	"github.com/pyrohq/pyro/pkg/engine/protocol/httpdriver"
	"github.com/pyrohq/pyro/web"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "pyro",
		Short:         "API client and load-testing tool",
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.AddCommand(serveCmd(), initCmd(), importCmd(), versionCmd())
	return root
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("pyro %s (%s) %s/%s\n", version.Version, version.Commit, runtime.GOOS, runtime.GOARCH)
		},
	}
}

func initCmd() *cobra.Command {
	var name string
	var sample bool
	cmd := &cobra.Command{
		Use:   "init [dir]",
		Short: "Create a new workspace",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			if name == "" {
				abs, _ := filepath.Abs(dir)
				name = filepath.Base(abs)
			}
			ws, err := store.Init(dir, name)
			if err != nil {
				return err
			}
			if sample {
				if err := seedSample(ws); err != nil {
					return err
				}
			}
			fmt.Printf("Created workspace %q in %s\n", name, dir)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "workspace name (defaults to directory name)")
	cmd.Flags().BoolVar(&sample, "sample", false, "add an example collection and environment")
	return cmd
}

func serveCmd() *cobra.Command {
	var (
		host, wsDir string
		port        int
		open, noTok bool
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the engine and open the web UI",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(wsDir)
			if err != nil {
				return err
			}
			token := ""
			if !noTok {
				token = randomToken()
			}
			if noTok && !isLoopback(host) {
				return errors.New("--no-token is only allowed when binding to a loopback address")
			}

			ln, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprint(port)))
			if err != nil {
				return err
			}
			hist, err := openHistory()
			if err != nil {
				slog.Warn("request history disabled", "err", err)
			} else {
				defer hist.Close()
			}
			eng := engine.New(httpdriver.New(httpdriver.Options{FileRoot: ws.Root}))
			srv := &http.Server{
				Handler: server.New(server.Config{
					Workspace: ws, Engine: eng, History: hist, Token: token, UI: web.Dist(),
				}).Handler(),
				ReadHeaderTimeout: 10 * time.Second,
			}

			url := "http://" + displayAddr(ln.Addr().(*net.TCPAddr), host) + "/"
			if token != "" {
				url += "?token=" + token
			}
			fmt.Printf("Pyro %s\n  workspace  %s (%s)\n  open       %s\n\nPress Ctrl+C to stop.\n",
				version.Version, ws.Meta.Name, ws.Root, url)
			if open {
				if err := openBrowser(url); err != nil {
					slog.Warn("could not open browser", "err", err)
				}
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			errc := make(chan error, 1)
			go func() { errc <- srv.Serve(ln) }()
			select {
			case err := <-errc:
				return err
			case <-ctx.Done():
			}
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return srv.Shutdown(shutdownCtx)
		},
	}
	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "address to bind")
	cmd.Flags().IntVarP(&port, "port", "p", 7878, "port to listen on (0 picks a free port)")
	cmd.Flags().StringVarP(&wsDir, "workspace", "w", "", "workspace directory (default: ./ if it has pyro.yaml, else ~/.pyro/workspace)")
	cmd.Flags().BoolVar(&open, "open", false, "open the UI in a browser")
	cmd.Flags().BoolVar(&noTok, "no-token", false, "disable the session token (loopback only; for development)")
	return cmd
}

// openWorkspace resolves the workspace to serve. With no explicit directory it
// uses the current directory if it is a workspace, otherwise a default
// workspace under ~/.pyro that is created (with a sample) on first run.
func openWorkspace(dir string) (*store.Workspace, error) {
	if dir != "" {
		return store.Open(dir)
	}
	if store.Exists(".") {
		abs, _ := filepath.Abs(".")
		return store.Open(abs)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	def := filepath.Join(home, ".pyro", "workspace")
	if store.Exists(def) {
		return store.Open(def)
	}
	ws, err := store.Init(def, "My Workspace")
	if err != nil {
		return nil, err
	}
	return ws, seedSample(ws)
}

// openHistory opens ~/.pyro/pyro.db, shared by all workspaces.
func openHistory() (*history.Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".pyro")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return history.Open(filepath.Join(dir, "pyro.db"))
}

func importCmd() *cobra.Command {
	var wsDir, reportFile string
	cmd := &cobra.Command{
		Use:   "import <file.json>...",
		Short: "Import Postman collections or environments into a workspace",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(wsDir)
			if err != nil {
				return err
			}
			var reports []*importer.Report
			for _, file := range args {
				data, err := os.ReadFile(file)
				if err != nil {
					return err
				}
				bundle, err := postman.Parse(data)
				if err != nil {
					return fmt.Errorf("%s: %w", file, err)
				}
				rep, err := ws.Import(bundle)
				if err != nil {
					return fmt.Errorf("%s: %w", file, err)
				}
				reports = append(reports, rep)
				printReport(file, rep)
			}
			if reportFile != "" {
				data, _ := json.MarshalIndent(reports, "", "  ")
				return os.WriteFile(reportFile, data, 0o644)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&wsDir, "workspace", "w", "", "workspace directory (same default as serve)")
	cmd.Flags().StringVar(&reportFile, "report", "", "write the import report(s) as JSON to this file")
	return cmd
}

func printReport(file string, r *importer.Report) {
	fmt.Printf("%s (%s)\n", file, r.Format)
	if r.Slug != "" {
		fmt.Printf("  collection   %q -> collections/%s (%d requests, %d folders)\n", r.Collection, r.Slug, r.Requests, r.Folders)
	}
	for _, e := range r.Environments {
		fmt.Printf("  environment  environments/%s.env.yaml\n", e)
	}
	if len(r.Warnings) > 0 {
		fmt.Printf("  %d warning(s):\n", len(r.Warnings))
		for _, w := range r.Warnings {
			where := w.Path
			if where == "" {
				where = "(collection)"
			}
			fmt.Printf("    %-28s %s: %s\n", where, w.Code, w.Detail)
		}
	}
}

func randomToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func displayAddr(addr *net.TCPAddr, host string) string {
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return net.JoinHostPort(host, fmt.Sprint(addr.Port))
}

func openBrowser(url string) error {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		if isWSL() {
			c = exec.Command("cmd.exe", "/c", "start", "", url)
		} else {
			c = exec.Command("xdg-open", url)
		}
	}
	return c.Start()
}

func isWSL() bool {
	b, err := os.ReadFile("/proc/version")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}
