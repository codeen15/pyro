// Command pyro is the Pyro API client and load-testing tool.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"golang.org/x/term"

	"github.com/codeen15/pyro/internal/history"
	"github.com/codeen15/pyro/internal/server"
	"github.com/codeen15/pyro/internal/store"
	"github.com/codeen15/pyro/internal/version"
	"github.com/codeen15/pyro/pkg/engine"
	exportpm "github.com/codeen15/pyro/pkg/engine/exporter/postman"
	"github.com/codeen15/pyro/pkg/engine/importer"
	"github.com/codeen15/pyro/pkg/engine/importer/auto"
	"github.com/codeen15/pyro/pkg/engine/protocol/httpdriver"
	"github.com/codeen15/pyro/web"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		if !errors.Is(err, errRunFailed) {
			fmt.Fprintln(os.Stderr, "Error:", err)
		}
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "pyro",
		Short:         "API client and load-testing tool",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(serveCmd(), initCmd(), importCmd(), exportCmd(), runCmd(), loadCmd(), agentCmd(), mockCmd(), docsCmd(), pluginCmd(), versionCmd())
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
		host, wsDir   string
		port          int
		open, noTok   bool
		agents, outs  []string
		agentToken    string
		agentInsecure bool
		agentListen   string
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

			if agentToken == "" {
				agentToken = os.Getenv("PYRO_AGENT_TOKEN")
			}
			if len(agents) > 0 && agentToken == "" {
				return errors.New("agents need a token: pass --agent-token or set PYRO_AGENT_TOKEN")
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
			eng := engine.New(httpdriver.New(httpdriver.Options{FileRoot: ws.Root, AllowOutsideRoot: ws.Meta.AllowFilesOutsideWorkspace}))
			if eng.Plugins = openPlugins(); eng.Plugins != nil {
				defer eng.Plugins.Close()
			}
			api := server.New(server.Config{
				Workspace: ws, Engine: eng, History: hist, Token: token, UI: web.Dist(),
				PluginIndex: os.Getenv("PYRO_PLUGIN_INDEX"),
				Agents:      agents, AgentToken: agentToken, AgentInsecure: agentInsecure, Outputs: outs,
				AnyHost: !isLoopback(host),
			})
			defer api.Close()
			srv := &http.Server{
				Handler:           api.Handler(),
				ReadHeaderTimeout: 10 * time.Second,
			}

			url := "http://" + displayAddr(ln.Addr().(*net.TCPAddr), host) + "/"
			if token != "" {
				url += "?token=" + token
			}
			fmt.Printf("Pyro %s\n  workspace  %s (%s)\n  open       %s\n", version.Version, ws.Meta.Name, ws.Root, url)
			if len(agents) > 0 {
				fmt.Printf("  agents     %s\n", strings.Join(agents, ", "))
			}
			if agentListen != "" {
				hub, stopHub, err := startAgentListener(agentListen, agentToken)
				if err != nil {
					return err
				}
				defer stopHub()
				api.SetAgentHub(hub)
			}
			fmt.Println("\nPress Ctrl+C to stop.")
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
	cmd.Flags().StringArrayVarP(&outs, "out", "o", nil, "stream every load test's live metrics: influxdb=URL, prometheus=URL, json=FILE, csv=FILE")
	cmd.Flags().StringSliceVar(&agents, "agent", nil, "load agents for distributed tests (host:port; repeat or comma-separate)")
	cmd.Flags().StringVar(&agentToken, "agent-token", "", "token for the agents (default: $PYRO_AGENT_TOKEN)")
	cmd.Flags().BoolVar(&agentInsecure, "agent-insecure", false, "skip TLS certificate verification for https agents")
	cmd.Flags().StringVar(&agentListen, "agent-listen", "", "accept agents that connect out (pyro agent --connect) on this address, e.g. :7980")
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
	var wsDir, reportFile, into string
	cmd := &cobra.Command{
		Use:   "import <file>...",
		Short: "Import Postman, OpenAPI/Swagger, HAR or cURL into a workspace",
		Long: "Imports Postman collections and environments (v2.0/v2.1), OpenAPI 3.x and Swagger 2.0\n" +
			"definitions (JSON or YAML), HAR recordings and cURL commands. The format is detected.\n" +
			"Each file becomes a new collection unless --into names an existing collection or folder.\n" +
			"Use - to read from standard input (handy for a copied cURL command).",
		Example: "  pyro import shop.postman_collection.json staging.postman_environment.json\n" +
			"  pyro import openapi.yaml\n  pbpaste | pyro import - --into shop",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(wsDir)
			if err != nil {
				return err
			}
			var reports []*importer.Report
			for _, file := range args {
				var data []byte
				if file == "-" {
					data, err = io.ReadAll(os.Stdin)
				} else {
					data, err = os.ReadFile(file)
				}
				if err != nil {
					return err
				}
				name := file
				if file == "-" {
					name = ""
				}
				bundle, err := auto.Parse(name, data)
				if err != nil {
					return fmt.Errorf("%s: %w", file, err)
				}
				var rep *importer.Report
				if into != "" {
					rep, err = ws.ImportInto(into, bundle)
				} else {
					rep, err = ws.Import(bundle)
				}
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
	cmd.Flags().StringVar(&into, "into", "", "add the requests to this existing collection or folder")
	return cmd
}

func exportCmd() *cobra.Command {
	var wsDir, out, envName string
	cmd := &cobra.Command{
		Use:   "export [collection]",
		Short: "Export a collection or environment as Postman v2.1 JSON",
		Long: "Writes a collection as a Postman Collection v2.1 file (assertions and extractors become\n" +
			"test scripts; WebSocket, Socket.IO and plugin requests are left out and reported),\n" +
			"or an environment with --environment.",
		Example: "  pyro export shop -o shop.postman_collection.json\n  pyro export -e staging -o staging.postman_environment.json",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(wsDir)
			if err != nil {
				return err
			}
			var data []byte
			switch {
			case envName != "" && len(args) == 0:
				env, err := ws.ReadEnvironment(envName)
				if err != nil {
					return fmt.Errorf("environment %q: %w", envName, err)
				}
				if env.Name == "" {
					env.Name = envName
				}
				if data, err = exportpm.ExportEnvironment(env); err != nil {
					return err
				}
			case len(args) == 1 && envName == "":
				b, err := ws.Snapshot(args[0])
				if err != nil {
					return err
				}
				var rep *importer.Report
				if data, rep, err = exportpm.Export(b); err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "%s: %d requests, %d folders\n", args[0], rep.Requests, rep.Folders)
				for _, w := range rep.Warnings {
					where := w.Path
					if where == "" {
						where = "(collection)"
					}
					fmt.Fprintf(os.Stderr, "  %-28s %s: %s\n", where, w.Code, w.Detail)
				}
			default:
				return errors.New("give a collection, or --environment, not both")
			}
			if out == "" || out == "-" {
				_, err = os.Stdout.Write(data)
				return err
			}
			return os.WriteFile(out, data, 0o644)
		},
	}
	cmd.Flags().StringVarP(&wsDir, "workspace", "w", "", "workspace directory (same default as serve)")
	cmd.Flags().StringVarP(&out, "output", "o", "", "file to write (default: standard output)")
	cmd.Flags().StringVarP(&envName, "environment", "e", "", "export this environment instead of a collection")
	return cmd
}

func printReport(file string, r *importer.Report) {
	fmt.Printf("%s (%s)\n", file, r.Format)
	switch {
	case r.Into:
		fmt.Printf("  added        %d requests, %d folders -> collections/%s\n", r.Requests, r.Folders, r.Slug)
	case r.Slug != "":
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

// stdinIsTerminal reports whether prompts can be answered (not a pipe,
// file or /dev/null, which is also a character device).
func stdinIsTerminal() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

func isWSL() bool {
	b, err := os.ReadFile("/proc/version")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}
