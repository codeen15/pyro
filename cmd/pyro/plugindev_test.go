package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/plugin"
)

// copyDir copies a testdata plugin into a temp dir the test can edit.
func copyDir(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// devManager is a fresh manager (own state) sharing the tests' compile cache.
func devManager(t *testing.T) *plugin.Manager {
	t.Helper()
	pd, err := pluginsDir()
	if err != nil {
		t.Fatal(err)
	}
	m, err := plugin.OpenWithCache(t.TempDir(), filepath.Join(pd, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

func edit(t *testing.T, path, from, to string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Git on Windows may check testdata out with CRLF line endings.
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	if !strings.Contains(s, from) {
		t.Fatalf("%s has no %q", path, from)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(s, from, to, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// sigOf signs an empty-bodied request with the installed js-echo plugin.
func sigOf(t *testing.T, m *plugin.Manager) string {
	t.Helper()
	req := &model.Request{Kind: model.KindHTTP, URL: "https://example.test/x", Body: &model.Body{Type: model.BodyText, Content: "payload"}}
	if _, err := m.Sign(context.Background(), "js-sign", req, nil); err != nil {
		t.Fatal(err)
	}
	for _, h := range req.Headers {
		if h.Key == "X-Sig" {
			return h.Value
		}
	}
	t.Fatalf("no X-Sig header: %+v", req.Headers)
	return ""
}

func TestPluginDevJS(t *testing.T) {
	dir := copyDir(t, echoPluginDir)
	m := devManager(t)

	var out bytes.Buffer
	if err := runDevCycle(dir, m, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	info, err := m.Get("js-echo")
	if err != nil || !info.Ready {
		t.Fatalf("info = %+v, %v", info, err)
	}
	if !strings.Contains(out.String(), "✓ js-echo 0.1.0 installed") {
		t.Errorf("output = %q", out.String())
	}
	if strings.Contains(out.String(), "built in") {
		t.Errorf("a JS plugin is not built: %q", out.String())
	}
	if !strings.Contains(out.String(), "  approved network: 127.0.0.1:*") {
		t.Errorf("first install should print approvals: %q", out.String())
	}
	first := sigOf(t, m)

	// Edit the script: the next cycle changes the reply, and the unchanged
	// capabilities are not announced again.
	edit(t, filepath.Join(dir, "plugin.js"), `hmacSHA256("secret"`, `hmacSHA256("other"`)
	out.Reset()
	if err := runDevCycle(dir, m, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if second := sigOf(t, m); second == first {
		t.Error("the edit did not take effect")
	}
	if strings.Contains(out.String(), "approved") {
		t.Errorf("unchanged capabilities announced again: %q", out.String())
	}

	// A syntax error is reported and the previous version keeps serving.
	good := sigOf(t, m)
	edit(t, filepath.Join(dir, "plugin.js"), `pyro.registerAuth(`, `pyro.registerAuth((`)
	out.Reset()
	if err := runDevCycle(dir, m, &out); err == nil {
		t.Fatal("a syntax error should fail the cycle")
	}
	if !strings.Contains(out.String(), "✗ install failed:") {
		t.Errorf("output = %q", out.String())
	}
	if again := sigOf(t, m); again != good {
		t.Error("the previous version stopped serving")
	}
	if info, _ := m.Get("js-echo"); !info.Ready {
		t.Errorf("not ready after a failed install: %+v", info)
	}

	// Changed capabilities and memory are approved and printed again.
	edit(t, filepath.Join(dir, "plugin.js"), `pyro.registerAuth((`, `pyro.registerAuth(`)
	edit(t, filepath.Join(dir, "pyro-plugin.yaml"), `capabilities:
  network: ["127.0.0.1:*"]`, `capabilities:
  network: ["127.0.0.1:*", "localhost:6379"]
limits:
  memoryMB: 300`)
	out.Reset()
	if err := runDevCycle(dir, m, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "  approved network: 127.0.0.1:*, localhost:6379") || !strings.Contains(out.String(), "  approved memory: 300 MB") {
		t.Errorf("output = %q", out.String())
	}
	if info, _ := m.Get("js-echo"); !info.Ready {
		t.Errorf("not ready after a capability change: %+v", info)
	}
}

func TestPluginDevGoBuildFailureKeepsPrevious(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a Go plugin to wasm")
	}
	dir := copyDir(t, "../../pkg/engine/plugin/testdata/alloc")
	sdk, err := filepath.Abs("../../plugins/sdk/go")
	if err != nil {
		t.Fatal(err)
	}
	edit(t, filepath.Join(dir, "go.mod"), "../../../../../plugins/sdk/go", sdk)
	m := devManager(t)

	var out bytes.Buffer
	if err := runDevCycle(dir, m, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "✓ alloc 0.1.0 installed (built in ") {
		t.Errorf("output = %q", out.String())
	}
	before, err := os.ReadFile(filepath.Join(dir, "alloc.wasm"))
	if err != nil {
		t.Fatal(err)
	}

	// Break the build and bump the manifest version: the old 0.1.0 stays.
	f, err := os.OpenFile(filepath.Join(dir, "main.go"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("\nvar _ int = \"not an int\"\n")
	f.Close()
	edit(t, filepath.Join(dir, "pyro-plugin.yaml"), "version: 0.1.0", "version: 0.2.0")
	out.Reset()
	if err := runDevCycle(dir, m, &out); err == nil {
		t.Fatal("a compile error should fail the cycle")
	}
	if !strings.Contains(out.String(), "✗ build failed:") || !strings.Contains(out.String(), "cannot use") {
		t.Errorf("output = %q", out.String())
	}
	info, err := m.Get("alloc")
	if err != nil || info.Version != "0.1.0" || !info.Ready {
		t.Fatalf("previous version not intact: %+v, %v", info, err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "alloc.wasm"))
	if !bytes.Equal(before, after) {
		t.Error("a failed build replaced the module")
	}
}

func TestPluginDevFingerprint(t *testing.T) {
	dir := copyDir(t, echoPluginDir)
	base, err := devFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Dot-files and node_modules are ignored.
	os.WriteFile(filepath.Join(dir, ".swp"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(dir, "node_modules", "x"), 0o755)
	os.WriteFile(filepath.Join(dir, "node_modules", "x", "i.js"), []byte("x"), 0o644)
	if got, _ := devFingerprint(dir); got != base {
		t.Error("ignored files changed the fingerprint")
	}
	time.Sleep(10 * time.Millisecond)
	edit(t, filepath.Join(dir, "plugin.js"), "dialing", "dialling")
	if got, _ := devFingerprint(dir); got == base {
		t.Error("an edit did not change the fingerprint")
	}

	// A wasm plugin's built module is excluded, so a rebuild doesn't retrigger.
	wdir := t.TempDir()
	os.WriteFile(filepath.Join(wdir, "pyro-plugin.yaml"), []byte("name: w\nversion: 0.1.0\nabi: 1\nmodule: w.wasm\nprovides: [{kind: protocol, id: w}]\ncapabilities: {}\n"), 0o644)
	os.WriteFile(filepath.Join(wdir, "w.wasm"), []byte("a"), 0o644)
	w1, _ := devFingerprint(wdir)
	os.WriteFile(filepath.Join(wdir, "w.wasm"), []byte("bbbb"), 0o644)
	if w2, _ := devFingerprint(wdir); w2 != w1 {
		t.Error("the built module changed the fingerprint")
	}
}

func TestPluginDevOnce(t *testing.T) {
	dir := copyDir(t, echoPluginDir)
	cmd := pluginDevCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{dir, "--once"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "✓ js-echo 0.1.0 installed") {
		t.Errorf("output = %q", out.String())
	}
	edit(t, filepath.Join(dir, "plugin.js"), `pyro.registerAuth(`, `pyro.registerAuth((`)
	cmd = pluginDevCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{dir, "--once"})
	if err := cmd.Execute(); err == nil {
		t.Error("--once should fail when the cycle fails")
	}
}
