package plugin_test

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codeen15/pyro/pkg/engine"
	"github.com/codeen15/pyro/pkg/engine/load"
	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/plugin"
	"github.com/codeen15/pyro/pkg/engine/protocol/httpdriver"
	"github.com/codeen15/pyro/pkg/engine/runner"
)

func lineEcho(t *testing.T) string {
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
					fmt.Fprintf(c, "OK %s", strings.ToUpper(line))
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func pluginEngine(t *testing.T) *engine.Engine {
	t.Helper()
	m, err := plugin.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	for _, name := range []string{"tcp", "hmac"} {
		if _, err := m.InstallDir(plugin.BuiltExample(name), true); err != nil {
			t.Fatal(err)
		}
	}
	e := engine.New(httpdriver.New(httpdriver.Options{}))
	e.Plugins = m
	return e
}

// The Phase 4 exit criterion: a third-party protocol plugin works in the
// manual send pipeline, collection runs and load tests.
func TestTCPPluginEverywhere(t *testing.T) {
	addr := lineEcho(t)
	e := pluginEngine(t)
	chain := []*model.Collection{{Variables: []model.Variable{{Key: "addr", Value: addr}, {Key: "who", Value: "pyro"}}}}
	req := &model.Request{
		Kind: "plugin:tcp", Name: "Greet", URL: "tcp://{{addr}}",
		Body:       &model.Body{Type: model.BodyText, Content: "hello {{who}}\n"},
		Options:    map[string]string{"timeoutMs": "{{timeout}}"},
		Assertions: []model.Assertion{{Type: "body", Op: "contains", Value: "HELLO PYRO"}},
		Extract:    []model.Extractor{{Var: "reply", From: "regex", Path: `OK (\w+)`}},
		Scripts:    &model.Scripts{Post: `pm.test("text", () => pm.expect(pm.response.text()).to.match(/^OK/));`},
	}

	// Manual send.
	v := engine.NewVars(nil, chain)
	v.Runtime["timeout"] = "2000"
	res := e.Send(context.Background(), engine.SendInput{Request: req, Chain: chain, Vars: v})
	if res.Error != "" || !res.Passed() || len(res.Tests) != 2 {
		t.Fatalf("send: error=%q tests=%+v scriptErrors=%v", res.Error, res.Tests, res.ScriptErrors)
	}
	if string(res.Response.Body) != "OK HELLO PYRO\n" || res.Sent.Options["timeoutMs"] != "2000" || v.Runtime["reply"] != "HELLO" {
		t.Fatalf("body=%q options=%v extracted=%v", res.Response.Body, res.Sent.Options, v.Runtime)
	}

	// Collection run: plugin requests are not skipped.
	sum := runner.Run(context.Background(), e, []runner.Item{{Path: "c/greet", Request: req, Chain: chain}}, runner.Options{Iterations: 3})
	if sum.Requests != 3 || !sum.OK() {
		t.Fatalf("run summary = %+v", sum)
	}

	// Load test.
	plan, err := load.ParsePlan([]byte(`
scenarios:
  tcp:
    executor: per-vu-iterations
    vus: 5
    iterations: 10
    flow: [ { request: c/greet } ]
thresholds:
  checks: ["rate==1"]
  http_req_failed: ["rate==0"]
`), nil)
	if err != nil {
		t.Fatal(err)
	}
	test, err := load.Start(plan, load.Options{
		Plugins: e.Plugins, TickInterval: 100 * time.Millisecond,
		Resolve: func(string) (load.Target, error) {
			return load.Target{Path: "c/greet", Request: req, Chain: chain}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	lr := test.Wait()
	if lr.Status != load.StatusPassed || lr.Metrics["http_reqs"].Value != 50 || len(lr.Requests) != 1 || lr.Requests[0].Method != "TCP" {
		t.Fatalf("load: status=%s reqs=%v rows=%+v reason=%s errors=%+v", lr.Status, lr.Metrics["http_reqs"].Value, lr.Requests, lr.Reason, lr.Errors)
	}
}

func TestHMACPluginAuthOnHTTP(t *testing.T) {
	var gotSig, gotTS string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig, gotTS = r.Header.Get("X-Signature"), r.Header.Get("X-Timestamp")
	}))
	defer ts.Close()
	e := pluginEngine(t)
	chain := []*model.Collection{{Auth: &model.Auth{Type: "plugin:hmac", Params: map[string]string{"secret": "{{secret}}"}}}}
	v := engine.NewVars(&model.Environment{Variables: []model.Variable{{Key: "secret", Value: "k3y"}}}, chain)
	res := e.Send(context.Background(), engine.SendInput{Request: &model.Request{Method: "GET", URL: ts.URL + "/x"}, Chain: chain, Vars: v})
	if res.Error != "" {
		t.Fatal(res.Error)
	}
	mac := hmac.New(sha256.New, []byte("k3y"))
	mac.Write([]byte("GET\n" + ts.URL + "/x\n" + gotTS + "\n"))
	if gotSig == "" || gotSig != hex.EncodeToString(mac.Sum(nil)) {
		t.Fatalf("signature %q (timestamp %q) doesn't verify", gotSig, gotTS)
	}
}

// A processor plugin before the request (sign a value into a variable) and
// after it (verify the response's signature header).
func TestHMACProcessorPreAndPost(t *testing.T) {
	sign := func(msg string) string {
		mac := hmac.New(sha256.New, []byte("k3y"))
		mac.Write([]byte(msg))
		return hex.EncodeToString(mac.Sum(nil))
	}
	var got string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Sig")
		body := `{"ok":true}`
		sig := sign(body)
		if r.URL.Query().Get("tamper") != "" {
			sig = "bad"
		}
		w.Header().Set("X-Signature", sig)
		fmt.Fprint(w, body)
	}))
	defer ts.Close()
	e := pluginEngine(t)
	proc := model.Processor{Type: "plugin:hmac-var", Options: map[string]string{"secret": "{{secret}}", "message": "order-{{id}}", "var": "sig"}}
	req := &model.Request{Method: "GET", URL: ts.URL, Headers: []model.KeyValue{{Key: "X-Sig", Value: "{{sig}}"}}, Pre: []model.Processor{proc}, Post: []model.Processor{proc}}
	v := engine.NewVars(&model.Environment{Variables: []model.Variable{{Key: "secret", Value: "k3y"}, {Key: "id", Value: "7"}}}, nil)
	res := e.Send(context.Background(), engine.SendInput{Request: req, Vars: v})
	if res.Error != "" || len(res.ScriptErrors) > 0 {
		t.Fatal(res.Error, res.ScriptErrors)
	}
	if got != sign("order-7") {
		t.Fatalf("pre-processor variable: header %q", got)
	}
	if len(res.Tests) != 1 || !res.Tests[0].Passed || res.Tests[0].Source != "plugin" {
		t.Fatalf("post-processor test: %+v", res.Tests)
	}
	req.URL = ts.URL + "/?tamper=1"
	res = e.Send(context.Background(), engine.SendInput{Request: req, Vars: v})
	if len(res.Tests) != 1 || res.Tests[0].Passed {
		t.Fatalf("tampered signature should fail: %+v", res.Tests)
	}
}

