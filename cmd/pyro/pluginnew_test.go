package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codeen15/pyro/pkg/engine/plugin"
)

// runPluginNew runs `pyro plugin new` in-process.
func runPluginNew(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newPluginCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// Moving the Go templates into files changed nothing: id "foo" writes exactly
// what testdata/plugin-new holds (captured before the move).
func TestPluginNewGoOutputUnchanged(t *testing.T) {
	for _, kind := range []string{"protocol", "auth", "processor"} {
		t.Run(kind, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "p")
			if _, err := runPluginNew(t, dir, "--kind", kind, "--id", "foo"); err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", "plugin-new", "go-"+kind)
			entries, err := os.ReadDir(golden)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 4 {
				t.Fatalf("golden has %d files", len(entries))
			}
			for _, e := range entries {
				name := strings.TrimSuffix(e.Name(), ".golden")
				want, _ := os.ReadFile(filepath.Join(golden, e.Name()))
				got, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Errorf("%s differs:\n%s", name, got)
				}
			}
			if left, _ := os.ReadDir(dir); len(left) != 4 {
				t.Errorf("wrote %d files, want 4", len(left))
			}
		})
	}
}

func TestPluginNewNextStep(t *testing.T) {
	for _, lang := range []string{"go", "js"} {
		dir := filepath.Join(t.TempDir(), "p")
		out, err := runPluginNew(t, dir, "--lang", lang, "--id", "x")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "pyro plugin dev "+dir) {
			t.Errorf("%s: output = %q", lang, out)
		}
	}
}

func TestPluginNewBadLang(t *testing.T) {
	_, err := runPluginNew(t, filepath.Join(t.TempDir(), "p"), "--lang", "rust")
	if err == nil || !strings.Contains(err.Error(), "--lang must be go or js") {
		t.Errorf("err = %v", err)
	}
}

// newJS scaffolds a JavaScript plugin, checks its files and manifest, and
// installs it the way `pyro plugin dev --once` does.
func newJS(t *testing.T, kind string) (dir, manifest string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "x")
	if _, err := runPluginNew(t, dir, "--lang", "js", "--kind", kind, "--id", "x"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"pyro-plugin.yaml", "plugin.js", "pyro.d.ts", "README.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
	if left, _ := os.ReadDir(dir); len(left) != 4 {
		t.Errorf("wrote %d files, want 4", len(left))
	}
	data, err := os.ReadFile(filepath.Join(dir, plugin.ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	man, err := plugin.ParseManifest(data)
	if err != nil {
		t.Fatalf("manifest: %v\n%s", err, data)
	}
	if !man.IsJS() || man.Module != "plugin.js" || !strings.Contains(string(data), "runtime: js") {
		t.Errorf("manifest = %s", data)
	}
	var out bytes.Buffer
	if err := runDevCycle(dir, devManager(t), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	return dir, string(data)
}

func TestPluginNewJSProtocol(t *testing.T) {
	dir, manifest := newJS(t, "protocol")
	if !strings.Contains(manifest, `network: ["localhost:*", "127.0.0.1:*"]`) {
		t.Errorf("manifest = %s", manifest)
	}
	addr := lineEcho(t)
	out, errOut, err := runPluginTest(t, dir, "--url", "x://"+addr, "--body", "hi\n")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	if !strings.Contains(out, "echo:hi") {
		t.Errorf("stdout = %q", out)
	}
}

func TestPluginNewJSAuth(t *testing.T) {
	dir, manifest := newJS(t, "auth")
	if strings.Contains(manifest, "network") {
		t.Errorf("auth needs no network: %s", manifest)
	}
	out, errOut, err := runPluginTest(t, dir, "--url", "https://example.test/x", "--body", "payload", "--param", "secret=s3cret")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write([]byte("payload"))
	if want := "X-Signature: " + hex.EncodeToString(mac.Sum(nil)); !strings.Contains(out, want) {
		t.Errorf("want %q in %q", want, out)
	}
	if _, _, err := runPluginTest(t, dir, "--url", "https://example.test/x"); err == nil {
		t.Error("an empty secret should fail")
	}
}

func TestPluginNewJSProcessor(t *testing.T) {
	dir, manifest := newJS(t, "processor")
	if strings.Contains(manifest, "network") {
		t.Errorf("a processor needs no network: %s", manifest)
	}
	out, errOut, err := runPluginTest(t, dir, "--phase", "post", "--status", "200")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	if !strings.Contains(out, "PASS") || !strings.Contains(out, "status is below 500") || !strings.Contains(out, "lastStatus = 200") {
		t.Errorf("stdout = %q", out)
	}
	out, _, err = runPluginTest(t, dir, "--phase", "post", "--status", "503", "--param", "var=code")
	if err == nil || !strings.Contains(out, "FAIL") || !strings.Contains(out, "code = 503") {
		t.Errorf("err = %v, stdout = %q", err, out)
	}
}
