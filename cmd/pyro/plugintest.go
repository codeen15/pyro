package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/codeen15/pyro/pkg/engine/check"
	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/plugin"
	"github.com/codeen15/pyro/pkg/engine/protocol"
)

// pluginTestDir, when set (by tests), is the folder a directory argument is
// installed into, so the compilation cache survives between invocations. By
// default each run uses a fresh temporary folder.
var pluginTestDir string

// pluginTestCmd runs one plugin call from the terminal, so authors can try a
// plugin without building a collection around it.
func pluginTestCmd() *cobra.Command {
	var (
		protocolID, authID, processorID string
		url, method, body, phase        string
		respBody                        string
		status                          int
		asJSON                          bool
		timeout                         time.Duration
		headers, opts, params, config   []string
	)
	cmd := &cobra.Command{
		Use:   "test <dir | name>",
		Short: "Run one call of a plugin and print the result",
		Long: "Runs one call of a plugin's request type, auth type or processor and prints what it returned,\n" +
			"including its log lines. A directory is installed into a temporary folder first (its permissions\n" +
			"are approved for you); a name must be an installed, approved plugin. With one feature the\n" +
			"--protocol, --auth and --processor flags are optional. Exits 1 if the call fails or a processor\n" +
			"test fails.",
		Example: "  pyro plugin test ./my-plugin --protocol redis --url redis://localhost:6379 --body 'PING'\n" +
			"  pyro plugin test ./my-plugin --auth sign --url https://api.example.com/orders --body '{}' --param secret=s3cret\n" +
			"  pyro plugin test ./my-plugin --processor check --phase post --status 200 --response-body '{\"ok\":true}'\n" +
			"  pyro plugin test redis --json",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n := 0
			for _, s := range []string{protocolID, authID, processorID} {
				if s != "" {
					n++
				}
			}
			if n > 1 {
				return errors.New("pick only one of --protocol, --auth and --processor")
			}
			if phase != "pre" && phase != "post" {
				return fmt.Errorf("--phase must be pre or post, not %q", phase)
			}
			hdrs, err := keyValues(headers, "--header")
			if err != nil {
				return err
			}
			optMap, err := keyValueMap(opts, "--opt")
			if err != nil {
				return err
			}
			paramMap, err := keyValueMap(params, "--param")
			if err != nil {
				return err
			}
			cfgMap, err := keyValueMap(config, "--config")
			if err != nil {
				return err
			}

			m, info, cleanup, err := openForTest(args[0], cfgMap)
			if err != nil {
				return err
			}
			defer cleanup()

			kind, id, err := pickFeature(info, protocolID, authID, processorID)
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			req := &model.Request{Kind: model.KindHTTP, Name: "plugin test", Method: method, URL: url, Headers: hdrs, Options: optMap}
			if body != "" {
				req.Body = &model.Body{Type: model.BodyText, Content: body}
			}
			out := cmd.OutOrStdout()
			t := &testRun{out: out, errOut: cmd.ErrOrStderr(), json: asJSON}
			switch kind {
			case plugin.KindProtocol:
				req.Kind = model.Kind("plugin:" + id)
				return t.protocol(ctx, m, id, req)
			case plugin.KindAuth:
				return t.auth(ctx, m, id, req, paramMap)
			default:
				// A processor's options come from --param (or --opt).
				popts := map[string]string{}
				for _, mm := range []map[string]string{optMap, paramMap} {
					for k, v := range mm {
						popts[k] = v
					}
				}
				in := plugin.ProcessInput{Phase: phase, Request: req, Vars: map[string]string{}, Options: popts}
				if phase == "post" {
					in.Response = &protocol.Response{Status: status, StatusText: statusText(status), Body: []byte(respBody), Size: int64(len(respBody))}
				}
				return t.process(ctx, m, id, in)
			}
		},
	}
	f := cmd.Flags()
	f.StringVar(&protocolID, "protocol", "", "test this request type (protocol plugin feature id)")
	f.StringVar(&authID, "auth", "", "test this auth type")
	f.StringVar(&processorID, "processor", "", "test this processor")
	f.StringVar(&url, "url", "", "request URL")
	f.StringVar(&method, "method", "", "request method")
	f.StringVar(&body, "body", "", "request body")
	f.StringArrayVar(&headers, "header", nil, "request header, k=v (repeatable)")
	f.StringArrayVar(&opts, "opt", nil, "request option for a protocol plugin, k=v (repeatable)")
	f.StringArrayVar(&params, "param", nil, "auth parameter or processor option, k=v (repeatable)")
	f.StringArrayVar(&config, "config", nil, "plugin setting, k=v (repeatable; directories only)")
	f.StringVar(&phase, "phase", "post", "processor phase: pre or post")
	f.IntVar(&status, "status", 200, "processor: response status (post phase)")
	f.StringVar(&respBody, "response-body", "", "processor: response body (post phase)")
	f.BoolVar(&asJSON, "json", false, "print the result as JSON")
	f.DurationVar(&timeout, "timeout", 30*time.Second, "give up after this long")
	return cmd
}

