package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
