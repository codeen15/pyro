package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/spf13/cobra"

	"github.com/codeen15/pyro/pkg/engine/plugin"
)

// pluginsDir is ~/.pyro/plugins, or $PYRO_PLUGINS_DIR.
func pluginsDir() (string, error) {
	if d := os.Getenv("PYRO_PLUGINS_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pyro", "plugins"), nil
}

// openPlugins opens the plugin manager; failures disable plugins rather
// than stopping the command.
func openPlugins() *plugin.Manager {
	dir, err := pluginsDir()
	if err == nil {
		var m *plugin.Manager
		if m, err = plugin.Open(dir); err == nil {
			return m
		}
	}
	slog.Warn("plugins disabled", "err", err)
	return nil
}

// watchPlugins keeps a long-running command's manager in step with changes
// other processes (the CLI) make to the plugins folder, and compiles the JS
// runtime in the background for the JS plugins in use. The returned func
// stops the watch and waits for it; call it before closing the manager.
func watchPlugins(ctx context.Context, m *plugin.Manager) (stop func()) {
	m.Precompile()
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.Watch(wctx, time.Second)
	}()
	return func() { cancel(); <-done }
}

func indexURL(flag string) string {
	if flag != "" {
		return flag
	}
	return os.Getenv("PYRO_PLUGIN_INDEX")
}

func pluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Install and manage plugins (request types, auth types and processors)",
		Long: "Plugins are WebAssembly modules that add request types (e.g. raw TCP), auth types\n" +
			"and pre/post processors. They run sandboxed and may only reach the network addresses they declare,\n" +
			"which you approve when installing. Installed plugins live in ~/.pyro/plugins\n" +
			"(or $PYRO_PLUGINS_DIR).",
	}
	var index string
	var yes bool

	list := &cobra.Command{
		Use:   "list",
		Short: "List installed plugins",
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := managerOrErr()
			if err != nil {
				return err
			}
			defer m.Close()
			infos := m.List()
			if len(infos) == 0 {
				fmt.Println("No plugins installed. Try: pyro plugin install ./my-plugin  (or a .pyro-plugin archive)")
				return nil
			}
			for _, in := range infos {
				fmt.Printf("%-14s %-9s %-16s %s (%s)\n", in.Name, in.Version, status(in), provides(in.Manifest), limits(in.Manifest))
			}
			return nil
		},
	}

	install := &cobra.Command{
		Use:   "install <dir | archive | name[@version]>",
		Short: "Install a plugin from a directory, an archive, or the plugin index",
		Example: "  pyro plugin install ./plugins/examples/tcp\n  pyro plugin install tcp-0.1.0.pyro-plugin\n" +
			"  PYRO_PLUGIN_INDEX=https://example.com/index.json pyro plugin install tcp",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := managerOrErr()
			if err != nil {
				return err
			}
			defer m.Close()
			src := args[0]
			var info plugin.Info
			st, statErr := os.Stat(src)
			switch {
			case statErr == nil && st.IsDir():
				info, err = m.InstallDir(src, false)
			case statErr == nil:
				data, rerr := os.ReadFile(src)
				if rerr != nil {
					return rerr
				}
				info, err = m.InstallArchive(data, false)
			default:
				info, err = installFromIndex(m, indexURL(index), src)
			}
			if err != nil {
				return err
			}
			fmt.Printf("Installed %s %s: %s\n", info.Name, info.Version, provides(info.Manifest))
			if info.Approved {
				fmt.Println("Capabilities unchanged since your last approval; it's ready.")
				return nil
			}
			return approveFlow(m, info, yes)
		},
	}
	install.Flags().StringVar(&index, "index", "", "plugin index URL (default $PYRO_PLUGIN_INDEX)")
	install.Flags().BoolVarP(&yes, "yes", "y", false, "approve the plugin's capabilities without asking")

	approve := &cobra.Command{
		Use:   "approve <name>",
		Short: "Review and approve a plugin's capabilities",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := managerOrErr()
			if err != nil {
				return err
			}
			defer m.Close()
			info, err := m.Get(args[0])
			if err != nil {
				return err
			}
			return approveFlow(m, info, yes)
		},
	}
	approve.Flags().BoolVarP(&yes, "yes", "y", false, "approve without asking")

	toggle := func(use string, enabled bool) *cobra.Command {
		return &cobra.Command{
			Use:   use + " <name>",
			Short: strings.ToUpper(use[:1]) + use[1:] + " a plugin",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				m, err := managerOrErr()
				if err != nil {
					return err
				}
				defer m.Close()
				info, err := m.SetEnabled(args[0], enabled)
				if err == nil {
					fmt.Printf("%s: %s\n", info.Name, status(info))
				}
				return err
			},
		}
	}

	remove := &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"uninstall"},
		Short:   "Uninstall a plugin",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := managerOrErr()
			if err != nil {
				return err
			}
			defer m.Close()
			if err := m.Remove(args[0]); err != nil {
				return err
			}
			fmt.Printf("Removed %s\n", args[0])
			return nil
		},
	}

	search := &cobra.Command{
		Use:   "search [query]",
		Short: "Search the plugin index",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			u := indexURL(index)
			if u == "" {
				return errors.New("no plugin index configured: pass --index or set PYRO_PLUGIN_INDEX (a public index isn't published yet)")
			}
			idx, err := plugin.FetchIndex(cmd.Context(), u)
			if err != nil {
				return err
			}
			q := ""
			if len(args) == 1 {
				q = args[0]
			}
			hits := idx.Search(q)
			if len(hits) == 0 {
				fmt.Println("No plugins match.")
			}
			for _, e := range hits {
				fmt.Printf("%-14s %-9s %s\n", e.Name, e.Version, firstNonEmpty(e.Title, e.Description))
			}
			return nil
		},
	}
	search.Flags().StringVar(&index, "index", "", "plugin index URL (default $PYRO_PLUGIN_INDEX)")

	cmd.AddCommand(list, install, approve, toggle("enable", true), toggle("disable", false), remove, search, newPluginCmd(), packCmd(), pluginTestCmd(), pluginDevCmd())
	return cmd
}

