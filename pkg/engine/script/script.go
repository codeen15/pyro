// Package script runs Postman-compatible pre-request and post-response
// scripts in goja (a pure-Go JavaScript engine).
//
// A Runtime is not safe for concurrent use. The load engine keeps one per
// virtual user; interactive sends borrow one from a Pool.
package script

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"

	"github.com/codeen15/pyro/pkg/engine/check"
	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/protocol"
	"github.com/codeen15/pyro/pkg/engine/vars"
)

//go:embed js/chai-4.5.0.js
var chaiSrc string

//go:embed js/pm.js
var pmSrc string

// DefaultTimeout bounds a single script execution.
const DefaultTimeout = 10 * time.Second

var (
	compileOnce sync.Once
	chaiProg    *goja.Program
	pmProg      *goja.Program
	compileErr  error
	userProgs   sync.Map // sha256 -> *goja.Program
)

func compileBuiltins() {
	compileOnce.Do(func() {
		if chaiProg, compileErr = goja.Compile("chai.js", chaiSrc, false); compileErr != nil {
			return
		}
		pmProg, compileErr = goja.Compile("pm.js", pmSrc, false)
	})
}

// Phase is when a script runs.
type Phase string

const (
	PreRequest   Phase = "prerequest"
	PostResponse Phase = "test"
)

// Info is exposed to scripts as pm.info.
type Info struct {
	RequestName string `json:"requestName"`
	RequestID   string `json:"requestId"`
	Iteration   int    `json:"iteration"`
	EventName   Phase  `json:"eventName"`
}

// Input is one script execution.
type Input struct {
	Phase Phase
	// Name labels the script in errors, e.g. "collection pre-request".
	Name     string
	Code     string
	Request  *model.Request
	Response *check.Response
	Vars     *vars.Set
	Info     Info
	// Send executes pm.sendRequest calls; nil disables them.
	Send    func(context.Context, *model.Request) (*protocol.Response, error)
	Timeout time.Duration
}

type Log struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

// Output is what a script produced. Request is the (possibly modified)
// request after a pre-request script.
type Output struct {
	Request *model.Request
	Tests   []check.Result
	Logs    []Log
	Skip    bool
	// NextSet is true when the script called setNextRequest; Next nil means
	// "stop the run".
	NextSet bool
	Next    *string
	Error   string
}

type Runtime struct {
	vm *goja.Runtime
}

// New creates a runtime with chai loaded.
func New() (*Runtime, error) {
	compileBuiltins()
	if compileErr != nil {
		return nil, fmt.Errorf("compiling script builtins: %w", compileErr)
	}
	vm := goja.New()
	if _, err := vm.RunProgram(chaiProg); err != nil {
		return nil, fmt.Errorf("loading chai: %w", err)
	}
	return &Runtime{vm: vm}, nil
}

// Run executes one script. Script errors are reported in Output.Error, never
// returned as Go errors.
func (rt *Runtime) Run(ctx context.Context, in Input) Output {
	out := Output{Request: in.Request}
	if strings.TrimSpace(in.Code) == "" {
		return out
	}
	if in.Vars == nil {
		in.Vars = vars.NewSet()
	}
	prog, err := userProgram(in.Code)
	if err != nil {
		out.Error = fmt.Sprintf("%s script: %v", in.Name, err)
		return out
	}

	timeout := in.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// The watcher may race the script's end, so wait for it to exit before
	// clearing; otherwise a late Interrupt would kill the next script.
	stop, exited := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(exited)
		select {
		case <-ctx.Done():
			rt.vm.Interrupt("script timed out")
		case <-stop:
		}
	}()
	defer func() {
		close(stop)
		<-exited
		rt.vm.ClearInterrupt()
	}()

	vm := rt.vm
	if err := vm.Set("__host", rt.host(ctx, &in, &out)); err != nil {
		out.Error = err.Error()
		return out
	}
	run := func() error {
		if _, err := vm.RunProgram(pmProg); err != nil {
			return err
		}
		if _, err := vm.RunProgram(prog); err != nil {
			return err
		}
		_, err := vm.RunString("__pyroLegacyTests()")
		return err
	}
	if err := run(); err != nil {
		out.Error = fmt.Sprintf("%s script: %s", in.Name, describe(err))
	}
	if in.Phase == PreRequest && in.Request != nil {
		if v, err := vm.RunString("__pyroRequestOut()"); err == nil {
			var r model.Request
			if json.Unmarshal([]byte(v.String()), &r) == nil {
				out.Request = &r
			}
		}
	}
	return out
}

func userProgram(code string) (*goja.Program, error) {
	key := sha256.Sum256([]byte(code))
	if p, ok := userProgs.Load(key); ok {
		return p.(*goja.Program), nil
	}
	// Each script gets its own function scope, like Postman's sandbox.
	p, err := goja.Compile("script", "(function(){\n"+code+"\n})()", false)
	if err != nil {
		return nil, err
	}
	userProgs.Store(key, p)
	return p, nil
}

func describe(err error) string {
	var ex *goja.Exception
	var intr *goja.InterruptedError
	switch {
	case errors.As(err, &intr):
		return fmt.Sprint(intr.Value())
	case errors.As(err, &ex):
		// Strip goja's stack suffix; the first line is the message.
		msg := ex.Error()
		if i := strings.Index(msg, " at "); i > 0 {
			msg = msg[:i]
		}
		return msg
	}
	return err.Error()
}

