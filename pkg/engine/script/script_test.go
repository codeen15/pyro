package script

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/codeen15/pyro/pkg/engine/check"
	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/protocol"
	"github.com/codeen15/pyro/pkg/engine/vars"
)

func runtime(t *testing.T) *Runtime {
	t.Helper()
	rt, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return rt
}

func response() *check.Response {
	return check.Wrap(&protocol.Response{
		Status: 200, StatusText: "OK",
		Headers: []model.KeyValue{{Key: "Content-Type", Value: "application/json"}},
		Cookies: []protocol.Cookie{{Name: "sid", Value: "abc"}},
		Body:    []byte(`{"token":"t-1","user":{"id":7,"roles":["admin"]}}`),
		Timings: protocol.Timings{Total: 80 * time.Millisecond},
	})
}

func TestPostResponseTests(t *testing.T) {
	v := vars.NewSet()
	v.EnvName = "dev"
	out := runtime(t).Run(context.Background(), Input{
		Phase: PostResponse, Name: "request", Response: response(), Vars: v,
		Code: `
			pm.test("status is 200", function () { pm.response.to.have.status(200); });
			pm.test("is ok", () => { pm.response.to.be.ok; });
			pm.test("has json", () => { pm.response.to.have.jsonBody("user.id", 7); });
			pm.test("chai works", () => { pm.expect(pm.response.json().user.roles).to.include("admin"); });
			pm.test("fails", () => { pm.expect(pm.response.code).to.equal(201); });
			pm.test("header", () => { pm.response.to.have.header("Content-Type", "application/json"); });
			pm.test("fast", () => { pm.response.to.have.responseTime.below(500); });
			pm.test("cookie", () => { pm.expect(pm.cookies.get("sid")).to.eql("abc"); });
			pm.environment.set("token", pm.response.json().token);
			pm.collectionVariables.set("userId", pm.response.json().user.id);
			pm.variables.set("local", "yes");
			tests["legacy"] = responseCode.code === 200;
			console.log("env is", pm.environment.name, {a: 1});
		`,
	})
	if out.Error != "" {
		t.Fatal(out.Error)
	}
	want := map[string]bool{"status is 200": true, "is ok": true, "has json": true, "chai works": true,
		"fails": false, "header": true, "fast": true, "cookie": true, "legacy": true}
	if len(out.Tests) != len(want) {
		t.Fatalf("tests = %+v", out.Tests)
	}
	for _, tr := range out.Tests {
		if tr.Passed != want[tr.Name] {
			t.Errorf("%s: passed=%v (%s)", tr.Name, tr.Passed, tr.Error)
		}
		if tr.Name == "fails" && !strings.Contains(tr.Error, "expected 200 to equal 201") {
			t.Errorf("chai message = %q", tr.Error)
		}
	}
	if v.Environment["token"] != "t-1" || v.Collection["userId"] != "7" || v.Runtime["local"] != "yes" {
		t.Errorf("vars = %+v", v)
	}
	if len(out.Logs) != 1 || out.Logs[0].Message != `env is dev {"a":1}` {
		t.Errorf("logs = %+v", out.Logs)
	}
}

func TestPreRequestModifiesRequest(t *testing.T) {
	v := vars.NewSet()
	v.Environment["base"] = "https://api"
	out := runtime(t).Run(context.Background(), Input{
		Phase: PreRequest, Name: "request", Vars: v,
		Request: &model.Request{Kind: model.KindHTTP, Method: "GET", URL: "{{base}}/x",
			Headers: []model.KeyValue{{Key: "X-Old", Value: "1"}}},
		Code: `
			pm.request.headers.upsert({key: "Authorization", value: "Bearer " + pm.variables.get("base")});
			pm.request.headers.remove("x-old");
			pm.request.method = "post";
			pm.request.url = pm.variables.replaceIn("{{base}}/y");
			pm.request.body.raw = JSON.stringify({ts: 1});
			pm.variables.set("id", require("uuid").v4());
		`,
	})
	if out.Error != "" {
		t.Fatal(out.Error)
	}
	r := out.Request
	if r.Method != "POST" || r.URL != "https://api/y" || len(r.Headers) != 1 || r.Headers[0].Value != "Bearer https://api" ||
		r.Body == nil || r.Body.Content != `{"ts":1}` {
		t.Fatalf("request = %+v body=%+v", r, r.Body)
	}
	if len(v.Runtime["id"]) != 36 {
		t.Fatalf("uuid = %q", v.Runtime["id"])
	}
}

