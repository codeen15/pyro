package plugin

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pyrohq/pyro/pkg/engine/model"
)

// built holds directories with each example plugin's manifest and module,
// compiled once for the whole test run.
var built = map[string]string{}

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "pyro-plugins-*")
	if err != nil {
		panic(err)
	}
	code := 0
	defer func() { os.RemoveAll(tmp); os.Exit(code) }()
	for _, name := range []string{"tcp", "hmac"} {
		src, _ := filepath.Abs(filepath.Join("..", "..", "..", "plugins", "examples", name))
		dst := filepath.Join(tmp, name)
		os.MkdirAll(dst, 0o755)
		cmd := exec.Command("go", "build", "-buildmode=c-shared", "-ldflags=-s -w", "-o", filepath.Join(dst, name+".wasm"), ".")
		cmd.Dir = src
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "building %s plugin: %v\n%s", name, err, out)
			code = 1
			return
		}
		manifest, _ := os.ReadFile(filepath.Join(src, ManifestFile))
		os.WriteFile(filepath.Join(dst, ManifestFile), manifest, 0o644)
		built[name] = dst
	}
	code = m.Run()
}

func manager(t *testing.T) *Manager {
	t.Helper()
	m, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m
}

// echoServer replies "echo:<line>\n" per line, or closes after "bye".
func echoServer(t *testing.T) string {
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
					if strings.TrimSpace(line) == "bye" {
						c.Write([]byte("goodbye"))
						return
					}
					if strings.TrimSpace(line) == "slow" {
						time.Sleep(2 * time.Second)
					}
					fmt.Fprintf(c, "echo:%s", line)
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func tcpReq(addr, body string, opts map[string]string) *model.Request {
	return &model.Request{Kind: "plugin:tcp", Name: "t", URL: "tcp://" + addr, Body: &model.Body{Type: model.BodyText, Content: body}, Options: opts}
}

func TestManifestValidation(t *testing.T) {
	_, err := ParseManifest([]byte("name: Bad Name\nversion: 1\nabi: 2\nmodule: ../x.wasm\nprovides: [{kind: reporter, id: X!}]\ncapabilities: {network: [nope]}\n"))
	for _, want := range []string{"name must", "version must", "abi 2", "module must", "kind must be protocol or auth", "invalid", "must be host:port"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
	c := Capabilities{Network: []string{"*.example.com:443", "localhost:*", "10.0.0.5:7000"}}
	for addr, want := range map[string]bool{
		"api.example.com:443": true, "example.com:443": false, "api.example.com:80": false,
		"localhost:9": true, "10.0.0.5:7000": true, "10.0.0.5:7001": false, "nope": false,
	} {
		if got := c.Allows(addr); got != want {
			t.Errorf("Allows(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestTCPPluginNeedsApprovalThenWorks(t *testing.T) {
	m := manager(t)
	addr := echoServer(t)
	info, err := m.InstallDir(built["tcp"], false)
	if err != nil {
		t.Fatal(err)
	}
	if info.Approved || info.Ready || !info.Enabled {
		t.Fatalf("fresh install = %+v", info)
	}
	ctx := context.Background()
	if _, _, err := m.Execute(ctx, "tcp", tcpReq(addr, "hi\n", nil)); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("unapproved execute: %v", err)
	}
	if info, _ = m.Approve("tcp"); !info.Ready {
		t.Fatalf("after approve = %+v", info)
	}

	res, _, err := m.Execute(ctx, "tcp", tcpReq(addr, "hello\n", nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Body) != "echo:hello\n" || res.Proto != "TCP" || res.Timings.Total <= 0 || res.StatusText != "OK" {
		t.Fatalf("response = %+v body=%q", res, res.Body)
	}
	// Hex payload, read until close.
	res, _, err = m.Execute(ctx, "tcp", tcpReq(addr, "62 79 65 0a", map[string]string{"encoding": "hex", "readUntil": "close"}))
	if err != nil || string(res.Body) != "goodbye" {
		t.Fatalf("hex/close = %q %v", res.Body, err)
	}
	// Read timeout surfaces as an error.
	_, _, err = m.Execute(ctx, "tcp", tcpReq(addr, "slow\n", map[string]string{"timeoutMs": "300"}))
	if err == nil || !strings.Contains(err.Error(), "no complete reply") {
		t.Fatalf("timeout err = %v", err)
	}
	// Connection refused is a clean error, and the instance stays usable.
	_, _, err = m.Execute(ctx, "tcp", tcpReq("127.0.0.1:1", "x", nil))
	if err == nil || !strings.Contains(err.Error(), "could not connect") {
		t.Fatalf("refused err = %v", err)
	}
	if res, _, err = m.Execute(ctx, "tcp", tcpReq(addr, "again\n", nil)); err != nil || string(res.Body) != "echo:again\n" {
		t.Fatalf("after error = %q %v", res.Body, err)
	}
}

func TestCapabilitiesEnforced(t *testing.T) {
	m := manager(t)
	addr := echoServer(t)
	dir := t.TempDir()
	manifest, _ := os.ReadFile(filepath.Join(built["tcp"], ManifestFile))
	module, _ := os.ReadFile(filepath.Join(built["tcp"], "tcp.wasm"))
	restricted := strings.Replace(string(manifest), `network: ["*:*"]`, `network: ["example.com:7"]`, 1)
	os.WriteFile(filepath.Join(dir, ManifestFile), []byte(restricted), 0o644)
	os.WriteFile(filepath.Join(dir, "tcp.wasm"), module, 0o644)
	if _, err := m.InstallDir(dir, true); err != nil {
		t.Fatal(err)
	}
	_, _, err := m.Execute(context.Background(), "tcp", tcpReq(addr, "hi\n", nil))
	if err == nil || !strings.Contains(err.Error(), "may not connect to "+addr) {
		t.Fatalf("expected capability denial, got %v", err)
	}

	// Upgrading with wider capabilities needs approval again…
	if info, _ := m.InstallDir(built["tcp"], false); info.Approved {
		t.Fatal("changed capabilities must need approval")
	}
	m.Approve("tcp")
	// …but reinstalling the same capabilities keeps the approval.
	if info, _ := m.InstallDir(built["tcp"], false); !info.Approved {
		t.Fatal("unchanged capabilities must stay approved")
	}
}

func TestHMACAuthPlugin(t *testing.T) {
	m := manager(t)
	if _, err := m.InstallDir(built["hmac"], true); err != nil {
		t.Fatal(err)
	}
	req := &model.Request{Method: "POST", URL: "https://api.example.com/orders", Body: &model.Body{Content: `{"a":1}`},
		Headers: []model.KeyValue{{Key: "X-Timestamp", Value: "1700000000"}}}
	if _, err := m.Sign(context.Background(), "hmac", req, map[string]string{"secret": "s3cret", "keyId": "k1"}); err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write([]byte("POST\nhttps://api.example.com/orders\n1700000000\n{\"a\":1}"))
	want := "keyId=k1,sig=" + hex.EncodeToString(mac.Sum(nil))
	var got string
	for _, h := range req.Headers {
		if h.Key == "X-Signature" {
			got = h.Value
		}
	}
	if got != want {
		t.Fatalf("signature = %q, want %q", got, want)
	}
	if _, err := m.Sign(context.Background(), "hmac", &model.Request{URL: "x"}, map[string]string{}); err == nil || !strings.Contains(err.Error(), "secret is empty") {
		t.Fatalf("missing secret: %v", err)
	}
}

func TestArchiveInstallAndRemove(t *testing.T) {
	m := manager(t)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range []string{ManifestFile, "hmac.wasm"} {
		data, _ := os.ReadFile(filepath.Join(built["hmac"], f))
		w, _ := zw.Create("hmac-0.1.0/" + f)
		w.Write(data)
	}
	zw.Close()
	// Files may sit inside a folder in the archive. Even a plugin with no
	// capabilities is only usable after an explicit approval.
	info, err := m.InstallArchive(buf.Bytes(), false)
	if err != nil || info.Name != "hmac" || info.Approved {
		t.Fatalf("install = %+v %v", info, err)
	}
	if _, err := m.InstallArchive([]byte("not a zip"), false); err == nil {
		t.Fatal("expected error for a non-zip archive")
	}
	if err := m.Remove("hmac"); err != nil {
		t.Fatal(err)
	}
	if len(m.List()) != 0 {
		t.Fatal("plugin still listed after remove")
	}
	// State survives reopening.
	m.InstallDir(built["tcp"], true)
	m.SetConfig("tcp", map[string]string{"x": "1"})
	m2, err := Open(m.Dir())
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	if l := m2.List(); len(l) != 1 || !l[0].Ready || l[0].Config["x"] != "1" {
		t.Fatalf("reopened = %+v", l)
	}
}

func TestVerifyRejectsMismatchedModules(t *testing.T) {
	m := manager(t)
	dir := t.TempDir()
	manifest, _ := os.ReadFile(filepath.Join(built["tcp"], ManifestFile))
	module, _ := os.ReadFile(filepath.Join(built["tcp"], "tcp.wasm"))
	os.WriteFile(filepath.Join(dir, ManifestFile), []byte(strings.Replace(string(manifest), "id: tcp", "id: udp", 1)), 0o644)
	os.WriteFile(filepath.Join(dir, "tcp.wasm"), module, 0o644)
	if _, err := m.InstallDir(dir, true); err == nil || !strings.Contains(err.Error(), `doesn't register it`) {
		t.Fatalf("mismatch err = %v", err)
	}
	os.WriteFile(filepath.Join(dir, ManifestFile), manifest, 0o644)
	os.WriteFile(filepath.Join(dir, "tcp.wasm"), []byte("not wasm"), 0o644)
	if _, err := m.InstallDir(dir, true); err == nil || !strings.Contains(err.Error(), "not a WebAssembly module") {
		t.Fatalf("non-wasm err = %v", err)
	}
}

func TestConcurrentCallsUseThePool(t *testing.T) {
	m := manager(t)
	addr := echoServer(t)
	m.InstallDir(built["tcp"], true)
	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, _, err := m.Execute(context.Background(), "tcp", tcpReq(addr, fmt.Sprintf("n%d\n", i), nil))
			if err != nil {
				errs <- err
				return
			}
			if string(res.Body) != fmt.Sprintf("echo:n%d\n", i) {
				errs <- fmt.Errorf("crossed wires: %q", res.Body)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestIndex(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range []string{ManifestFile, "tcp.wasm"} {
		data, _ := os.ReadFile(filepath.Join(built["tcp"], f))
		w, _ := zw.Create(f)
		w.Write(data)
	}
	zw.Close()
	archive := buf.Bytes()
	sum := Checksum(archive)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			fmt.Fprintf(w, `{"plugins":[
				{"name":"tcp","version":"0.9.0","url":"tcp-old.zip","sha256":"%s"},
				{"name":"tcp","version":"0.10.0","url":"files/tcp.zip","sha256":"%s","title":"Raw TCP"},
				{"name":"bad","version":"1.0.0","url":"files/tcp.zip","sha256":"%s"}]}`, sum, sum, strings.Repeat("0", 64))
		case "/files/tcp.zip":
			w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	idx, err := FetchIndex(context.Background(), srv.URL+"/index.json")
	if err != nil {
		t.Fatal(err)
	}
	e, ok := idx.Find("tcp", "")
	if !ok || e.Version != "0.10.0" {
		t.Fatalf("latest = %+v", e)
	}
	if got := idx.Search("raw"); len(got) != 1 || got[0].Version != "0.10.0" {
		t.Fatalf("search = %+v", got)
	}
	data, err := idx.Download(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	m := manager(t)
	if info, err := m.InstallArchive(data, false); err != nil || info.Name != "tcp" {
		t.Fatalf("install from index: %+v %v", info, err)
	}
	bad, _ := idx.Find("bad", "")
	if _, err := idx.Download(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("bad checksum: %v", err)
	}
	if _, err := FetchIndex(context.Background(), "file:///etc/passwd"); err == nil {
		t.Fatal("non-http index must be refused")
	}
}