// host builds the __host object pm.js talks to.
func (rt *Runtime) host(ctx context.Context, in *Input, out *Output) *goja.Object {
	vm := rt.vm
	h := vm.NewObject()
	set := func(name string, fn any) { _ = h.Set(name, fn) }

	scope := func(name string) map[string]string {
		if name == "variables" {
			return in.Vars.Runtime
		}
		return in.Vars.Scope(name)
	}
	set("varGet", func(name, key string) goja.Value {
		if name == "variables" {
			if v, _, ok := in.Vars.Resolver().Lookup(key); ok {
				return vm.ToValue(v)
			}
			return goja.Null()
		}
		if v, ok := scope(name)[key]; ok {
			return vm.ToValue(v)
		}
		return goja.Null()
	})
	set("varSet", func(name, key, value string) {
		if m := scope(name); m != nil && name != "data" {
			m[key] = value
		}
	})
	set("varUnset", func(name, key string) {
		if m := scope(name); m != nil && name != "data" {
			delete(m, key)
		}
	})
	set("varClear", func(name string) {
		if m := scope(name); m != nil && name != "data" {
			for k := range m {
				delete(m, k)
			}
		}
	})
	set("varAll", func(name string) map[string]string {
		if name == "variables" {
			all := map[string]string{}
			r := in.Vars.Resolver()
			for _, s := range []map[string]string{in.Vars.Globals, in.Vars.Collection, in.Vars.Environment, in.Vars.Data, in.Vars.Runtime} {
				for k := range s {
					all[k], _, _ = r.Lookup(k)
				}
			}
			return all
		}
		cp := map[string]string{}
		for k, v := range scope(name) {
			cp[k] = v
		}
		return cp
	})
	set("replaceIn", func(s string) string {
		v, _ := in.Vars.Resolver().Resolve(s)
		return v
	})
	set("envName", func() string { return in.Vars.EnvName })
	set("requestJSON", func() string {
		if in.Request == nil {
			return "{}"
		}
		b, _ := json.Marshal(in.Request)
		return string(b)
	})
	set("responseJSON", func() goja.Value {
		if in.Phase != PostResponse || in.Response == nil || in.Response.Response == nil {
			return goja.Null()
		}
		return vm.ToValue(responseJSON(in.Response.Response))
	})
	info := in.Info
	info.EventName = in.Phase
	set("infoJSON", func() string {
		b, _ := json.Marshal(info)
		return string(b)
	})
	set("test", func(name string, passed bool, msg string) {
		out.Tests = append(out.Tests, check.Result{Name: name, Passed: passed, Error: msg, Source: "script"})
	})
	set("log", func(level, msg string) {
		out.Logs = append(out.Logs, Log{Level: level, Message: msg})
	})
	set("skip", func() { out.Skip = true })
	set("next", func(v goja.Value) {
		out.NextSet = true
		if goja.IsNull(v) || goja.IsUndefined(v) {
			out.Next = nil
			return
		}
		s := v.String()
		out.Next = &s
	})
	set("uuid", vars.UUID)
	set("sendRequest", func(spec string) string {
		if in.Send == nil {
			panic(vm.NewGoError(errors.New("pm.sendRequest is not available here")))
		}
		req, err := parseSendSpec(spec)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		res, err := in.Send(ctx, req)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return responseJSON(res)
	})
	return h
}

func responseJSON(r *protocol.Response) string {
	b, _ := json.Marshal(map[string]any{
		"code":         r.Status,
		"status":       r.StatusText,
		"headers":      r.Headers,
		"cookies":      r.Cookies,
		"body":         string(r.Body),
		"responseTime": float64(r.Timings.Total.Microseconds()) / 1000,
		"responseSize": r.Size,
	})
	return string(b)
}

// parseSendSpec converts a Postman pm.sendRequest argument into a request.
func parseSendSpec(spec string) (*model.Request, error) {
	var s struct {
		URL    any             `json:"url"`
		Method string          `json:"method"`
		Header json.RawMessage `json:"header"`
		Body   *struct {
			Mode       string           `json:"mode"`
			Raw        string           `json:"raw"`
			URLEncoded []model.KeyValue `json:"urlencoded"`
		} `json:"body"`
	}
	if err := json.Unmarshal([]byte(spec), &s); err != nil {
		return nil, fmt.Errorf("pm.sendRequest: %v", err)
	}
	r := &model.Request{Kind: model.KindHTTP, Method: strings.ToUpper(s.Method)}
	switch u := s.URL.(type) {
	case string:
		r.URL = u
	case map[string]any:
		r.URL, _ = u["raw"].(string)
	}
	if r.URL == "" {
		return nil, errors.New("pm.sendRequest: url is required")
	}
	var list []model.KeyValue
	if json.Unmarshal(s.Header, &list) == nil {
		r.Headers = list
	} else {
		var obj map[string]string
		if json.Unmarshal(s.Header, &obj) == nil {
			for k, v := range obj {
				r.Headers = append(r.Headers, model.KeyValue{Key: k, Value: v})
			}
		}
	}
	if s.Body != nil {
		switch s.Body.Mode {
		case "raw":
			r.Body = &model.Body{Type: model.BodyText, Content: s.Body.Raw}
			if strings.HasPrefix(strings.TrimSpace(s.Body.Raw), "{") || strings.HasPrefix(strings.TrimSpace(s.Body.Raw), "[") {
				r.Body.Type = model.BodyJSON
			}
		case "urlencoded":
			r.Body = &model.Body{Type: model.BodyForm, Fields: s.Body.URLEncoded}
		}
	}
	return r, nil
}

// Pool hands out runtimes for interactive use, where sends are concurrent
// but infrequent.
type Pool struct {
	p sync.Pool
}

func (p *Pool) Get() (*Runtime, error) {
	if rt, ok := p.p.Get().(*Runtime); ok {
		return rt, nil
	}
	return New()
}

func (p *Pool) Put(rt *Runtime) { p.p.Put(rt) }