// openForTest returns a manager holding the plugin. A directory is installed
// (approved) into a temporary manager; a name must already be approved.
func openForTest(arg string, cfg map[string]string) (*plugin.Manager, plugin.Info, func(), error) {
	noop := func() {}
	if st, err := os.Stat(arg); err == nil && st.IsDir() {
		dir, remove := pluginTestDir, noop
		if dir == "" {
			tmp, err := os.MkdirTemp("", "pyro-plugin-test-")
			if err != nil {
				return nil, plugin.Info{}, noop, err
			}
			dir, remove = tmp, func() { os.RemoveAll(tmp) }
		}
		m, err := plugin.Open(dir)
		if err != nil {
			remove()
			return nil, plugin.Info{}, noop, err
		}
		cleanup := func() { m.Close(); remove() }
		info, err := m.InstallDir(arg, true)
		if err == nil && len(cfg) > 0 {
			info, err = m.SetConfig(info.Name, cfg)
		}
		if err != nil {
			cleanup()
			return nil, plugin.Info{}, noop, err
		}
		return m, info, cleanup, nil
	}
	if len(cfg) > 0 {
		return nil, plugin.Info{}, noop, errors.New("--config only applies when testing a directory; an installed plugin uses its saved settings")
	}
	m, err := managerOrErr()
	if err != nil {
		return nil, plugin.Info{}, noop, err
	}
	info, err := m.Get(arg)
	if err != nil {
		m.Close()
		return nil, plugin.Info{}, noop, fmt.Errorf("%q is not a directory or an installed plugin: %w", arg, err)
	}
	if !info.Approved {
		m.Close()
		return nil, plugin.Info{}, noop, fmt.Errorf("plugin %s is not approved yet; run: pyro plugin approve %s", info.Name, info.Name)
	}
	return m, info, m.Close, nil
}

// pickFeature resolves which provided feature to run.
func pickFeature(info plugin.Info, protocolID, authID, processorID string) (kind, id string, err error) {
	switch {
	case protocolID != "":
		kind, id = plugin.KindProtocol, protocolID
	case authID != "":
		kind, id = plugin.KindAuth, authID
	case processorID != "":
		kind, id = plugin.KindProcessor, processorID
	case len(info.Provides) == 1:
		return info.Provides[0].Kind, info.Provides[0].ID, nil
	default:
		return "", "", fmt.Errorf("this plugin provides %d features; pick one with --protocol, --auth or --processor", len(info.Provides))
	}
	if !slices.ContainsFunc(info.Provides, func(p plugin.Provide) bool { return p.Kind == kind && p.ID == id }) {
		return "", "", fmt.Errorf("plugin %s provides no %s %q (it provides: %s)", info.Name, kind, id, provides(info.Manifest))
	}
	return kind, id, nil
}

type testRun struct {
	out, errOut io.Writer
	json        bool
}

func (t *testRun) logs(logs []plugin.Log) {
	if t.json {
		return
	}
	for _, l := range logs {
		fmt.Fprintf(t.errOut, "[%s] %s\n", l.Level, l.Message)
	}
}

