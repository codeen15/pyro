package pyro

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestDispatch(t *testing.T) {
	RegisterProtocol("echo", ProtocolFunc(func(ctx *Context, r *Request) (*Response, error) {
		if r.Body == "fail" {
			return nil, errors.New("asked to fail")
		}
		return &Response{Status: 1, Body: []byte(ctx.Config["prefix"] + r.Body + r.Option("suffix", "!"))}, nil
	}))
	RegisterAuth("stamp", AuthFunc(func(ctx *Context, r *Request) error {
		r.SetHeader("X-Stamp", ctx.Params["value"])
		return nil
	}))
	call := func(m message) reply {
		b, _ := json.Marshal(m)
		var r reply
		json.Unmarshal(dispatch(b), &r)
		return r
	}
	if r := call(message{Op: "describe"}); r.ABI != 1 || len(r.Provides) != 2 {
		t.Fatalf("describe = %+v", r)
	}
	if r := call(message{Op: "execute", ID: "echo", Request: &Request{Body: "hi"}, Config: map[string]string{"prefix": ">"}}); string(r.Response.Body) != ">hi!" {
		t.Fatalf("execute = %+v", r)
	}
	if r := call(message{Op: "execute", ID: "echo", Request: &Request{Body: "fail"}}); r.Error != "asked to fail" {
		t.Fatalf("error = %+v", r)
	}
	if r := call(message{Op: "sign", ID: "stamp", Request: &Request{}, Params: map[string]string{"value": "v1"}}); r.Request.Header("x-stamp") != "v1" {
		t.Fatalf("sign = %+v", r)
	}
	if r := call(message{Op: "execute", ID: "nope", Request: &Request{}}); r.Error == "" {
		t.Fatal("unknown protocol must error")
	}
	if r := dispatch([]byte("{bad")); !json.Valid(r) {
		t.Fatal("invalid input must still produce JSON")
	}
}

func TestHandleOp(t *testing.T) {
	var seen string
	HandleOp("load", func(raw json.RawMessage) any {
		seen = string(raw)
		return map[string]string{"ok": "1"}
	})
	HandleOp("boom", func(raw json.RawMessage) any { panic("kaboom") })
	out := dispatch([]byte(`{"op":"load","x":1}`))
	if string(out) != `{"ok":"1"}` {
		t.Fatalf("load reply = %s", out)
	}
	if seen != `{"op":"load","x":1}` {
		t.Fatalf("handler saw %s", seen)
	}
	var r reply
	json.Unmarshal(dispatch([]byte(`{"op":"boom"}`)), &r)
	if r.Error == "" {
		t.Fatalf("panic reply = %+v", r)
	}
	json.Unmarshal(dispatch([]byte(`{"op":"nope"}`)), &r)
	if r.Error != `unknown operation "nope"` {
		t.Fatalf("unknown reply = %+v", r)
	}
}