func managerOrErr() (*plugin.Manager, error) {
	dir, err := pluginsDir()
	if err != nil {
		return nil, err
	}
	return plugin.Open(dir)
}

func installFromIndex(m *plugin.Manager, index, ref string) (plugin.Info, error) {
	if index == "" {
		return plugin.Info{}, fmt.Errorf("%q is not a file or directory, and no plugin index is configured (pass --index or set PYRO_PLUGIN_INDEX)", ref)
	}
	name, version, _ := strings.Cut(ref, "@")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	idx, err := plugin.FetchIndex(ctx, index)
	if err != nil {
		return plugin.Info{}, err
	}
	e, ok := idx.Find(name, version)
	if !ok {
		return plugin.Info{}, fmt.Errorf("the index has no plugin %q", ref)
	}
	data, err := idx.Download(ctx, e)
	if err != nil {
		return plugin.Info{}, err
	}
	return m.InstallArchive(data, false)
}

func approveFlow(m *plugin.Manager, info plugin.Info, yes bool) error {
	net := info.Capabilities.Network
	if len(net) == 0 {
		fmt.Println("It asks for no network access.")
	} else {
		fmt.Printf("It asks to connect to: %s\n", strings.Join(net, ", "))
	}
	if info.IsJS() {
		fmt.Println("runtime: js")
	}
	fmt.Printf("memory: %d MB\n", info.Limits.MemoryMB)
	if !yes {
		if !stdinIsTerminal() {
			fmt.Printf("Not approved yet. Approve with: pyro plugin approve %s --yes\n", info.Name)
			return nil
		}
		fmt.Print("Approve? [y/N] ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Printf("Not approved. The plugin stays installed but unusable until you run: pyro plugin approve %s\n", info.Name)
			return nil
		}
	}
	info, err := m.Approve(info.Name)
	if err != nil {
		return err
	}
	fmt.Printf("%s is ready.\n", info.Name)
	return nil
}

func status(in plugin.Info) string {
	switch {
	case in.Error != "":
		return "broken"
	case !in.Enabled:
		return "disabled"
	case !in.Approved:
		return "needs approval"
	}
	return "ready"
}

func provides(m plugin.Manifest) string {
	var parts []string
	for _, p := range m.Provides {
		parts = append(parts, fmt.Sprintf("%s plugin:%s", p.Kind, p.ID))
	}
	return strings.Join(parts, ", ")
}

// limits summarises a plugin's runtime and memory limit for listings.
func limits(m plugin.Manifest) string {
	s := fmt.Sprintf("memory: %d MB", m.Limits.MemoryMB)
	if m.IsJS() {
		s = "runtime: js, " + s
	}
	return s
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func packCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "pack <dir>",
		Short: "Package a built plugin (manifest + module) as a .pyro-plugin archive",
		Long:  "Writes <name>-<version>.pyro-plugin and prints its sha256 for an index entry.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			data, err := os.ReadFile(filepath.Join(dir, plugin.ManifestFile))
			if err != nil {
				return err
			}
			man, err := plugin.ParseManifest(data)
			if err != nil {
				return err
			}
			module, err := os.ReadFile(filepath.Join(dir, man.Module))
			switch {
			case err != nil && man.IsJS() && errors.Is(err, os.ErrNotExist):
				return fmt.Errorf("missing %s", man.Module)
			case err != nil && man.IsJS():
				return err
			case err != nil:
				return fmt.Errorf("%w (build it first: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o %s .)", err, man.Module)
			}
			var buf bytes.Buffer
			zw := zip.NewWriter(&buf)
			for name, b := range map[string][]byte{plugin.ManifestFile: data, man.Module: module} {
				w, err := zw.Create(name)
				if err != nil {
					return err
				}
				if _, err := w.Write(b); err != nil {
					return err
				}
			}
			if err := zw.Close(); err != nil {
				return err
			}
			if out == "" {
				out = fmt.Sprintf("%s-%s.pyro-plugin", man.Name, man.Version)
			}
			if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
				return err
			}
			fmt.Printf("%s\nsha256 %s\n", out, plugin.Checksum(buf.Bytes()))
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "archive path")
	return cmd
}

