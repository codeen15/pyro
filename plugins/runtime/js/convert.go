package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	pyro "github.com/codeen15/pyro/plugins/sdk/go"
	"github.com/dop251/goja"
)

// Conversions between the SDK's types and JS values. Headers and query
// parameters are arrays of {key, value, disabled?}, as on the wire.

// str is String(v), with undefined and null as "".
func str(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	return v.String()
}

// requestToJS makes a request object: the SDK Request's fields (all present)
// plus option(), header() and setHeader() from its prototype.
func (rt *jsRuntime) requestToJS(req *pyro.Request) goja.Value {
	b, err := json.Marshal(req)
	if err != nil {
		panic(err)
	}
	v, err := rt.mkReq(goja.Undefined(), rt.vm.ToValue(string(b)))
	if err != nil {
		panic(err)
	}
	return v
}

// requestFromJS reads a request object back, after JS may have changed it.
func (rt *jsRuntime) requestFromJS(v goja.Value) (*pyro.Request, error) {
	o, ok := v.(*goja.Object)
	if !ok {
		return nil, errors.New("request must be an object")
	}
	r := &pyro.Request{
		Kind:   str(o.Get("kind")),
		Name:   str(o.Get("name")),
		Method: str(o.Get("method")),
		URL:    str(o.Get("url")),
		Body:   str(o.Get("body")),
	}
	var err error
	if r.Headers, err = kvFromJS(o.Get("headers"), "request headers"); err != nil {
		return nil, err
	}
	if r.Query, err = kvFromJS(o.Get("query"), "request query"); err != nil {
		return nil, err
	}
	if r.Options, err = stringMapFromJS(o.Get("options"), "request options"); err != nil {
		return nil, err
	}
	return r, nil
}

// responseFromJS converts a protocol handler's return value: {status,
// statusText, headers ({name: value} or [{key, value}]), body (string or
// Uint8Array), remoteAddr}. undefined means an empty response.
func (rt *jsRuntime) responseFromJS(v goja.Value) (*pyro.Response, error) {
	res := &pyro.Response{}
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return res, nil
	}
	o, ok := v.(*goja.Object)
	if !ok {
		return nil, fmt.Errorf("handler must return an object, not %s", v.String())
	}
	if s := o.Get("status"); s != nil && !goja.IsUndefined(s) && !goja.IsNull(s) {
		res.Status = int(s.ToInteger())
	}
	res.StatusText = str(o.Get("statusText"))
	res.RemoteAddr = str(o.Get("remoteAddr"))
	var err error
	if res.Headers, err = kvFromJS(o.Get("headers"), "response headers"); err != nil {
		return nil, err
	}
	if res.Body, err = bodyFromJS(o.Get("body")); err != nil {
		return nil, err
	}
	return res, nil
}

func bodyFromJS(v goja.Value) ([]byte, error) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil, nil
	}
	switch x := v.Export().(type) {
	case string:
		return []byte(x), nil
	case []byte:
		return append([]byte(nil), x...), nil
	case goja.ArrayBuffer:
		return append([]byte(nil), x.Bytes()...), nil
	}
	return nil, errors.New("response body must be a string or Uint8Array")
}

// kvFromJS accepts [{key, value, disabled?}] or {name: value | [values]}.
func kvFromJS(v goja.Value, what string) ([]pyro.KeyValue, error) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil, nil
	}
	o, ok := v.(*goja.Object)
	if !ok {
		return nil, fmt.Errorf("%s must be an array or object", what)
	}
	var out []pyro.KeyValue
	if o.ClassName() == "Array" {
		n := int(o.Get("length").ToInteger())
		for i := 0; i < n; i++ {
			e, ok := o.Get(strconv.Itoa(i)).(*goja.Object)
			if !ok {
				return nil, fmt.Errorf("%s[%d] must be {key, value}", what, i)
			}
			kv := pyro.KeyValue{Key: str(e.Get("key")), Value: str(e.Get("value"))}
			if d := e.Get("disabled"); d != nil {
				kv.Disabled = d.ToBoolean()
			}
			out = append(out, kv)
		}
		return out, nil
	}
	for _, k := range o.Keys() {
		val := o.Get(k)
		if a, ok := val.(*goja.Object); ok && a.ClassName() == "Array" {
			n := int(a.Get("length").ToInteger())
			for i := 0; i < n; i++ {
				out = append(out, pyro.KeyValue{Key: k, Value: str(a.Get(strconv.Itoa(i)))})
			}
			continue
		}
		out = append(out, pyro.KeyValue{Key: k, Value: str(val)})
	}
	return out, nil
}

func stringMapFromJS(v goja.Value, what string) (map[string]string, error) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil, nil
	}
	o, ok := v.(*goja.Object)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", what)
	}
	keys := o.Keys()
	if len(keys) == 0 {
		return nil, nil
	}
	m := make(map[string]string, len(keys))
	for _, k := range keys {
		m[k] = str(o.Get(k))
	}
	return m, nil
}

// stringMapToJS makes a plain object with the map's keys in sorted order.
func (rt *jsRuntime) stringMapToJS(m map[string]string) *goja.Object {
	o := rt.vm.NewObject()
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		o.Set(k, m[k])
	}
	return o
}

func (rt *jsRuntime) contextToJS(ctx *pyro.Context) *goja.Object {
	o := rt.vm.NewObject()
	var config, params map[string]string
	if ctx != nil {
		config, params = ctx.Config, ctx.Params
	}
	o.Set("config", rt.stringMapToJS(config))
	o.Set("params", rt.stringMapToJS(params))
	return o
}

func (rt *jsRuntime) kvToJS(kvs []pyro.KeyValue) goja.Value {
	if kvs == nil {
		kvs = []pyro.KeyValue{}
	}
	b, err := json.Marshal(kvs)
	if err != nil {
		panic(err)
	}
	v, err := rt.parse(goja.Undefined(), rt.vm.ToValue(string(b)))
	if err != nil {
		panic(err)
	}
	return v
}

// processResponseToJS gives a post-processor {status, statusText, headers,
// body (string), bodyBytes (Uint8Array), timeMs}; null before the request.
func (rt *jsRuntime) processResponseToJS(r *pyro.ProcessResponse) goja.Value {
	if r == nil {
		return goja.Null()
	}
	o := rt.vm.NewObject()
	o.Set("status", r.Status)
	o.Set("statusText", r.StatusText)
	o.Set("headers", rt.kvToJS(r.Headers))
	o.Set("body", string(r.Body))
	o.Set("bodyBytes", rt.newBytes(append([]byte(nil), r.Body...)))
	o.Set("timeMs", r.TimeMs)
	return o
}

// bytesArg reads a data argument: a string (as UTF-8), Uint8Array or
// ArrayBuffer. Anything else throws a TypeError.
func (rt *jsRuntime) bytesArg(v goja.Value) []byte {
	if v != nil {
		switch x := v.Export().(type) {
		case string:
			return []byte(x)
		case []byte:
			return append([]byte(nil), x...)
		case goja.ArrayBuffer:
			return append([]byte(nil), x.Bytes()...)
		}
	}
	panic(rt.vm.NewTypeError("expected a string, Uint8Array or ArrayBuffer"))
}

// newBytes makes a Uint8Array over b (b is not copied).
func (rt *jsRuntime) newBytes(b []byte) goja.Value {
	if b == nil {
		b = []byte{}
	}
	o, err := rt.vm.New(rt.u8, rt.vm.ToValue(rt.vm.NewArrayBuffer(b)))
	if err != nil {
		panic(err)
	}
	return o
}