func TestPluginsUnavailable(t *testing.T) {
	e := engine.New(httpdriver.New(httpdriver.Options{}))
	res := e.Send(context.Background(), engine.SendInput{Request: &model.Request{Kind: "plugin:tcp", URL: "tcp://x:1"}})
	if !strings.Contains(res.Error, "need plugins") {
		t.Fatalf("error = %q", res.Error)
	}
}

// fakeRedis speaks just enough RESP: PING, SET, GET, INCR, SELECT, LRANGE
// (always x, y) and an error for anything else. Each SELECT's argument is
// sent on selected.
func fakeRedis(t *testing.T, selected chan<- string) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var mu sync.Mutex
	store := map[string]string{}
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
					head, err := r.ReadString('\n')
					if err != nil || !strings.HasPrefix(head, "*") {
						return
					}
					n, _ := strconv.Atoi(strings.TrimSpace(head[1:]))
					args := make([]string, 0, n)
					for i := 0; i < n; i++ {
						lenLine, err := r.ReadString('\n')
						if err != nil {
							return
						}
						size, _ := strconv.Atoi(strings.TrimSpace(lenLine[1:]))
						buf := make([]byte, size+2)
						if _, err := io.ReadFull(r, buf); err != nil {
							return
						}
						args = append(args, string(buf[:size]))
					}
					switch strings.ToUpper(args[0]) {
					case "PING":
						fmt.Fprint(c, "+PONG\r\n")
					case "SELECT":
						selected <- args[1]
						fmt.Fprint(c, "+OK\r\n")
					case "SET":
						mu.Lock()
						store[args[1]] = args[2]
						mu.Unlock()
						fmt.Fprint(c, "+OK\r\n")
					case "GET":
						mu.Lock()
						v, ok := store[args[1]]
						mu.Unlock()
						if ok {
							// Split the reply over two writes: the plugin must keep reading.
							fmt.Fprintf(c, "$%d\r\n", len(v))
							time.Sleep(20 * time.Millisecond)
							fmt.Fprintf(c, "%s\r\n", v)
						} else {
							fmt.Fprint(c, "$-1\r\n")
						}
					case "INCR":
						fmt.Fprint(c, ":42\r\n")
					case "LRANGE":
						fmt.Fprint(c, "*2\r\n$1\r\nx\r\n$1\r\ny\r\n")
					default:
						fmt.Fprintf(c, "-ERR unknown command '%s'\r\n", args[0])
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

// copyRedisJS copies plugins/examples/redis-js into a temp dir, narrowing its
// network capability to loopback (the example ships "*:6379").
func copyRedisJS(t *testing.T) string {
	t.Helper()
	src := filepath.Join("..", "..", "..", "plugins", "examples", "redis-js")
	dir := t.TempDir()
	manifest, err := os.ReadFile(filepath.Join(src, plugin.ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.Replace(string(manifest), `"*:6379"`, `"127.0.0.1:*"`, 1)
	if patched == string(manifest) {
		t.Fatal(`manifest has no "*:6379" network entry to patch`)
	}
	script, err := os.ReadFile(filepath.Join(src, "plugin.js"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, plugin.ManifestFile), []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.js"), script, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRedisJSExample(t *testing.T) {
	selected := make(chan string, 4)
	addr := fakeRedis(t, selected)
	m, err := plugin.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	if _, err := m.InstallDir(copyRedisJS(t), true); err != nil {
		t.Fatal(err)
	}
	e := engine.New(httpdriver.New(httpdriver.Options{}))
	e.Plugins = m

	send := func(cmd string, opts map[string]string) *engine.Result {
		t.Helper()
		res := e.Send(context.Background(), engine.SendInput{
			Request: &model.Request{Kind: "plugin:redis", Name: "r", URL: "redis://" + addr,
				Body: &model.Body{Type: model.BodyText, Content: cmd}, Options: opts},
			Vars: engine.NewVars(nil, nil),
		})
		if res.Error != "" {
			t.Fatalf("%s: %s", cmd, res.Error)
		}
		return res
	}
	for _, c := range []struct{ cmd, body string }{
		{"PING", "PONG"},
		{"SET a 1", "OK"},
		{"GET a", "1"},
		{`SET greeting "hello big world"`, "OK"},
		{"GET greeting", "hello big world"},
		{"GET missing", ""},
		{"INCR n", "42"},
		{"LRANGE l 0 -1", `["x","y"]`},
	} {
		res := send(c.cmd, nil)
		if res.Response.Status != 0 || string(res.Response.Body) != c.body {
			t.Errorf("%s: status=%d body=%q, want %q", c.cmd, res.Response.Status, res.Response.Body, c.body)
		}
	}
	if res := send("GET missing", nil); res.Response.StatusText != "nil" {
		t.Errorf("null bulk statusText = %q, want nil", res.Response.StatusText)
	}

	res := send("FLUSHALL", nil)
	if res.Response.Status != 1 || res.Response.StatusText != "ERR" || string(res.Response.Body) != "ERR unknown command 'FLUSHALL'" {
		t.Errorf("error reply: status=%d text=%q body=%q", res.Response.Status, res.Response.StatusText, res.Response.Body)
	}

	// db sends SELECT first, once per request; 0 and empty don't.
	if res := send("PING", map[string]string{"db": "3"}); string(res.Response.Body) != "PONG" {
		t.Errorf("PING after SELECT: body=%q", res.Response.Body)
	}
	if got := <-selected; got != "3" {
		t.Errorf("SELECT %q, want 3", got)
	}
	send("PING", map[string]string{"db": "0"})
	select {
	case got := <-selected:
		t.Errorf("db 0 sent SELECT %s", got)
	default:
	}
}