// sdkVersion is the published Go SDK the plugin template requires (tag
// plugins/sdk/go/<version>; bump it with each SDK release).
const sdkVersion = "v0.2.0"

// pluginTemplates holds the files `pyro plugin new` writes, per language:
// templates/<lang>/<file>.tmpl is a text/template; other files are copied as is.
//
//go:embed templates
var pluginTemplates embed.FS

// newFiles lists what a new plugin contains: output name -> template file.
func newFiles(lang, kind string) map[string]string {
	if lang == "js" {
		return map[string]string{
			"pyro-plugin.yaml": "pyro-plugin.yaml.tmpl",
			"plugin.js":        kind + ".js.tmpl",
			"pyro.d.ts":        "pyro.d.ts",
			"README.md":        "README.md.tmpl",
		}
	}
	return map[string]string{
		"go.mod":           "go.mod.tmpl",
		"pyro-plugin.yaml": "pyro-plugin.yaml.tmpl",
		"main.go":          kind + ".go.tmpl",
		"README.md":        "README.md.tmpl",
	}
}

func newPluginCmd() *cobra.Command {
	var kind, id, sdk, lang string
	cmd := &cobra.Command{
		Use:   "new <dir>",
		Short: "Create a new plugin from a template (Go or JavaScript)",
		Example: "  pyro plugin new ./redis-plugin --kind protocol --id redis\n" +
			"  pyro plugin new ./redis-js --lang js --kind protocol --id redis\n" +
			"  pyro plugin new ./sigv4 --kind auth --id sigv4 --sdk ../pyro/plugins/sdk/go\n" +
			"  pyro plugin new ./jwt-check --kind processor --id jwt-check",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			if lang != "go" && lang != "js" {
				return errors.New("--lang must be go or js")
			}
			if lang == "js" && sdk != "" {
				return errors.New("--sdk only applies to --lang go")
			}
			if kind != plugin.KindProtocol && kind != plugin.KindAuth && kind != plugin.KindProcessor {
				return errors.New("--kind must be protocol, auth or processor")
			}
			if id == "" {
				id = strings.ToLower(filepath.Base(dir))
			}
			if _, err := os.Stat(dir); err == nil {
				if entries, _ := os.ReadDir(dir); len(entries) > 0 {
					return fmt.Errorf("%s exists and is not empty", dir)
				}
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			if sdk != "" {
				abs, err := filepath.Abs(sdk)
				if err != nil {
					return err
				}
				sdk = abs
			}
			version := sdkVersion
			if sdk != "" {
				version = "v0.0.0" // the replace directive points at the local copy
			}
			data := map[string]string{"ID": id, "Kind": kind, "SDK": sdk, "SDKVersion": version}
			for name, file := range newFiles(lang, kind) {
				src, err := pluginTemplates.ReadFile("templates/" + lang + "/" + file)
				if err != nil {
					return err
				}
				if strings.HasSuffix(file, ".tmpl") {
					var b bytes.Buffer
					if err := template.Must(template.New(name).Parse(string(src))).Execute(&b, data); err != nil {
						return err
					}
					src = b.Bytes()
				}
				if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
					return err
				}
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Created %s plugin %q in %s\n", kind, id, dir)
			if lang == "go" {
				fmt.Fprintf(out, "Fetch dependencies once: cd %s && go mod tidy\n", dir)
			}
			fmt.Fprintf(out, "Then build, install and rebuild on every save: pyro plugin dev %s\n", dir)
			return nil
		},
	}
	cmd.Flags().StringVar(&lang, "lang", "go", "go or js (JavaScript: no build step, runs on the embedded runtime)")
	cmd.Flags().StringVar(&kind, "kind", plugin.KindProtocol, "protocol, auth or processor")
	cmd.Flags().StringVar(&id, "id", "", "the type id (plugin:<id>); default: directory name")
	cmd.Flags().StringVar(&sdk, "sdk", "", "Go: use a local copy of the Go SDK instead of the published "+sdkVersion+" (adds a replace directive)")
	return cmd
}
