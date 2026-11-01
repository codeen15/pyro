package server

import (
	"archive/zip"
	"bufio"
	"bytes"
	b64 "encoding/base64"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pyrohq/pyro/internal/store"
	"github.com/pyrohq/pyro/pkg/engine"
	"github.com/pyrohq/pyro/pkg/engine/model"
	"github.com/pyrohq/pyro/pkg/engine/plugin"
	"github.com/pyrohq/pyro/pkg/engine/protocol/httpdriver"
)

var (
	tcpArchiveOnce sync.Once
	tcpArchive     []byte
	tcpArchiveErr  error
)

// buildTCPArchive compiles the example TCP plugin once and zips it.
func buildTCPArchive(t *testing.T) []byte {
	tcpArchiveOnce.Do(func() {
		src, _ := filepath.Abs("../../plugins/examples/tcp")
		out := filepath.Join(os.TempDir(), fmt.Sprintf("pyro-test-tcp-%d.wasm", os.Getpid()))
		defer os.Remove(out)
		cmd := exec.Command("go", "build", "-buildmode=c-shared", "-ldflags=-s -w", "-o", out, ".")
		cmd.Dir = src
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
		if b, err := cmd.CombinedOutput(); err != nil {
			tcpArchiveErr = fmt.Errorf("%v: %s", err, b)
			return
		}
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for name, path := range map[string]string{plugin.ManifestFile: filepath.Join(src, plugin.ManifestFile), "tcp.wasm": out} {
			data, _ := os.ReadFile(path)
			w, _ := zw.Create(name)
			w.Write(data)
		}
		zw.Close()
		tcpArchive = buf.Bytes()
	})
	if tcpArchiveErr != nil {
		t.Fatal(tcpArchiveErr)
	}
	return tcpArchive
}

func TestPluginAPI(t *testing.T) {
	archive := buildTCPArchive(t)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				line, _ := bufio.NewReader(c).ReadString('\n')
				fmt.Fprintf(c, "got %s", line)
			}()
		}
	}()

	ws, _ := store.Init(t.TempDir(), "test")
	ws.CreateCollection("demo", model.Collection{Name: "Demo"})
	pm, err := plugin.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer pm.Close()
	eng := engine.New(httpdriver.New(httpdriver.Options{}))
	eng.Plugins = pm
	ts := httptest.NewServer(New(Config{Workspace: ws, Engine: eng, Token: testToken}).Handler())
	defer ts.Close()

	res, body := do(t, ts, "POST", "/api/v1/plugins", string(archive), true)
	if res.StatusCode != 201 || !strings.Contains(body, `"approved":false`) {
		t.Fatalf("install: %d %s", res.StatusCode, body)
	}
	send := func() string {
		_, b := do(t, ts, "POST", "/api/v1/send", `{"request":{"kind":"plugin:tcp","name":"t","url":"tcp://`+ln.Addr().String()+`","body":{"type":"text","content":"hello\n"}}}`, true)
		return b
	}
	if b := send(); !strings.Contains(b, "needs approval") {
		t.Fatalf("unapproved send: %s", b)
	}
	if res, body := do(t, ts, "POST", "/api/v1/plugins/tcp/approve", "", true); res.StatusCode != 200 || !strings.Contains(body, `"ready":true`) {
		t.Fatalf("approve: %d %s", res.StatusCode, body)
	}
	if b := send(); !strings.Contains(b, b64.StdEncoding.EncodeToString([]byte("got hello\n"))) {
		t.Fatalf("approved send: %s", b)
	}
	do(t, ts, "POST", "/api/v1/plugins/tcp/enabled", `{"enabled":false}`, true)
	if b := send(); !strings.Contains(b, "disabled") {
		t.Fatalf("disabled send: %s", b)
	}
	_, body = do(t, ts, "GET", "/api/v1/plugins", "", true)
	if !strings.Contains(body, `"name":"tcp"`) || !strings.Contains(body, `"urlPlaceholder"`) {
		t.Fatalf("list: %s", body)
	}
	if res, _ := do(t, ts, "DELETE", "/api/v1/plugins/tcp", "", true); res.StatusCode != 204 {
		t.Fatalf("remove: %d", res.StatusCode)
	}
	if res, _ := do(t, ts, "POST", "/api/v1/plugins", "junk", true); res.StatusCode != 422 {
		t.Fatalf("junk archive: %d", res.StatusCode)
	}
	if res, _ := do(t, ts, "GET", "/api/v1/plugins/index", "", true); res.StatusCode != 404 {
		t.Fatalf("index without config: %d", res.StatusCode)
	}
}
