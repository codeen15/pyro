package plugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codeen15/pyro/pkg/engine/model"
)

const baseManifest = "name: p\nversion: 1.0.0\nabi: 1\nprovides: [{kind: protocol, id: p}]\ncapabilities: {}\n"

func TestManifestRuntimeAndLimits(t *testing.T) {
	m, err := ParseManifest([]byte(baseManifest + "runtime: js\nmodule: plugin.js\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Runtime != RuntimeJS || !m.IsJS() || m.Limits.MemoryMB != 256 || m.Limits.MaxInstances != 32 {
		t.Fatalf("js defaults = %+v", m)
	}

	m, err = ParseManifest([]byte(baseManifest + "module: x.wasm\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Runtime != RuntimeWasm || m.IsJS() || m.Limits.MemoryMB != 128 || m.Limits.MaxInstances != 0 {
		t.Fatalf("wasm defaults = %+v", m)
	}

	m, err = ParseManifest([]byte(baseManifest + "runtime: js\nmodule: plugin.js\nlimits: {memoryMB: 512, maxInstances: 4}\n"))
	if err != nil || m.Limits.MemoryMB != 512 || m.Limits.MaxInstances != 4 {
		t.Fatalf("explicit limits = %+v, %v", m, err)
	}

	for body, want := range map[string]string{
		"runtime: js\nmodule: x.wasm\n":                "module must be a .js file",
		"runtime: wasm\nmodule: x.js\n":                "module must be a .wasm file",
		"module: x.js\n":                               "module must be a .wasm file",
		"runtime: lua\nmodule: x.wasm\n":               "runtime must be wasm or js",
		"module: x.wasm\nlimits: {memoryMB: 2048}\n":   "memoryMB must be at most 1024",
		"module: x.wasm\nlimits: {maxInstances: -1}\n": "maxInstances must not be negative",
	} {
		if _, err := ParseManifest([]byte(baseManifest + body)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want error containing %q, got %v", body, want, err)
		}
	}
}

func TestLookup(t *testing.T) {
	m := manager(t)
	if _, ok := m.Lookup("protocol", "tcp"); ok {
		t.Fatal("Lookup found a plugin before any was installed")
	}
	if _, err := m.InstallDir(built["tcp"], true); err != nil {
		t.Fatal(err)
	}
	info, ok := m.Lookup("protocol", "tcp")
	if !ok || info.Name != "tcp" || !info.Ready {
		t.Fatalf("Lookup(protocol, tcp) = %+v, %v", info, ok)
	}
	if _, ok := m.Lookup("auth", "tcp"); ok {
		t.Fatal("Lookup matched the wrong kind")
	}
}

func TestApprovalCoversMemoryLimit(t *testing.T) {
	m := manager(t)
	if info, err := m.InstallDir(built["tcp"], true); err != nil || !info.Approved {
		t.Fatalf("install approved = %+v, %v", info, err)
	}
	dir := t.TempDir()
	manifest, _ := os.ReadFile(filepath.Join(built["tcp"], ManifestFile))
	module, _ := os.ReadFile(filepath.Join(built["tcp"], "tcp.wasm"))
	bigger := string(manifest) + "\nlimits: { memoryMB: 512 }\n"
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), []byte(bigger), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tcp.wasm"), module, 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := m.InstallDir(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if info.Limits.MemoryMB != 512 || info.Approved || info.Ready {
		t.Fatalf("a larger memory limit must need approval, got %+v", info)
	}
	if info, _ = m.Approve("tcp"); !info.Approved {
		t.Fatalf("Approve must cover the new limit, got %+v", info)
	}
	// Reinstalling the same limit keeps the approval.
	if info, _ = m.InstallDir(dir, false); !info.Approved {
		t.Fatal("unchanged limit must stay approved")
	}
}

func TestLegacyApprovalStillValid(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.InstallDir(built["tcp"], true); err != nil {
		t.Fatal(err)
	}
	first.Close()
	// State written before approvedMemoryMB existed.
	legacy := `{"plugins":{"tcp":{"enabled":true,"approved":{"network":["*:*"]}}}}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	info, err := m.Get("tcp")
	if err != nil {
		t.Fatal(err)
	}
	if !info.Approved || !info.Ready {
		t.Fatalf("approval without approvedMemoryMB must count as 128 MB, got %+v", info)
	}
}

func TestLegacyApprovalKeepsNonDefaultMemory(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.InstallDir(built["tcp"], true); err != nil {
		t.Fatal(err)
	}
	first.Close()
	// The installed manifest asks for 256 MB; the state predates approvedMemoryMB.
	installed := filepath.Join(dir, "tcp", ManifestFile)
	manifest, _ := os.ReadFile(installed)
	if err := os.WriteFile(installed, append(manifest, []byte("\nlimits: { memoryMB: 256 }\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	legacy := `{"plugins":{"tcp":{"enabled":true,"approved":{"network":["*:*"]}}}}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	info, err := m.Get("tcp")
	if err != nil {
		t.Fatal(err)
	}
	if info.Limits.MemoryMB != 256 || !info.Approved || !info.Ready {
		t.Fatalf("a legacy approval must stay valid for the installed manifest, got %+v", info)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "state.json")); !strings.Contains(string(data), `"approvedMemoryMB": 256`) {
		t.Fatalf("migrated state not saved: %s", data)
	}

	// A later version asking for more memory needs approval again.
	bigger := t.TempDir()
	module, _ := os.ReadFile(filepath.Join(built["tcp"], "tcp.wasm"))
	src, _ := os.ReadFile(filepath.Join(built["tcp"], ManifestFile))
	if err := os.WriteFile(filepath.Join(bigger, ManifestFile), append(src, []byte("\nlimits: { memoryMB: 512 }\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bigger, "tcp.wasm"), module, 0o644); err != nil {
		t.Fatal(err)
	}
	if info, err = m.InstallDir(bigger, false); err != nil || info.Approved {
		t.Fatalf("512 MB must need approval, got %+v, %v", info, err)
	}
}

func TestBrokenPluginPlaceholderKeepsDefaultMemory(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bad"), 0o755); err != nil {
		t.Fatal(err)
	}
	m, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	if info, _ := m.Get("bad"); info.Error == "" || info.Limits.MemoryMB != DefaultMemoryMBWasm {
		t.Fatalf("broken placeholder = %+v", info)
	}
}

