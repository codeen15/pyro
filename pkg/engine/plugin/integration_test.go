package plugin_test

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pyrohq/pyro/pkg/engine"
	"github.com/pyrohq/pyro/pkg/engine/load"
	"github.com/pyrohq/pyro/pkg/engine/model"
	"github.com/pyrohq/pyro/pkg/engine/plugin"
	"github.com/pyrohq/pyro/pkg/engine/protocol/httpdriver"
	"github.com/pyrohq/pyro/pkg/engine/runner"
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
		Resolve: func(string) (load.Target, error) { return load.Target{Path: "c/greet", Request: req, Chain: chain}, nil },
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

func TestPluginsUnavailable(t *testing.T) {
	e := engine.New(httpdriver.New(httpdriver.Options{}))
	res := e.Send(context.Background(), engine.SendInput{Request: &model.Request{Kind: "plugin:tcp", URL: "tcp://x:1"}})
	if !strings.Contains(res.Error, "need plugins") {
		t.Fatalf("error = %q", res.Error)
	}
}
