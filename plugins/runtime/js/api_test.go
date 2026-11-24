package main

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	pyro "github.com/codeen15/pyro/plugins/sdk/go"
	"github.com/dop251/goja"
)

// captured records registrations instead of handing them to the SDK.
type captured struct {
	protocols  map[string]pyro.ProtocolFunc
	auths      map[string]pyro.AuthFunc
	processors map[string]pyro.ProcessorFunc
}

func (c *captured) protocol(id string, f pyro.ProtocolFunc)   { c.protocols[id] = f }
func (c *captured) auth(id string, f pyro.AuthFunc)           { c.auths[id] = f }
func (c *captured) processor(id string, f pyro.ProcessorFunc) { c.processors[id] = f }

func newTestRuntime(t *testing.T) (*jsRuntime, *captured) {
	t.Helper()
	c := &captured{map[string]pyro.ProtocolFunc{}, map[string]pyro.AuthFunc{}, map[string]pyro.ProcessorFunc{}}
	rt, err := newRuntime(c)
	if err != nil {
		t.Fatal(err)
	}
	return rt, c
}

func eval(t *testing.T, rt *jsRuntime, src string) goja.Value {
	t.Helper()
	v, err := rt.vm.RunString(src)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	return v
}

func TestEncoding(t *testing.T) {
	rt, _ := newTestRuntime(t)
	if got := eval(t, rt, `pyro.encoding.hex.encode(pyro.encoding.utf8.encode("hi"))`).String(); got != "6869" {
		t.Fatalf("hex = %q", got)
	}
	if got := eval(t, rt, `pyro.encoding.utf8.decode(pyro.encoding.hex.decode("6869"))`).String(); got != "hi" {
		t.Fatalf("utf8 decode = %q", got)
	}
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	want := base64.StdEncoding.EncodeToString(all)
	got := eval(t, rt, `
		const all = new Uint8Array(256); for (let i = 0; i < 256; i++) all[i] = i;
		pyro.encoding.base64.encode(all)`).String()
	if got != want {
		t.Fatalf("base64 = %q, want %q", got, want)
	}
	back := eval(t, rt, `pyro.encoding.base64.decode(`+"`"+want+"`"+`)`).Export()
	if !reflect.DeepEqual(back, all) {
		t.Fatalf("base64 decode = %v", back)
	}
	if got := eval(t, rt, `pyro.encoding.base64.encode("hi")`).String(); got != "aGk=" {
		t.Fatalf("base64 of string = %q", got)
	}
	if _, err := rt.vm.RunString(`pyro.encoding.hex.decode("zz")`); err == nil {
		t.Fatal("bad hex should throw")
	}
}

func TestCrypto(t *testing.T) {
	rt, _ := newTestRuntime(t)
	got := eval(t, rt, `pyro.encoding.hex.encode(pyro.crypto.hmacSHA256("key", "The quick brown fox jumps over the lazy dog"))`).String()
	if got != "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8" {
		t.Fatalf("hmac = %s", got)
	}
	got = eval(t, rt, `pyro.encoding.hex.encode(pyro.crypto.sha256("abc"))`).String()
	if got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("sha256 = %s", got)
	}
	if !eval(t, rt, `pyro.crypto.sha256("") instanceof Uint8Array`).ToBoolean() {
		t.Fatal("sha256 should return a Uint8Array")
	}
}