// installAlloc installs the alloc test plugin with extra manifest limits.
func installAlloc(t *testing.T, m *Manager, limits string) {
	t.Helper()
	dir := t.TempDir()
	manifest, _ := os.ReadFile(filepath.Join(built["alloc"], ManifestFile))
	module, _ := os.ReadFile(filepath.Join(built["alloc"], "alloc.wasm"))
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), append(manifest, []byte("\nlimits: "+limits+"\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alloc.wasm"), module, 0o644); err != nil {
		t.Fatal(err)
	}
	if info, err := m.InstallDir(dir, true); err != nil || !info.Ready {
		t.Fatalf("install alloc = %+v, %v", info, err)
	}
}

func allocReq(opts map[string]string) *model.Request {
	return &model.Request{Kind: "plugin:alloc", Name: "a", URL: "alloc://", Options: opts}
}

func TestMemoryLimitEnforced(t *testing.T) {
	small := manager(t)
	installAlloc(t, small, "{memoryMB: 64}")
	_, _, err := small.Execute(context.Background(), "alloc", allocReq(map[string]string{"mb": "100"}))
	if err == nil || !strings.Contains(err.Error(), "plugin crashed") {
		t.Fatalf("allocating 100 MB under a 64 MB limit: want a crash, got %v", err)
	}
	// The plugin still works after the trap (the broken instance is discarded).
	if _, _, err := small.Execute(context.Background(), "alloc", allocReq(map[string]string{"mb": "1"})); err != nil {
		t.Fatalf("small allocation after a trap: %v", err)
	}

	big := manager(t)
	installAlloc(t, big, "{memoryMB: 256}")
	if _, _, err := big.Execute(context.Background(), "alloc", allocReq(map[string]string{"mb": "100"})); err != nil {
		t.Fatalf("allocating 100 MB under a 256 MB limit: %v", err)
	}
}

func TestMaxInstancesQueues(t *testing.T) {
	m := manager(t)
	installAlloc(t, m, "{maxInstances: 2}")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, errs[i] = m.Execute(ctx, "alloc", allocReq(map[string]string{"sleepMs": "300"}))
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d: %v", i, err)
		}
	}
	if d := time.Since(start); d < 600*time.Millisecond {
		t.Fatalf("3 calls of 300 ms on 2 instances took %v, want >= 600ms", d)
	}
}

func TestMaxInstancesBusy(t *testing.T) {
	m := manager(t)
	installAlloc(t, m, "{maxInstances: 1}")
	// Compile before timing anything.
	if err := m.Check(context.Background(), KindProtocol, "alloc"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := m.Execute(context.Background(), "alloc", allocReq(map[string]string{"sleepMs": "1000"}))
		done <- err
	}()
	time.Sleep(100 * time.Millisecond) // let call A take the only slot
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, _, err := m.Execute(ctx, "alloc", allocReq(nil))
	if !errors.Is(err, ErrBusy) || err.Error() != "plugin alloc is busy: all 1 instances in use" {
		t.Fatalf("second call = %v, want the busy error", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("call A: %v", err)
	}
	// The slot is free again.
	if _, _, err := m.Execute(context.Background(), "alloc", allocReq(nil)); err != nil {
		t.Fatalf("after A finished: %v", err)
	}
}

func TestBusyPluginTimesOut(t *testing.T) {
	m := manager(t)
	installAlloc(t, m, "{maxInstances: 1}")
	if err := m.Check(context.Background(), KindProtocol, "alloc"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := m.Execute(ctx, "alloc", allocReq(map[string]string{"sleepMs": "10000"}))
	if err == nil || err.Error() != "plugin timed out" {
		t.Fatalf("10 s call with a 500 ms deadline = %v, want plugin timed out", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("timed-out call took %v, want well under 2s", d)
	}
	// The trapped instance released the only slot.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if _, _, err := m.Execute(ctx2, "alloc", allocReq(nil)); err != nil {
		t.Fatalf("call after the timeout: %v", err)
	}
}
