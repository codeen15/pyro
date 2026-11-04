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
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/uptrace/bun"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	"github.com/codeen15/pyro/internal/db"
	"github.com/codeen15/pyro/internal/history"
	"github.com/codeen15/pyro/internal/server"
	"github.com/codeen15/pyro/internal/store"
	"github.com/codeen15/pyro/internal/team"
	"github.com/codeen15/pyro/internal/version"
	"github.com/codeen15/pyro/pkg/engine"
	"github.com/codeen15/pyro/pkg/engine/protocol/httpdriver"
	"github.com/codeen15/pyro/web"
)

// teamConfig is server.yaml in the data directory. Environment variables
// (also read from a .env file) override it, and flags override both.
type teamConfig struct {
	PublicURL     string    `yaml:"publicURL"`
	Signup        string    `yaml:"signup"`
	SignupDomains []string  `yaml:"signupDomains"`
	MaxLoads      int       `yaml:"maxLoads"`
	SMTP          team.SMTP `yaml:"smtp"`
	// Database is a PostgreSQL URL; empty means SQLite in the data folder.
	Database string `yaml:"database"`
}

type teamFlags struct {
	data, config, publicURL, signup string
	signupDomains                   []string
	tlsCert, tlsKey                 string
	maxLoads                        int
	database, envFile               string
}

func (f *teamFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	f.registerStorage(fl)
	fl.StringVar(&f.config, "config", "", "team server settings file (default <data>/server.yaml)")
	fl.StringVar(&f.publicURL, "public-url", "", "address people open, e.g. https://pyro.example.com ($PYRO_PUBLIC_URL)")
	fl.StringVar(&f.signup, "signup", "", "who may create an account: invite-only (default), open, or domains ($PYRO_SIGNUP)")
	fl.StringSliceVar(&f.signupDomains, "signup-domain", nil, "with --signup domains: allowed email domain, repeat or comma-separate ($PYRO_SIGNUP_DOMAINS)")
	fl.StringVar(&f.tlsCert, "tls-cert", "", "serve HTTPS with this certificate ($PYRO_TLS_CERT)")
	fl.StringVar(&f.tlsKey, "tls-key", "", "private key for --tls-cert ($PYRO_TLS_KEY)")
	fl.IntVar(&f.maxLoads, "max-loads", 0, "load tests that may run at once across all projects, default 2 ($PYRO_MAX_LOADS)")
}

// registerStorage adds the flags that locate the data (serve --team, admin).
func (f *teamFlags) registerStorage(fl *pflag.FlagSet) {
	fl.StringVar(&f.data, "data", "", "team server data directory, default ~/.pyro/team ($PYRO_DATA_DIR)")
	fl.StringVar(&f.database, "database", "", "PostgreSQL URL, e.g. postgres://user:pass@host/pyro; default SQLite in the data directory ($PYRO_DATABASE_URL)")
	fl.StringVar(&f.envFile, "env-file", "", "read environment variables from this file (default: .env in the current folder, if any)")
}

// loadEnv reads the .env file. Variables already set in the environment win.
func (f *teamFlags) loadEnv() error {
	file := f.envFile
	if file == "" {
		if _, err := os.Stat(".env"); err != nil {
			return nil
		}
		file = ".env"
	}
	if err := godotenv.Load(file); err != nil {
		return fmt.Errorf("env file %s: %w", file, err)
	}
	return nil
}