func TestRequestHelpers(t *testing.T) {
	rt, _ := newTestRuntime(t)
	req := &pyro.Request{
		Kind: "plugin:x", Name: "r", Method: "GET", URL: "http://a",
		Headers: []pyro.KeyValue{{Key: "X-Sig", Value: "old"}, {Key: "Off", Value: "v", Disabled: true}},
		Options: map[string]string{"k": "v", "empty": ""},
	}
	obj := rt.requestToJS(req)
	rt.vm.Set("req", obj)
	checks := map[string]string{
		`req.option("k", "d")`:             "v",
		`req.option("missing", "d")`:       "d",
		`req.option("empty", "d")`:         "d",
		`req.option("missing")`:            "",
		`req.header("x-sig")`:              "old",
		`req.header("off")`:                "",
		`req.method + " " + req.url`:       "GET http://a",
		`req.query.length + ""`:            "0",
		`req.headers[0].key`:               "X-Sig",
		`JSON.stringify(Object.keys(req))`: `["kind","name","method","url","headers","query","body","options"]`,
	}
	for src, want := range checks {
		if got := eval(t, rt, src).String(); got != want {
			t.Errorf("%s = %q, want %q", src, got, want)
		}
	}
	eval(t, rt, `req.setHeader("x-sig", "new"); req.setHeader("Off", "on"); req.setHeader("Added", 1);
		req.url = "http://b"; req.body = "payload"; req.query.push({key: "q", value: "1"})`)
	back, err := rt.requestFromJS(obj)
	if err != nil {
		t.Fatal(err)
	}
	want := &pyro.Request{
		Kind: "plugin:x", Name: "r", Method: "GET", URL: "http://b", Body: "payload",
		Headers: []pyro.KeyValue{{Key: "X-Sig", Value: "new"}, {Key: "Off", Value: "on"}, {Key: "Added", Value: "1"}},
		Query:   []pyro.KeyValue{{Key: "q", Value: "1"}},
		Options: map[string]string{"k": "v", "empty": ""},
	}
	if !reflect.DeepEqual(back, want) {
		t.Fatalf("request back = %+v\nwant %+v", back, want)
	}
}

func TestResponseFromJS(t *testing.T) {
	rt, _ := newTestRuntime(t)
	obj := eval(t, rt, `({status: 200, statusText: "OK", headers: {"A": "1", "B": ["2", "3"]}, body: "hi", remoteAddr: "1.2.3.4:5"})`)
	res, err := rt.responseFromJS(obj)
	if err != nil {
		t.Fatal(err)
	}
	want := &pyro.Response{Status: 200, StatusText: "OK", Body: []byte("hi"), RemoteAddr: "1.2.3.4:5",
		Headers: []pyro.KeyValue{{Key: "A", Value: "1"}, {Key: "B", Value: "2"}, {Key: "B", Value: "3"}}}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("object form = %+v", res)
	}
	obj = eval(t, rt, `({status: 1, headers: [{key: "A", value: "1"}, {key: "Z", value: "x", disabled: true}], body: new Uint8Array([0, 255])})`)
	res, err = rt.responseFromJS(obj)
	if err != nil {
		t.Fatal(err)
	}
	want = &pyro.Response{Status: 1, Body: []byte{0, 255},
		Headers: []pyro.KeyValue{{Key: "A", Value: "1"}, {Key: "Z", Value: "x", Disabled: true}}}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("array form = %+v", res)
	}
	if res, err := rt.responseFromJS(goja.Undefined()); err != nil || res.Status != 0 {
		t.Fatalf("undefined = %+v, %v", res, err)
	}
	if _, err := rt.responseFromJS(eval(t, rt, `({body: {a: 1}})`)); err == nil {
		t.Fatal("object body should be an error")
	}
}