func (t *testRun) emit(v any) error {
	enc := json.NewEncoder(t.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func nonNilLogs(l []plugin.Log) []plugin.Log {
	if l == nil {
		return []plugin.Log{}
	}
	return l
}

func (t *testRun) protocol(ctx context.Context, m *plugin.Manager, id string, req *model.Request) error {
	res, logs, err := m.Execute(ctx, id, req)
	t.logs(logs)
	if err != nil {
		return err
	}
	if t.json {
		return t.emit(map[string]any{
			"kind": plugin.KindProtocol, "id": id,
			"response": map[string]any{
				"status": res.Status, "statusText": res.StatusText, "headers": res.Headers,
				"body": string(res.Body), "size": res.Size, "remoteAddr": res.RemoteAddr,
				"timeMs": float64(res.Timings.Total) / float64(time.Millisecond),
			},
			"logs": nonNilLogs(logs),
		})
	}
	line := res.StatusText
	if res.Status != 0 {
		line = strings.TrimSpace(fmt.Sprintf("%d %s", res.Status, res.StatusText))
	}
	fmt.Fprintf(t.out, "%s (%.0f ms)\n", line, float64(res.Timings.Total)/float64(time.Millisecond))
	for _, h := range res.Headers {
		fmt.Fprintf(t.out, "%s: %s\n", h.Key, h.Value)
	}
	fmt.Fprintf(t.out, "\n%s\n", res.Body)
	return nil
}

func (t *testRun) auth(ctx context.Context, m *plugin.Manager, id string, req *model.Request, params map[string]string) error {
	logs, err := m.Sign(ctx, id, req, params)
	t.logs(logs)
	if err != nil {
		return err
	}
	if t.json {
		return t.emit(map[string]any{
			"kind": plugin.KindAuth, "id": id,
			"request": map[string]any{"url": req.URL, "query": nonNilKV(req.Query), "headers": nonNilKV(req.Headers)},
			"logs":    nonNilLogs(logs),
		})
	}
	fmt.Fprintf(t.out, "Signed. The request is now:\n%s\n", req.URL)
	for _, q := range req.Query {
		fmt.Fprintf(t.out, "  ?%s=%s\n", q.Key, q.Value)
	}
	for _, h := range req.Headers {
		fmt.Fprintf(t.out, "%s: %s\n", h.Key, h.Value)
	}
	return nil
}

func nonNilKV(kv []model.KeyValue) []model.KeyValue {
	if kv == nil {
		return []model.KeyValue{}
	}
	return kv
}

func (t *testRun) process(ctx context.Context, m *plugin.Manager, id string, in plugin.ProcessInput) error {
	res, err := m.Process(ctx, id, in)
	t.logs(res.Logs)
	if err != nil {
		return err
	}
	failed := 0
	for _, r := range res.Tests {
		if !r.Passed {
			failed++
		}
	}
	if t.json {
		if err := t.emit(map[string]any{
			"kind": plugin.KindProcessor, "id": id, "phase": in.Phase,
			"vars": nonNilMap(res.Vars), "request": res.Request, "tests": nonNilTests(res.Tests), "logs": nonNilLogs(res.Logs),
		}); err != nil {
			return err
		}
	} else {
		for _, r := range res.Tests {
			mark := "PASS"
			if !r.Passed {
				mark = "FAIL"
			}
			fmt.Fprintf(t.out, "%s  %s", mark, r.Name)
			if r.Error != "" {
				fmt.Fprintf(t.out, "  (%s)", r.Error)
			}
			fmt.Fprintln(t.out)
		}
		if len(res.Tests) == 0 {
			fmt.Fprintln(t.out, "No tests reported.")
		}
		for _, k := range sortedKeys(res.Vars) {
			fmt.Fprintf(t.out, "set %s = %s\n", k, res.Vars[k])
		}
		if res.Request != nil {
			fmt.Fprintf(t.out, "request rewritten: %s\n", res.Request.URL)
			for _, h := range res.Request.Headers {
				fmt.Fprintf(t.out, "%s: %s\n", h.Key, h.Value)
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d tests failed", failed, len(res.Tests))
	}
	return nil
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func nonNilTests(r []check.Result) []check.Result {
	if r == nil {
		return []check.Result{}
	}
	return r
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func statusText(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "OK"
	case code >= 400 && code < 500:
		return "Client Error"
	case code >= 500:
		return "Server Error"
	}
	return ""
}

// splitPair splits "k=v" (or "k: v" for headers) at the first separator.
func splitPair(s string) (k, v string, ok bool) {
	i := strings.IndexAny(s, "=:")
	if i <= 0 {
		return "", "", false
	}
	return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:]), true
}

func keyValues(pairs []string, flag string) ([]model.KeyValue, error) {
	var out []model.KeyValue
	for _, p := range pairs {
		k, v, ok := splitPair(p)
		if !ok {
			return nil, fmt.Errorf("%s %q: expected k=v", flag, p)
		}
		out = append(out, model.KeyValue{Key: k, Value: v})
	}
	return out, nil
}

func keyValueMap(pairs []string, flag string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("%s %q: expected k=v", flag, p)
		}
		out[k] = v
	}
	return out, nil
}
