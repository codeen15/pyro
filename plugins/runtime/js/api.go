package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	pyro "github.com/codeen15/pyro/plugins/sdk/go"
	"github.com/dop251/goja"
	"github.com/dop251/goja/parser"
)

// jsRuntime is one goja VM running one plugin script, with the global pyro
// object installed.
type jsRuntime struct {
	vm  *goja.Runtime
	reg registry

	// Captured before the plugin's script runs, so the script cannot break
	// the runtime by replacing globals.
	mkReq     goja.Callable
	parse     goja.Callable
	stringify goja.Callable
	u8        *goja.Object
	errorCtor *goja.Object
}

// prelude defines what is easier to write in JS: the request object's
// helper methods (on a prototype, so they are not own fields and do not show
// in JSON.stringify(req)) and the builder for request objects.
const prelude = `(function () {
  "use strict";
  const parse = JSON.parse, stringify = JSON.stringify;
  const lower = (s) => String(s).toLowerCase();
  const reqProto = {
    option(key, def) {
      const o = this.options;
      const v = o == null ? undefined : o[key];
      if (v === undefined || v === null || v === "") return def === undefined ? "" : def;
      return v;
    },
    header(name) {
      const n = lower(name);
      if (!Array.isArray(this.headers)) return "";
      for (const h of this.headers) {
        if (h && !h.disabled && lower(h.key) === n) return h.value == null ? "" : String(h.value);
      }
      return "";
    },
    setHeader(name, value) {
      if (!Array.isArray(this.headers)) this.headers = [];
      const n = lower(name);
      for (const h of this.headers) {
        if (h && lower(h.key) === n) {
          h.value = String(value);
          delete h.disabled;
          return;
        }
      }
      this.headers.push({ key: String(name), value: String(value) });
    },
  };
  function mkReq(json) {
    const r = parse(json);
    const o = Object.create(reqProto);
    o.kind = r.kind || "";
    o.name = r.name || "";
    o.method = r.method || "";
    o.url = r.url || "";
    o.headers = r.headers || [];
    o.query = r.query || [];
    o.body = r.body || "";
    o.options = r.options || {};
    return o;
  }
  return { mkReq, parse, stringify, Uint8Array, Error };
})()`

func newRuntime(reg registry) (*jsRuntime, error) {
	vm := goja.New()
	rt := &jsRuntime{vm: vm, reg: reg}
	v, err := vm.RunScript("<pyro>", prelude)
	if err != nil {
		return nil, err
	}
	p := v.ToObject(vm)
	var ok bool
	if rt.mkReq, ok = goja.AssertFunction(p.Get("mkReq")); !ok {
		return nil, errors.New("prelude: mkReq")
	}
	rt.parse, _ = goja.AssertFunction(p.Get("parse"))
	rt.stringify, _ = goja.AssertFunction(p.Get("stringify"))
	rt.u8 = p.Get("Uint8Array").ToObject(vm)
	rt.errorCtor = p.Get("Error").ToObject(vm)
	if err := vm.Set("pyro", rt.api()); err != nil {
		return nil, err
	}
	return rt, nil
}

// load runs the plugin's entry script. Errors read
// "plugin.js:12:5 SyntaxError: …".
func (rt *jsRuntime) load(name, src string) error {
	prog, err := parser.ParseFile(nil, name, src, 0)
	if err != nil {
		var list parser.ErrorList
		if errors.As(err, &list) && len(list) > 0 {
			return fmt.Errorf("%s SyntaxError: %s", list[0].Position, list[0].Message)
		}
		return fmt.Errorf("%s SyntaxError: %v", name, err)
	}
	p, err := goja.CompileAST(prog, false)
	if err != nil {
		return errors.New(formatError(err))
	}
	if _, err := rt.vm.RunProgram(p); err != nil {
		return errors.New(formatError(err))
	}
	return nil
}