func TestHandlers(t *testing.T) {
	rt, c := newTestRuntime(t)
	err := rt.load("plugin.js", `
pyro.registerProtocol("echo", (ctx, req) => ({
  status: 200, statusText: ctx.config.greeting, body: req.method + " " + req.url + " " + ctx.params.p,
  headers: {"X-Name": req.name},
}))
pyro.registerAuth("sig", (ctx, req) => { req.setHeader("X-Sig", pyro.encoding.hex.encode(pyro.crypto.sha256(req.body))) })
pyro.registerProcessor("proc", (ctx, p) => {
  if (p.phase === "pre") { p.request.url += "?x=" + p.vars.base; return }
  p.setVar("status", p.response.status)
  p.test("is 201", p.response.status === 201, "got " + p.response.status)
  p.test("body", JSON.parse(p.response.body).ok === true)
})
pyro.registerProtocol("boom", () => { null.x })
pyro.registerProtocol("async", async () => ({status: 7}))
pyro.registerProtocol("reject", async () => { throw new RangeError("nope") })
pyro.registerAuth("throws", () => { throw "plain" })
`)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &pyro.Context{Config: map[string]string{"greeting": "hello"}, Params: map[string]string{"p": "q"}}

	res, err := c.protocols["echo"](ctx, &pyro.Request{Name: "n", Method: "GET", URL: "u"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 200 || res.StatusText != "hello" || string(res.Body) != "GET u q" || res.Headers[0] != (pyro.KeyValue{Key: "X-Name", Value: "n"}) {
		t.Fatalf("execute = %+v", res)
	}

	req := &pyro.Request{Body: "abc"}
	if err := c.auths["sig"](ctx, req); err != nil {
		t.Fatal(err)
	}
	if req.Header("X-Sig") != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("sign = %+v", req)
	}

	pre := &pyro.Process{Phase: "pre", Request: &pyro.Request{URL: "http://a"}, Vars: map[string]string{"base": "1"}}
	if err := c.processors["proc"](ctx, pre); err != nil {
		t.Fatal(err)
	}
	if pre.Request.URL != "http://a?x=1" {
		t.Fatalf("pre request = %+v", pre.Request)
	}
	post := &pyro.Process{Phase: "post", Request: &pyro.Request{}, Vars: map[string]string{},
		Response: &pyro.ProcessResponse{Status: 201, Body: []byte(`{"ok":true}`)}}
	if err := c.processors["proc"](ctx, post); err != nil {
		t.Fatal(err)
	}
	if post.Vars["status"] != "201" {
		t.Fatalf("vars = %v", post.Vars)
	}

	_, err = c.protocols["boom"](ctx, &pyro.Request{})
	if err == nil || !strings.HasPrefix(err.Error(), "plugin.js:13:") || !strings.Contains(err.Error(), "TypeError: ") {
		t.Fatalf("thrown error = %v", err)
	}
	if res, err := c.protocols["async"](ctx, &pyro.Request{}); err != nil || res.Status != 7 {
		t.Fatalf("async = %+v, %v", res, err)
	}
	if _, err := c.protocols["reject"](ctx, &pyro.Request{}); err == nil || !strings.Contains(err.Error(), "RangeError: nope") {
		t.Fatalf("rejected = %v", err)
	}
	if err := c.auths["throws"](ctx, &pyro.Request{}); err == nil || !strings.HasSuffix(err.Error(), "plain") {
		t.Fatalf("thrown string = %v", err)
	}
}

func TestLoadErrors(t *testing.T) {
	rt, _ := newTestRuntime(t)
	err := rt.load("plugin.js", "const a = 1\nconst b = ;\n")
	if err == nil || !strings.HasPrefix(err.Error(), "plugin.js:2:11 SyntaxError: ") {
		t.Fatalf("syntax error = %v", err)
	}
	rt, _ = newTestRuntime(t)
	err = rt.load("plugin.js", "\n  throw new Error('bad config')\n")
	if err == nil || err.Error() != "plugin.js:2:9 Error: bad config" {
		t.Fatalf("thrown at load = %v", err)
	}
	rt, _ = newTestRuntime(t)
	if err := rt.load("plugin.js", `pyro.registerAuth("", () => {})`); err == nil {
		t.Fatal("empty id should fail")
	}
	rt, _ = newTestRuntime(t)
	if err := rt.load("plugin.js", `pyro.registerAuth("a", 1)`); err == nil {
		t.Fatal("non-function handler should fail")
	}
}

func TestLoadOp(t *testing.T) {
	loaded = false
	t.Cleanup(func() { loaded = false })
	reply := func(msg string) string {
		b, err := json.Marshal(handleLoad(json.RawMessage(msg)))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := reply(`{"op":"load","entry":"main.js","files":{"plugin.js":""}}`); !strings.Contains(got, `"error"`) {
		t.Fatalf("missing entry = %s", got)
	}
	loaded = false
	if got := reply(`{"op":"load","entry":"plugin.js","files":{"plugin.js":"pyro.log.info('hi', 1, {a: 1})"}}`); got != `{}` {
		t.Fatalf("load = %s", got)
	}
	if got := reply(`{"op":"load","entry":"plugin.js","files":{"plugin.js":""}}`); !strings.Contains(got, "already loaded") {
		t.Fatalf("second load = %s", got)
	}
}

func TestDialOutsideWasm(t *testing.T) {
	rt, _ := newTestRuntime(t)
	_, err := rt.vm.RunString(`pyro.dial("localhost:1", {tls: false, timeoutMs: 100})`)
	if err == nil || !strings.Contains(err.Error(), "only available inside Pyro") {
		t.Fatalf("dial = %v", err)
	}
}
