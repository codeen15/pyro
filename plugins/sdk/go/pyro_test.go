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
