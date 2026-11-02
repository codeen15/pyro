package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
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
				fmt.Printf("%-14s %-9s %-16s %s\n", in.Name, in.Version, status(in), provides(in.Manifest))
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

	cmd.AddCommand(list, install, approve, toggle("enable", true), toggle("disable", false), remove, search, newPluginCmd(), packCmd())
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
			if err != nil {
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

func newPluginCmd() *cobra.Command {
	var kind, id, sdk string
	cmd := &cobra.Command{
		Use:   "new <dir>",
		Short: "Create a new Go plugin from a template",
		Example: "  pyro plugin new ./redis-plugin --kind protocol --id redis\n" +
			"  pyro plugin new ./sigv4 --kind auth --id sigv4 --sdk ../pyro/plugins/sdk/go\n" +
			"  pyro plugin new ./jwt-check --kind processor --id jwt-check",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
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
			data := map[string]string{"ID": id, "Kind": kind, "SDK": sdk}
			files := map[string]string{"go.mod": tmplGoMod, "pyro-plugin.yaml": tmplManifest, "README.md": tmplReadme}
			switch kind {
			case plugin.KindProtocol:
				files["main.go"] = tmplProtocol
			case plugin.KindProcessor:
				files["main.go"] = tmplProcessor
			default:
				files["main.go"] = tmplAuth
			}
			for name, src := range files {
				var b bytes.Buffer
				if err := template.Must(template.New(name).Parse(src)).Execute(&b, data); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(dir, name), b.Bytes(), 0o644); err != nil {
					return err
				}
			}
			fmt.Printf("Created %s plugin %q in %s\nBuild:   cd %s && GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o %s.wasm .\nInstall: pyro plugin install %s\n",
				kind, id, dir, dir, id, dir)
			if sdk == "" {
				fmt.Println("Note: the SDK module isn't published yet; pass --sdk <path to pyro/plugins/sdk/go> to use a local copy.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "kind", plugin.KindProtocol, "protocol, auth or processor")
	cmd.Flags().StringVar(&id, "id", "", "the type id (plugin:<id>); default: directory name")
	cmd.Flags().StringVar(&sdk, "sdk", "", "path to a local copy of the Go SDK (adds a replace directive)")
	return cmd
}

const tmplGoMod = `module example.com/pyro-plugin-{{.ID}}

go 1.24

require github.com/codeen15/pyro/plugins/sdk/go v0.0.0
{{if .SDK}}
replace github.com/codeen15/pyro/plugins/sdk/go => {{.SDK}}
{{end}}`

const tmplManifest = `name: {{.ID}}
version: 0.1.0
abi: 1
title: {{.ID}}
description: Describe what this plugin does.
module: {{.ID}}.wasm
provides:
  - kind: {{.Kind}}
    id: {{.ID}}
    title: {{.ID}}
{{- if eq .Kind "protocol"}}
    urlPlaceholder: {{.ID}}://localhost:1234
    fields:
      - { key: timeoutMs, label: Timeout (ms), type: number, default: "5000" }
capabilities:
  # host:port patterns this plugin may connect to (matched literally, so list
  # both names you use); users approve these when installing.
  network: ["localhost:*", "127.0.0.1:*"]
{{- else if eq .Kind "processor"}}
    phases: [pre, post]
    fields:
      - { key: var, label: Variable, type: string, default: requestId }
capabilities: {}
{{- else}}
    fields:
      - { key: secret, label: Secret, type: secret }
capabilities: {}
{{- end}}
`

const tmplProtocol = `// Command {{.ID}} is a Pyro protocol plugin: requests of kind plugin:{{.ID}}.
package main

import (
	"fmt"
	"strings"
	"time"

	pyro "github.com/codeen15/pyro/plugins/sdk/go"
)

func init() {
	pyro.RegisterProtocol("{{.ID}}", pyro.ProtocolFunc(execute))
}

func main() {}

func execute(ctx *pyro.Context, req *pyro.Request) (*pyro.Response, error) {
	addr := strings.TrimPrefix(req.URL, "{{.ID}}://")
	conn, err := pyro.Dial(addr, false, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(req.Body)); err != nil {
		return nil, err
	}
	buf := make([]byte, 64<<10)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("reading reply: %w", err)
	}
	pyro.Logf(pyro.LevelDebug, "read %d bytes from %s", n, addr)
	return &pyro.Response{StatusText: "OK", Body: buf[:n], RemoteAddr: addr}, nil
}
`

const tmplAuth = `// Command {{.ID}} is a Pyro auth plugin: auth type plugin:{{.ID}}.
package main

import (
	"errors"

	pyro "github.com/codeen15/pyro/plugins/sdk/go"
)

func init() {
	pyro.RegisterAuth("{{.ID}}", pyro.AuthFunc(sign))
}

func main() {}

func sign(ctx *pyro.Context, req *pyro.Request) error {
	secret := ctx.Params["secret"]
	if secret == "" {
		return errors.New("secret is empty")
	}
	req.SetHeader("Authorization", "Custom "+secret)
	return nil
}
`

const tmplProcessor = `// Command {{.ID}} is a Pyro processor plugin: pre/post processor plugin:{{.ID}}.
package main

import (
	"fmt"
	"time"

	pyro "github.com/codeen15/pyro/plugins/sdk/go"
)

func init() {
	pyro.RegisterProcessor("{{.ID}}", pyro.ProcessorFunc(process))
}

func main() {}

func process(ctx *pyro.Context, p *pyro.Process) error {
	name := ctx.Params["var"]
	if name == "" {
		name = "requestId"
	}
	if p.Phase == "pre" {
		// Before the request: set a variable and add a header.
		id := fmt.Sprintf("req-%d", time.Now().UnixNano())
		p.SetVar(name, id)
		p.Request.SetHeader("X-Request-Id", id)
		return nil
	}
	// After the response: report a check.
	p.Test("status is below 500", p.Response.Status < 500, fmt.Sprintf("got %d", p.Response.Status))
	return nil
}
`

const tmplReadme = `# {{.ID}} (Pyro plugin)

Build:

    GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o {{.ID}}.wasm .

Install and approve:

    pyro plugin install .

Package for sharing (prints the sha256 for an index entry):

    pyro plugin pack .

See docs/plugins.md in the Pyro repository for the SDK and ABI.
`
