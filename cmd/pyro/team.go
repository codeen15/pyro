package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	"github.com/codeen15/pyro/internal/history"
	"github.com/codeen15/pyro/internal/server"
	"github.com/codeen15/pyro/internal/store"
	"github.com/codeen15/pyro/internal/team"
	"github.com/codeen15/pyro/internal/version"
	"github.com/codeen15/pyro/pkg/engine"
	"github.com/codeen15/pyro/pkg/engine/protocol/httpdriver"
	"github.com/codeen15/pyro/web"
)

// teamConfig is server.yaml in the data directory; flags override it.
type teamConfig struct {
	PublicURL     string   `yaml:"publicURL"`
	Signup        string   `yaml:"signup"`
	SignupDomains []string `yaml:"signupDomains"`
	MaxLoads      int      `yaml:"maxLoads"`
}

type teamFlags struct {
	data, config, publicURL, signup string
	signupDomains                   []string
	tlsCert, tlsKey                 string
	maxLoads                        int
}

func (f *teamFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&f.data, "data", "", "team server data directory (default ~/.pyro/team)")
	fl.StringVar(&f.config, "config", "", "team server settings file (default <data>/server.yaml)")
	fl.StringVar(&f.publicURL, "public-url", "", "address people open, e.g. https://pyro.example.com")
	fl.StringVar(&f.signup, "signup", "", "who may create an account: invite-only (default), open, or domains")
	fl.StringSliceVar(&f.signupDomains, "signup-domain", nil, "with --signup domains: allowed email domain (repeat or comma-separate)")
	fl.StringVar(&f.tlsCert, "tls-cert", "", "serve HTTPS with this certificate")
	fl.StringVar(&f.tlsKey, "tls-key", "", "private key for --tls-cert")
	fl.IntVar(&f.maxLoads, "max-loads", 0, "load tests that may run at once across all projects (default 2)")
}

func teamDataDir(dir string) (string, error) {
	if dir != "" {
		return filepath.Abs(dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pyro", "team"), nil
}

func openTeamStore(dir string) (*team.Store, string, error) {
	dir, err := teamDataDir(dir)
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Join(dir, "projects"), 0o700); err != nil {
		return nil, "", err
	}
	st, err := team.Open(filepath.Join(dir, "pyro.db"))
	return st, dir, err
}

func (f *teamFlags) settings(dir string) (teamConfig, error) {
	var c teamConfig
	file := f.config
	if file == "" {
		file = filepath.Join(dir, "server.yaml")
	}
	data, err := os.ReadFile(file)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(data, &c); err != nil {
			return c, fmt.Errorf("%s: %w", file, err)
		}
	case !errors.Is(err, os.ErrNotExist) || f.config != "":
		return c, err
	}
	if f.publicURL != "" {
		c.PublicURL = f.publicURL
	}
	if f.signup != "" {
		c.Signup = f.signup
	}
	if len(f.signupDomains) > 0 {
		c.SignupDomains = f.signupDomains
	}
	if f.maxLoads > 0 {
		c.MaxLoads = f.maxLoads
	}
	switch c.Signup {
	case "", team.SignupInviteOnly, team.SignupOpen:
	case team.SignupDomains:
		if len(c.SignupDomains) == 0 {
			return c, errors.New("--signup domains needs at least one --signup-domain")
		}
	default:
		return c, fmt.Errorf("unknown signup policy %q (use invite-only, open or domains)", c.Signup)
	}
	return c, nil
}