// first returns the first non-empty value.
func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (f *teamFlags) dataDir() (string, error) {
	if dir := first(f.data, os.Getenv("PYRO_DATA_DIR")); dir != "" {
		return filepath.Abs(dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pyro", "team"), nil
}

// teamData is the team server's storage: one database shared by accounts,
// workspaces, history and load runs, plus the project folders.
type teamData struct {
	dir  string
	db   *bun.DB
	st   *team.Store
	hist *history.Store
	kind string // "PostgreSQL" or "SQLite (<file>)"
}

func (t *teamData) Close() { t.db.Close() }

// openTeam loads .env, reads the settings and opens the database.
func (f *teamFlags) openTeam() (*teamData, teamConfig, error) {
	var c teamConfig
	if err := f.loadEnv(); err != nil {
		return nil, c, err
	}
	dir, err := f.dataDir()
	if err != nil {
		return nil, c, err
	}
	if c, err = f.settings(dir); err != nil {
		return nil, c, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "projects"), 0o700); err != nil {
		return nil, c, err
	}
	url, kind := c.Database, "PostgreSQL"
	if url == "" {
		url = filepath.Join(dir, "pyro.db")
		kind = "SQLite (" + url + ")"
	}
	d, err := db.Open(url)
	if err != nil {
		return nil, c, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	st, err := team.NewStore(ctx, d)
	if err != nil {
		d.Close()
		return nil, c, err
	}
	hist, err := history.New(ctx, d)
	if err != nil {
		d.Close()
		return nil, c, err
	}
	return &teamData{dir: dir, db: d, st: st, hist: hist, kind: kind}, c, nil
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
	env := os.Getenv
	c.PublicURL = first(f.publicURL, env("PYRO_PUBLIC_URL"), c.PublicURL)
	c.Signup = first(f.signup, env("PYRO_SIGNUP"), c.Signup)
	c.Database = first(f.database, env("PYRO_DATABASE_URL"), c.Database)
	if len(f.signupDomains) > 0 {
		c.SignupDomains = f.signupDomains
	} else if v := env("PYRO_SIGNUP_DOMAINS"); v != "" {
		c.SignupDomains = strings.Split(v, ",")
	}
	if f.maxLoads > 0 {
		c.MaxLoads = f.maxLoads
	} else if v := env("PYRO_MAX_LOADS"); v != "" {
		if c.MaxLoads, err = strconv.Atoi(v); err != nil {
			return c, fmt.Errorf("PYRO_MAX_LOADS: %w", err)
		}
	}
	c.SMTP.Host = first(env("PYRO_SMTP_HOST"), c.SMTP.Host)
	c.SMTP.Username = first(env("PYRO_SMTP_USERNAME"), c.SMTP.Username)
	c.SMTP.From = first(env("PYRO_SMTP_FROM"), c.SMTP.From)
	if v := env("PYRO_SMTP_PORT"); v != "" {
		if c.SMTP.Port, err = strconv.Atoi(v); err != nil {
			return c, fmt.Errorf("PYRO_SMTP_PORT: %w", err)
		}
	}
	f.tlsCert = first(f.tlsCert, env("PYRO_TLS_CERT"))
	f.tlsKey = first(f.tlsKey, env("PYRO_TLS_KEY"))
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
	td, c, err := f.openTeam()
	if err != nil {
		return err
	}
	defer td.Close()
	st, dir, hist := td.st, td.dir, td.hist
	if (f.tlsCert == "") != (f.tlsKey == "") {
		return errors.New("--tls-cert and --tls-key go together")
	}
	plugins := openPlugins()
	if plugins != nil {
		defer plugins.Close()
	}
	ts := team.New(team.Config{
		DataDir: dir, Store: st, PublicURL: c.PublicURL, Signup: c.Signup, SignupDomains: c.SignupDomains,
		MaxLoads: c.MaxLoads, UI: web.Dist(), Mailer: team.NewMailer(c.SMTP),
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
	fmt.Printf("Pyro %s team server\n  data       %s\n  database   %s\n  open       %s/\n", version.Version, dir, td.kind, addr)
	if n == 0 {
		fmt.Println("\nNo accounts yet: the first person to sign up becomes the administrator.")
	}
	if c.SMTP.Host == "" {
		fmt.Println("\nInvite emails are off (no smtp in server.yaml); invites are links to copy.")
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
	var tf teamFlags
	open := func() (*team.Store, func(), error) {
		td, _, err := tf.openTeam()
		if err != nil {
			return nil, nil, err
		}
		return td.st, td.Close, nil
	}
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Manage team server accounts from the command line",
		Long:  "Creates accounts, resets passwords and grants administration on a team server's data\ndirectory (the server may be running).",
	}
	tf.registerStorage(cmd.PersistentFlags())

	var name string
	var admin bool
	create := &cobra.Command{
		Use:   "create-user <email>",
		Short: "Create an account (asks for the password)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, closeDB, err := open()
			if err != nil {
				return err
			}
			defer closeDB()
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
			st, closeDB, err := open()
			if err != nil {
				return err
			}
			defer closeDB()
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
			st, closeDB, err := open()
			if err != nil {
				return err
			}
			defer closeDB()
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
			st, closeDB, err := open()
			if err != nil {
				return err
			}
			defer closeDB()
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