// api builds the global pyro object.
func (rt *jsRuntime) api() *goja.Object {
	vm := rt.vm
	o := vm.NewObject()
	o.Set("registerProtocol", func(c goja.FunctionCall) goja.Value {
		id, fn := rt.handlerArgs(c, "registerProtocol")
		rt.reg.protocol(id, rt.protocolFunc(fn))
		return goja.Undefined()
	})
	o.Set("registerAuth", func(c goja.FunctionCall) goja.Value {
		id, fn := rt.handlerArgs(c, "registerAuth")
		rt.reg.auth(id, rt.authFunc(fn))
		return goja.Undefined()
	})
	o.Set("registerProcessor", func(c goja.FunctionCall) goja.Value {
		id, fn := rt.handlerArgs(c, "registerProcessor")
		rt.reg.processor(id, rt.processorFunc(fn))
		return goja.Undefined()
	})
	o.Set("dial", rt.dial)

	log := vm.NewObject()
	for i, name := range []string{"debug", "info", "warn", "error"} {
		level := pyro.LevelDebug + i
		log.Set(name, func(c goja.FunctionCall) goja.Value {
			parts := make([]string, len(c.Arguments))
			for j, a := range c.Arguments {
				parts[j] = rt.logText(a)
			}
			pyro.Logf(level, "%s", strings.Join(parts, " "))
			return goja.Undefined()
		})
	}
	o.Set("log", log)

	// base64 and hex: encode(string | bytes) → string, decode(string) → bytes.
	codec := func(encode func([]byte) string, decode func(string) ([]byte, error)) *goja.Object {
		e := vm.NewObject()
		e.Set("encode", func(c goja.FunctionCall) goja.Value {
			return vm.ToValue(encode(rt.bytesArg(c.Argument(0))))
		})
		e.Set("decode", func(c goja.FunctionCall) goja.Value {
			b, err := decode(str(c.Argument(0)))
			if err != nil {
				panic(rt.newError(err.Error()))
			}
			return rt.newBytes(b)
		})
		return e
	}
	enc := vm.NewObject()
	// utf8: encode(string) → bytes, decode(bytes) → string.
	utf8 := vm.NewObject()
	utf8.Set("encode", func(c goja.FunctionCall) goja.Value {
		return rt.newBytes([]byte(str(c.Argument(0))))
	})
	utf8.Set("decode", func(c goja.FunctionCall) goja.Value {
		return vm.ToValue(string(rt.bytesArg(c.Argument(0))))
	})
	enc.Set("utf8", utf8)
	enc.Set("base64", codec(base64.StdEncoding.EncodeToString, func(s string) ([]byte, error) {
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			if b2, err2 := base64.RawStdEncoding.DecodeString(s); err2 == nil {
				return b2, nil
			}
			return nil, fmt.Errorf("invalid base64: %v", err)
		}
		return b, nil
	}))
	enc.Set("hex", codec(hex.EncodeToString, func(s string) ([]byte, error) {
		b, err := hex.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("invalid hex: %v", err)
		}
		return b, nil
	}))
	o.Set("encoding", enc)

	cr := vm.NewObject()
	cr.Set("sha256", func(c goja.FunctionCall) goja.Value {
		sum := sha256.Sum256(rt.bytesArg(c.Argument(0)))
		return rt.newBytes(sum[:])
	})
	cr.Set("hmacSHA256", func(c goja.FunctionCall) goja.Value {
		m := hmac.New(sha256.New, rt.bytesArg(c.Argument(0)))
		m.Write(rt.bytesArg(c.Argument(1)))
		return rt.newBytes(m.Sum(nil))
	})
	o.Set("crypto", cr)
	return o
}

func (rt *jsRuntime) handlerArgs(c goja.FunctionCall, what string) (string, goja.Callable) {
	id, ok := c.Argument(0).Export().(string)
	if !ok || id == "" {
		panic(rt.vm.NewTypeError("pyro.%s: id must be a non-empty string", what))
	}
	fn, ok := goja.AssertFunction(c.Argument(1))
	if !ok {
		panic(rt.vm.NewTypeError("pyro.%s(%q): handler must be a function", what, id))
	}
	return id, fn
}