// serveTeam runs the multi-user team server.
func serveTeam(f *teamFlags, host string, port int, agents []string, agentToken string, agentInsecure bool, outs []string) error {
	st, dir, err := openTeamStore(f.data)
	if err != nil {
		return err
	}
	defer st.Close()
	c, err := f.settings(dir)
	if err != nil {
		return err
	}
	if (f.tlsCert == "") != (f.tlsKey == "") {
		return errors.New("--tls-cert and --tls-key go together")
	}
	hist, err := history.Open(filepath.Join(dir, "pyro.db"))
	if err != nil {
		return err
	}
	defer hist.Close()
	plugins := openPlugins()
	if plugins != nil {
		defer plugins.Close()
	}
	ts := team.New(team.Config{
		DataDir: dir, Store: st, PublicURL: c.PublicURL, Signup: c.Signup, SignupDomains: c.SignupDomains,
		MaxLoads: c.MaxLoads, UI: web.Dist(),
		Project: func(ws *store.Workspace, id string) server.Config {
			eng := engine.New(httpdriver.New(httpdriver.Options{FileRoot: ws.Root}))
			eng.Plugins = plugins
			return server.Config{
				Engine: eng, History: hist, PluginIndex: os.Getenv("PYRO_PLUGIN_INDEX"),
				Agents: agents, AgentToken: agentToken, AgentInsecure: agentInsecure, Outputs: outs,
			}
		},
	})
	defer ts.Close()

	ln, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: ts.Handler(), ReadHeaderTimeout: 10 * time.Second}
	scheme := "http"
	if f.tlsCert != "" {
		scheme = "https"
	}
	addr := c.PublicURL
	if addr == "" {
		addr = scheme + "://" + displayAddr(ln.Addr().(*net.TCPAddr), host)
	}
	n, _ := st.CountUsers(context.Background())
	fmt.Printf("Pyro %s team server\n  data       %s\n  open       %s/\n", version.Version, dir, addr)
	if n == 0 {
		fmt.Println("\nNo accounts yet: the first person to sign up becomes the administrator.")
	}
	if scheme == "http" && !isLoopback(host) && !strings.HasPrefix(c.PublicURL, "https://") {
		fmt.Println("\nWarning: serving plain HTTP on the network; passwords and session cookies travel unencrypted.\nUse --tls-cert/--tls-key, or put an HTTPS proxy in front and set --public-url.")
	}
	fmt.Println("\nPress Ctrl+C to stop.")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() {
		if f.tlsCert != "" {
			errc <- srv.ServeTLS(ln, f.tlsCert, f.tlsKey)
		} else {
			errc <- srv.Serve(ln)
		}
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func adminCmd() *cobra.Command {
	var data string
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Manage team server accounts from the command line",
		Long:  "Creates accounts, resets passwords and grants administration on a team server's data\ndirectory (the server may be running).",
	}
	cmd.PersistentFlags().StringVar(&data, "data", "", "team server data directory (default ~/.pyro/team)")

	var name string
	var admin bool
	create := &cobra.Command{
		Use:   "create-user <email>",
		Short: "Create an account (asks for the password)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, _, err := openTeamStore(data)
			if err != nil {
				return err
			}
			defer st.Close()
			pw, err := readPassword()
			if err != nil {
				return err
			}
			u, err := st.CreateUser(cmd.Context(), team.NewUser{Email: args[0], Name: name, Password: pw, Admin: admin})
			if err != nil {
				return err
			}
			role := "user"
			if u.Admin {
				role = "administrator"
			}
			fmt.Printf("Created %s (%s)\n", u.Email, role)
			return nil
		},
	}
	create.Flags().StringVar(&name, "name", "", "display name (default: the part before @)")
	create.Flags().BoolVar(&admin, "admin", false, "make the account a server administrator")

	reset := &cobra.Command{
		Use:   "reset-password <email>",
		Short: "Set a new password (asks for it) and sign the account out everywhere",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, _, err := openTeamStore(data)
			if err != nil {
				return err
			}
			defer st.Close()
			u, err := st.UserByEmail(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("no account for %s", args[0])
			}
			pw, err := readPassword()
			if err != nil {
				return err
			}
			if err := st.SetPassword(cmd.Context(), u.ID, pw, ""); err != nil {
				return err
			}
			fmt.Printf("Password changed for %s\n", u.Email)
			return nil
		},
	}

	var revoke bool
	setAdmin := &cobra.Command{
		Use:   "set-admin <email>",
		Short: "Make an account a server administrator (--revoke to undo)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, _, err := openTeamStore(data)
			if err != nil {
				return err
			}
			defer st.Close()
			u, err := st.UserByEmail(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("no account for %s", args[0])
			}
			if err := st.SetAdmin(cmd.Context(), u.ID, !revoke); err != nil {
				return err
			}
			fmt.Printf("%s is %s an administrator\n", u.Email, map[bool]string{true: "no longer", false: "now"}[revoke])
			return nil
		},
	}
	setAdmin.Flags().BoolVar(&revoke, "revoke", false, "remove administration instead")

	list := &cobra.Command{
		Use:   "users",
		Short: "List accounts",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, _, err := openTeamStore(data)
			if err != nil {
				return err
			}
			defer st.Close()
			users, err := st.Users(cmd.Context())
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "EMAIL\tNAME\tSIGN-IN\tADMIN\tCREATED")
			for _, u := range users {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%v\t%s\n", u.Email, u.Name, u.Provider, u.Admin, u.Created.Format("2006-01-02"))
			}
			return tw.Flush()
		},
	}
	cmd.AddCommand(create, reset, setAdmin, list)
	return cmd
}

// readPassword asks twice on a terminal, or reads one line from a pipe.
func readPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", errors.New("no password on standard input")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(os.Stderr, "Password: ")
	a, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "Again: ")
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(a) != string(b) {
		return "", errors.New("the passwords don't match")
	}
	return string(a), nil
}
