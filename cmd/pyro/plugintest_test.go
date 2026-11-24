package main

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const echoPluginDir = "../../pkg/engine/plugin/testdata/js/echo"

// TestMain gives the tests their own plugins folder, whose compilation cache
// `pyro plugin test` shares between runs, so the JavaScript runtime compiles
// once rather than once per case (and the real ~/.pyro is never touched).
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "pyro-plugintest-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Setenv("PYRO_PLUGINS_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func lineEcho(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					fmt.Fprintf(c, "echo:%s", line)
				}
			}()
		}
	}()
	return ln.Addr().String()
}

// runPluginTest runs `pyro plugin test` in-process.
func runPluginTest(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := pluginTestCmd()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errb.String(), err
}

func TestPluginTestProtocol(t *testing.T) {
	addr := lineEcho(t)
	out, errOut, err := runPluginTest(t, echoPluginDir, "--protocol", "js-echo", "--url", "tcp://"+addr, "--body", "hi")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	if !strings.Contains(out, "OK") || !strings.Contains(out, "echo:hi") {
		t.Errorf("stdout = %q", out)
	}
	if all := out + errOut; !strings.Contains(all, "dialing "+addr) {
		t.Errorf("missing log line in %q", all)
	}
}

func TestPluginTestProtocolJSON(t *testing.T) {
	addr := lineEcho(t)
	out, _, err := runPluginTest(t, echoPluginDir, "--protocol", "js-echo", "--url", "tcp://"+addr, "--body", "hi", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Response struct {
			Body       string `json:"body"`
			StatusText string `json:"statusText"`
		} `json:"response"`
		Logs []struct{ Level, Message string } `json:"logs"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got.Response.Body != "echo:hi" || got.Response.StatusText != "OK" {
		t.Errorf("response = %+v", got.Response)
	}
	if len(got.Logs) == 0 || !strings.Contains(got.Logs[0].Message, "dialing") {
		t.Errorf("logs = %+v", got.Logs)
	}
}

func TestPluginTestAuth(t *testing.T) {
	out, _, err := runPluginTest(t, echoPluginDir, "--auth", "js-sign", "--url", "https://example.test/x", "--body", "payload")
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write([]byte("payload"))
	want := "X-Sig: " + hex.EncodeToString(mac.Sum(nil))
	if !strings.Contains(out, want) {
		t.Errorf("want %q in %q", want, out)
	}
}

func TestPluginTestProcessor(t *testing.T) {
	out, _, err := runPluginTest(t, echoPluginDir, "--processor", "js-check", "--phase", "post", "--status", "200")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "PASS") || !strings.Contains(out, "status ok") || !strings.Contains(out, "seen = 1") {
		t.Errorf("stdout = %q", out)
	}
}

func TestPluginTestProcessorFailure(t *testing.T) {
	out, _, err := runPluginTest(t, echoPluginDir, "--processor", "js-check", "--status", "500")
	if err == nil {
		t.Fatalf("want an error, got none\n%s", out)
	}
	if !strings.Contains(out, "FAIL") || !strings.Contains(out, "status 500") {
		t.Errorf("stdout = %q", out)
	}
}

func TestPluginTestNeedsAFeature(t *testing.T) {
	_, _, err := runPluginTest(t, echoPluginDir)
	want := "this plugin provides 3 features; pick one with --protocol, --auth or --processor"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

func TestPluginTestUnknownFeature(t *testing.T) {
	_, _, err := runPluginTest(t, echoPluginDir, "--protocol", "nope")
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("err = %v", err)
	}
}

// A directory run installs into a throwaway folder that is gone afterwards,
// while compiled code lands in the real plugins folder's cache, where the
// next run finds it.
func TestPluginTestCacheAndCleanup(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	pd := t.TempDir()
	t.Setenv("PYRO_PLUGINS_DIR", pd)

	for i := 0; i < 2; i++ {
		if _, _, err := runPluginTest(t, echoPluginDir, "--auth", "js-sign", "--body", "x"); err != nil {
			t.Fatal(err)
		}
		if left, _ := os.ReadDir(tmp); len(left) != 0 {
			t.Errorf("run %d left the temporary folder behind: %v", i+1, left)
		}
		if entries, err := os.ReadDir(filepath.Join(pd, "cache")); err != nil || len(entries) == 0 {
			t.Errorf("run %d: the plugins folder's cache is empty: %v %v", i+1, entries, err)
		}
	}
	// Nothing else is written to the real plugins folder.
	if entries, _ := os.ReadDir(pd); len(entries) != 1 || entries[0].Name() != "cache" {
		t.Errorf("plugins folder = %v, want only cache", entries)
	}
}