func (rt *jsRuntime) protocolFunc(fn goja.Callable) pyro.ProtocolFunc {
	return func(ctx *pyro.Context, req *pyro.Request) (*pyro.Response, error) {
		v, err := rt.call(fn, rt.contextToJS(ctx), rt.requestToJS(req))
		if err != nil {
			return nil, err
		}
		return rt.responseFromJS(v)
	}
}

func (rt *jsRuntime) authFunc(fn goja.Callable) pyro.AuthFunc {
	return func(ctx *pyro.Context, req *pyro.Request) error {
		r := rt.requestToJS(req)
		if _, err := rt.call(fn, rt.contextToJS(ctx), r); err != nil {
			return err
		}
		back, err := rt.requestFromJS(r)
		if err != nil {
			return err
		}
		*req = *back
		return nil
	}
}

func (rt *jsRuntime) processorFunc(fn goja.Callable) pyro.ProcessorFunc {
	return func(ctx *pyro.Context, p *pyro.Process) error {
		vm := rt.vm
		r := rt.requestToJS(p.Request)
		vars := rt.stringMapToJS(p.Vars)
		o := vm.NewObject()
		o.Set("phase", p.Phase)
		o.Set("request", r)
		o.Set("response", rt.processResponseToJS(p.Response))
		o.Set("vars", vars)
		o.Set("setVar", func(c goja.FunctionCall) goja.Value {
			name, value := str(c.Argument(0)), str(c.Argument(1))
			p.SetVar(name, value)
			vars.Set(name, value)
			return goja.Undefined()
		})
		o.Set("test", func(c goja.FunctionCall) goja.Value {
			p.Test(str(c.Argument(0)), c.Argument(1).ToBoolean(), str(c.Argument(2)))
			return goja.Undefined()
		})
		if _, err := rt.call(fn, rt.contextToJS(ctx), o); err != nil {
			return err
		}
		back, err := rt.requestFromJS(r)
		if err != nil {
			return err
		}
		*p.Request = *back
		return nil
	}
}

var promiseType = reflect.TypeOf((*goja.Promise)(nil))

// call runs a handler. A returned promise is settled by then (goja runs
// queued jobs before the call returns), so async handlers work as long as
// they await nothing that needs real I/O.
func (rt *jsRuntime) call(fn goja.Callable, args ...goja.Value) (goja.Value, error) {
	v, err := fn(goja.Undefined(), args...)
	if err != nil {
		return nil, errors.New(formatError(err))
	}
	if o, ok := v.(*goja.Object); ok && o.ExportType() == promiseType {
		p := o.Export().(*goja.Promise)
		switch p.State() {
		case goja.PromiseStateFulfilled:
			return p.Result(), nil
		case goja.PromiseStateRejected:
			return nil, errors.New(valueText(p.Result()))
		default:
			return nil, errors.New("handler returned a promise that never settled")
		}
	}
	return v, nil
}

// formatError turns a goja error into "plugin.js:12:5 TypeError: message".
func formatError(err error) string {
	var ex *goja.Exception
	if errors.As(err, &ex) {
		text := valueText(ex.Value())
		for _, f := range ex.Stack() {
			if f.SrcName() == "<native>" {
				continue
			}
			if pos := f.Position(); pos.Line > 0 {
				return pos.String() + " " + text
			}
			break
		}
		return text
	}
	var syn *goja.CompilerSyntaxError
	if errors.As(err, &syn) && syn.File != nil {
		return fmt.Sprintf("%s SyntaxError: %s", syn.File.Position(syn.Offset), syn.Message)
	}
	var ref *goja.CompilerReferenceError
	if errors.As(err, &ref) && ref.File != nil {
		return fmt.Sprintf("%s ReferenceError: %s", ref.File.Position(ref.Offset), ref.Message)
	}
	return err.Error()
}

