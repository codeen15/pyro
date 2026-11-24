package plugin

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/protocol"
)

func jsDir(name string) string { return filepath.Join("testdata", "js", name) }

// installJS installs testdata/js/<name>, approved, and fails the test on error.
func installJS(t *testing.T, m *Manager, name string) Info {
	t.Helper()
	info, err := m.InstallDir(jsDir(name), true)
	if err != nil || !info.Ready {
		t.Fatalf("install %s = %+v, %v", name, info, err)
	}
	return info
}

// copyJS copies testdata/js/<name> to a temp dir, appending extra to the
// manifest, and returns the copy's path.
func copyJS(t *testing.T, name, extra string) string {
	t.Helper()
	dir := t.TempDir()
	manifest, err := os.ReadFile(filepath.Join(jsDir(name), ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join(jsDir(name), "plugin.js"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), append(manifest, []byte(extra)...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.js"), script, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func echoReq(addr, body string) *model.Request {
	return &model.Request{Kind: "plugin:js-echo", Name: "e", URL: "tcp://" + addr, Body: &model.Body{Type: model.BodyText, Content: body}}
}

func TestJSProtocolEcho(t *testing.T) {
	m := manager(t)
	info := installJS(t, m, "echo")
	if info.Runtime != RuntimeJS || info.Limits.MemoryMB != DefaultMemoryMBJS || info.Limits.MaxInstances != DefaultMaxInstances {
		t.Fatalf("info = %+v", info)
	}
	addr := echoServer(t)
	res, logs, err := m.Execute(context.Background(), "js-echo", echoReq(addr, "hi"))
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Body) != "echo:hi" {
		t.Fatalf("body = %q", res.Body)
	}
	found := false
	for _, l := range logs {
		if l.Level == "info" && l.Message == "dialing "+addr {
			found = true
		}
	}
	if !found {
		t.Fatalf("logs = %+v, want the plugin's info line", logs)
	}
	// A second call reuses the pooled instance (no second load).
	if res, _, err := m.Execute(context.Background(), "js-echo", echoReq(addr, "again")); err != nil || string(res.Body) != "echo:again" {
		t.Fatalf("second call = %v, %v", res, err)
	}
}

func TestJSAuthAndProcessor(t *testing.T) {
	m := manager(t)
	installJS(t, m, "echo")
	req := &model.Request{Kind: model.KindHTTP, Method: "POST", URL: "http://x", Body: &model.Body{Type: model.BodyText, Content: "payload"}}
	if _, err := m.Sign(context.Background(), "js-sign", req, nil); err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write([]byte("payload"))
	want := hex.EncodeToString(mac.Sum(nil))
	got := ""
	for _, h := range req.Headers {
		if h.Key == "X-Sig" {
			got = h.Value
		}
	}
	if got != want {
		t.Fatalf("X-Sig = %q, want %q (headers %+v)", got, want, req.Headers)
	}

	out, err := m.Process(context.Background(), "js-check", ProcessInput{
		Phase: "post", Request: req, Response: &protocol.Response{Status: 200},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Vars["seen"] != "1" {
		t.Fatalf("vars = %+v", out.Vars)
	}
	if len(out.Tests) != 1 || out.Tests[0].Name != "status ok" || !out.Tests[0].Passed {
		t.Fatalf("tests = %+v", out.Tests)
	}
}

func TestJSCapabilityDenied(t *testing.T) {
	m := manager(t)
	dir := copyJS(t, "echo", "")
	manifest, _ := os.ReadFile(filepath.Join(dir, ManifestFile))
	manifest = bytes.Replace(manifest, []byte(`["127.0.0.1:*"]`), []byte(`["localhost:1"]`), 1)
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InstallDir(dir, true); err != nil {
		t.Fatal(err)
	}
	_, _, err := m.Execute(context.Background(), "js-echo", echoReq(echoServer(t), "hi"))
	if err == nil || !strings.Contains(err.Error(), "may not connect to") {
		t.Fatalf("err = %v, want a capability error", err)
	}
}

func TestJSSyntaxErrorFailsInstall(t *testing.T) {
	m := manager(t)
	_, err := m.InstallDir(jsDir("syntax"), true)
	if err == nil || !strings.Contains(err.Error(), "plugin.js:") || !strings.Contains(err.Error(), "SyntaxError") {
		t.Fatalf("err = %v, want the JS syntax error with its position", err)
	}
	if _, err := m.Get("js-syntax"); err == nil {
		t.Fatal("a plugin that failed to load must not be installed")
	}
}

func TestJSThrowAtLoadFailsInstall(t *testing.T) {
	m := manager(t)
	dir := copyJS(t, "throws", "")
	if err := os.WriteFile(filepath.Join(dir, "plugin.js"), []byte("const x = 1\nthrow new TypeError(\"bad start\")\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := m.InstallDir(dir, true)
	if err == nil || !strings.Contains(err.Error(), "plugin.js:2:") || !strings.Contains(err.Error(), "TypeError: bad start") {
		t.Fatalf("err = %v, want the JS error with its line", err)
	}
}

func TestJSScriptChecks(t *testing.T) {
	m := manager(t)
	dir := copyJS(t, "throws", "")
	if err := os.WriteFile(filepath.Join(dir, "plugin.js"), []byte("// \xff\xfe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InstallDir(dir, true); err == nil || err.Error() != "plugin.js is not valid UTF-8" {
		t.Fatalf("err = %v", err)
	}
	big := bytes.Repeat([]byte("// padding padding padding padding\n"), (4<<20)/35+1)
	if err := os.WriteFile(filepath.Join(dir, "plugin.js"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InstallDir(dir, true); err == nil || err.Error() != "plugin.js is larger than 4 MB" {
		t.Fatalf("err = %v", err)
	}
}

func TestJSThrowIsAnErrorReply(t *testing.T) {
	m := manager(t)
	installJS(t, m, "throws")
	_, _, err := m.Execute(context.Background(), "js-throws", &model.Request{Kind: "plugin:js-throws", URL: "x://"})
	if err == nil || !strings.Contains(err.Error(), "Error: boom") {
		t.Fatalf("err = %v, want Error: boom", err)
	}
	// An error reply keeps the instance; the plugin still answers.
	_, _, err = m.Execute(context.Background(), "js-throws", &model.Request{Kind: "plugin:js-throws", URL: "x://"})
	if err == nil || !strings.Contains(err.Error(), "Error: boom") {
		t.Fatalf("second call err = %v", err)
	}
}

func TestJSInfiniteLoopTimesOut(t *testing.T) {
	m := manager(t)
	installJS(t, m, "loop")
	installJS(t, m, "echo")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	_, _, err := m.Execute(ctx, "js-loop", &model.Request{Kind: "plugin:js-loop", URL: "x://"})
	if err == nil || err.Error() != "plugin timed out" {
		t.Fatalf("err = %v, want plugin timed out", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("timed-out call took %v", d)
	}
	addr := echoServer(t)
	if res, _, err := m.Execute(context.Background(), "js-echo", echoReq(addr, "after")); err != nil || string(res.Body) != "echo:after" {
		t.Fatalf("echo after the loop = %v, %v", res, err)
	}
}

// TestJSMemoryLimit: the alloc plugin keeps 300 MiB of Uint8Arrays alive,
// which fails under the default 256 MB and succeeds under 512 MB.
func TestJSMemoryLimit(t *testing.T) {
	small := manager(t)
	installJS(t, small, "alloc")
	req := &model.Request{Kind: "plugin:js-alloc", URL: "x://", Options: map[string]string{"mb": "300"}}
	if _, _, err := small.Execute(context.Background(), "js-alloc", req); err == nil {
		t.Fatal("allocating 300 MiB under the default 256 MB: want an error")
	}
	// The plugin still works after the failure.
	if _, _, err := small.Execute(context.Background(), "js-alloc", &model.Request{Kind: "plugin:js-alloc", URL: "x://", Options: map[string]string{"mb": "1"}}); err != nil {
		t.Fatalf("1 MiB after the failure: %v", err)
	}

	big := manager(t)
	if info, err := big.InstallDir(copyJS(t, "alloc", "limits: {memoryMB: 512}\n"), true); err != nil || !info.Ready || info.Limits.MemoryMB != 512 {
		t.Fatalf("install = %+v, %v", info, err)
	}
	res, _, err := big.Execute(context.Background(), "js-alloc", req)
	if err != nil || string(res.Body) != "allocated 300 MiB" {
		t.Fatalf("allocating 300 MiB under 512 MB = %v, %v", res, err)
	}
}

func TestJSArchiveInstall(t *testing.T) {
	m := manager(t)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{ManifestFile, "plugin.js"} {
		data, err := os.ReadFile(filepath.Join(jsDir("echo"), name))
		if err != nil {
			t.Fatal(err)
		}
		w, _ := zw.Create("js-echo/" + name)
		w.Write(data)
	}
	zw.Close()
	info, err := m.InstallArchive(buf.Bytes(), true)
	if err != nil || !info.Ready {
		t.Fatalf("install = %+v, %v", info, err)
	}
	if res, _, err := m.Execute(context.Background(), "js-echo", echoReq(echoServer(t), "zip")); err != nil || string(res.Body) != "echo:zip" {
		t.Fatalf("execute = %v, %v", res, err)
	}
}

// TestJSFoundAtOpen: a JS plugin already installed when the manager opens
// works (the runtime compiles in the background).
func TestJSFoundAtOpen(t *testing.T) {
	dir := t.TempDir()
	m1, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	installJS(t, m1, "echo")
	m1.Close()

	m2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m2.Close)
	// Open started the compile in the background...
	var started *jsrtCompile
	for deadline := time.Now().Add(5 * time.Second); started == nil && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		m2.mu.Lock()
		if h := m2.hosts[DefaultMemoryMBJS]; h != nil {
			h.jsMu.Lock()
			started = h.js
			h.jsMu.Unlock()
		}
		m2.mu.Unlock()
	}
	if started == nil {
		t.Fatal("Open did not start compiling the JS runtime")
	}
	// ...and the request uses that compile rather than starting another.
	defer func() {
		h := m2.hosts[DefaultMemoryMBJS]
		if h == nil || h.js != started {
			t.Error("the request compiled the JS runtime again")
		}
	}()
	if res, _, err := m2.Execute(context.Background(), "js-echo", echoReq(echoServer(t), "open")); err != nil || string(res.Body) != "echo:open" {
		t.Fatalf("execute = %v, %v", res, err)
	}
}

// TestJSRealClockAndRandom: plugins see the real wall clock, and each
// instance gets its own random numbers (WASI's defaults are a fixed 2022
// clock and the same random sequence in every instance).
func TestJSRealClockAndRandom(t *testing.T) {
	call := func() (int64, float64) {
		m := manager(t) // a new manager, so a fresh instance
		installJS(t, m, "clock")
		res, _, err := m.Execute(context.Background(), "js-clock", &model.Request{Kind: "plugin:js-clock", Name: "c"})
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			Now    int64   `json:"now"`
			Random float64 `json:"random"`
		}
		if err := json.Unmarshal(res.Body, &out); err != nil {
			t.Fatalf("body %q: %v", res.Body, err)
		}
		return out.Now, out.Random
	}
	now1, r1 := call()
	if d := time.Since(time.UnixMilli(now1)); d < -time.Minute || d > time.Minute {
		t.Fatalf("Date.now() = %v, host time %v", time.UnixMilli(now1), time.Now())
	}
	_, r2 := call()
	if r1 == r2 {
		t.Fatalf("Math.random() gave %v in both fresh instances", r1)
	}
}