func TestErrorsTimeoutsAndControl(t *testing.T) {
	rt := runtime(t)
	out := rt.Run(context.Background(), Input{Phase: PostResponse, Name: "folder", Code: `undefinedFn();`, Response: response()})
	if !strings.HasPrefix(out.Error, "folder script: ReferenceError: undefinedFn is not defined") {
		t.Errorf("error = %q", out.Error)
	}
	out = rt.Run(context.Background(), Input{Phase: PreRequest, Code: `while(true){}`, Timeout: 50 * time.Millisecond})
	if !strings.Contains(out.Error, "timed out") {
		t.Errorf("timeout error = %q", out.Error)
	}
	// The runtime is reusable after an interrupt.
	out = rt.Run(context.Background(), Input{Phase: PreRequest, Code: `pm.execution.skipRequest(); postman.setNextRequest("Login");`})
	if out.Error != "" || !out.Skip || !out.NextSet || *out.Next != "Login" {
		t.Errorf("control = %+v", out)
	}
	out = rt.Run(context.Background(), Input{Phase: PreRequest, Code: `pm.execution.setNextRequest(null)`})
	if !out.NextSet || out.Next != nil {
		t.Errorf("stop = %+v", out)
	}
	out = rt.Run(context.Background(), Input{Phase: PreRequest, Code: `require("lodash")`})
	if !strings.Contains(out.Error, "require('lodash') is not available") {
		t.Errorf("require = %q", out.Error)
	}
	out = rt.Run(context.Background(), Input{Phase: PreRequest, Code: `syntax error here(`})
	if out.Error == "" {
		t.Error("expected syntax error")
	}
}

func TestSendRequest(t *testing.T) {
	var got *model.Request
	out := runtime(t).Run(context.Background(), Input{
		Phase: PreRequest, Vars: vars.NewSet(),
		Send: func(_ context.Context, r *model.Request) (*protocol.Response, error) {
			got = r
			return &protocol.Response{Status: 201, Body: []byte(`{"access_token":"abc"}`)}, nil
		},
		Code: `
			pm.sendRequest({url: "https://auth/token", method: "POST", header: {"X-A": "1"},
				body: {mode: "raw", raw: '{"grant":"x"}'}}, function (err, res) {
				pm.environment.set("tok", res.json().access_token);
				pm.test("token call", () => pm.expect(res.code).to.equal(201));
			});
		`,
	})
	if out.Error != "" {
		t.Fatal(out.Error)
	}
	if got.Method != "POST" || got.URL != "https://auth/token" || got.Headers[0].Key != "X-A" || got.Body.Type != model.BodyJSON {
		t.Fatalf("sent = %+v", got)
	}
	if len(out.Tests) != 1 || !out.Tests[0].Passed {
		t.Fatalf("tests = %+v", out.Tests)
	}
}

func BenchmarkNewRuntime(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := New(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRunTest(b *testing.B) {
	rt, _ := New()
	in := Input{Phase: PostResponse, Response: response(), Vars: vars.NewSet(),
		Code: `pm.test("ok", () => pm.response.to.have.status(200));`}
	for i := 0; i < b.N; i++ {
		rt.Run(context.Background(), in)
	}
}

// Regression: a late timeout interrupt from one run must never hit the next.
func TestRuntimeReuseNeverInheritsInterrupt(t *testing.T) {
	rt := runtime(t)
	for i := 0; i < 300; i++ {
		if out := rt.Run(context.Background(), Input{Phase: PreRequest, Code: `pm.variables.set("i", "1")`}); out.Error != "" {
			t.Fatalf("run %d: %s", i, out.Error)
		}
	}
}