// valueText is "name: message" for Error objects and String(v) otherwise.
func valueText(v goja.Value) string {
	if o, ok := v.(*goja.Object); ok && o.ClassName() == "Error" {
		name, msg := str(o.Get("name")), str(o.Get("message"))
		if name == "" {
			name = "Error"
		}
		if msg == "" {
			return name
		}
		return name + ": " + msg
	}
	if v == nil {
		return "undefined"
	}
	return v.String()
}

func (rt *jsRuntime) logText(v goja.Value) string {
	if o, ok := v.(*goja.Object); ok {
		if _, isFn := goja.AssertFunction(o); !isFn && o.ClassName() != "Error" {
			if s, err := rt.stringify(goja.Undefined(), o); err == nil && !goja.IsUndefined(s) {
				return s.String()
			}
		}
		return valueText(o)
	}
	if v == nil {
		return "undefined"
	}
	return v.String()
}

// newError makes a JS Error; panic with it to throw.
func (rt *jsRuntime) newError(msg string) *goja.Object {
	o, err := rt.vm.New(rt.errorCtor, rt.vm.ToValue(msg))
	if err != nil {
		panic(err)
	}
	return o
}

// dial implements pyro.dial(addr, {tls, timeoutMs}).
func (rt *jsRuntime) dial(c goja.FunctionCall) goja.Value {
	vm := rt.vm
	addr := str(c.Argument(0))
	opts := optionsArg(c.Argument(1))
	conn, err := pyro.Dial(addr, optBool(opts, "tls"), optMillis(opts, "timeoutMs", 10*time.Second))
	if err != nil {
		panic(rt.newError(err.Error()))
	}
	closed := false
	open := func() {
		if closed {
			panic(rt.newError("connection closed"))
		}
	}
	o := vm.NewObject()
	o.Set("write", func(c goja.FunctionCall) goja.Value {
		open()
		n, err := conn.Write(rt.bytesArg(c.Argument(0)))
		if err != nil {
			panic(rt.newError(err.Error()))
		}
		return vm.ToValue(n)
	})
	o.Set("read", func(c goja.FunctionCall) goja.Value {
		open()
		opts := optionsArg(c.Argument(0))
		conn.SetReadTimeout(optMillis(opts, "timeoutMs", 10*time.Second))
		size := 64 << 10
		if v := opts["max"]; v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
			size = int(v.ToInteger())
			if size < 1 || size > 16<<20 {
				panic(vm.NewTypeError("conn.read: max must be between 1 and %d", 16<<20))
			}
		}
		buf := make([]byte, size)
		n, err := conn.Read(buf)
		if n > 0 {
			return rt.newBytes(buf[:n])
		}
		switch {
		case err == nil:
			return rt.newBytes(nil)
		case errors.Is(err, io.EOF):
			return goja.Null()
		case errors.Is(err, pyro.ErrTimeout):
			panic(rt.newError("read timed out"))
		default:
			panic(rt.newError(err.Error()))
		}
	})
	o.Set("close", func(goja.FunctionCall) goja.Value {
		if !closed {
			closed = true
			conn.Close()
		}
		return goja.Undefined()
	})
	return o
}

func optionsArg(v goja.Value) map[string]goja.Value {
	o, ok := v.(*goja.Object)
	if !ok {
		return nil
	}
	m := map[string]goja.Value{}
	for _, k := range o.Keys() {
		m[k] = o.Get(k)
	}
	return m
}

func optBool(opts map[string]goja.Value, key string) bool {
	v := opts[key]
	return v != nil && v.ToBoolean()
}

func optMillis(opts map[string]goja.Value, key string, def time.Duration) time.Duration {
	v := opts[key]
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return def
	}
	ms := v.ToInteger()
	if ms < 0 {
		ms = 0
	}
	return time.Duration(ms) * time.Millisecond
}
