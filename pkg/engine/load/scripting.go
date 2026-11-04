package load

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/codeen15/pyro/pkg/engine"
	"github.com/codeen15/pyro/pkg/engine/metrics"
	"github.com/codeen15/pyro/pkg/engine/model"
)

// Script steps run JavaScript with a pyro.* API for VUs (see
// docs/load-engine.md, "Script steps"). Each VU has its own goja runtime,
// separate from the pm.* runtime that request scripts use.

// compileScript wraps a step's code in its own function scope.
func compileScript(code string) (*goja.Program, error) {
	return goja.Compile("script", "(function(){\n"+code+"\n})()", false)
}

var (
	scriptRequestRe = regexp.MustCompile("pyro\\.request\\(\\s*[\"'`]([^\"'`$]+)[\"'`]")
	scriptURLRe     = regexp.MustCompile("pyro\\.http\\.(?:get|post|put|patch|del|head|request)\\((?:\\s*[\"'`][A-Za-z]+[\"'`]\\s*,)?\\s*[\"'`](https?://[^\"'`/$]+)")
)

// scriptRequests lists the workspace requests a script calls with literal
// paths (pyro.request("shop/list")), so they're resolved before the test.
func scriptRequests(code string) []string {
	var out []string
	for _, m := range scriptRequestRe.FindAllStringSubmatch(code, -1) {
		out = append(out, m[1])
	}
	return out
}

// scriptHosts lists hosts a script calls with literal URLs.
func scriptHosts(code string) []string {
	var out []string
	for _, m := range scriptURLRe.FindAllStringSubmatch(code, -1) {
		if u, err := url.Parse(m[1]); err == nil && u.Hostname() != "" {
			out = append(out, u.Hostname())
		}
	}
	return out
}

// scriptError is thrown by pyro.fail: the script stops and the message is
// counted as an error.
type scriptError struct{ msg string }

func (e *scriptError) Error() string { return e.msg }

// script runs a script step.
func (v *vu) script(ctx context.Context, st *Step, group string) error {
	prog := v.t.scripts[st]
	if prog == nil {
		return fmt.Errorf("script step wasn't compiled")
	}
	if v.js == nil {
		v.js = goja.New()
		v.js.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
		if err := v.js.Set("pyro", v.pyroAPI()); err != nil {
			return err
		}
		v.js.Set("console", map[string]any{"log": func(goja.FunctionCall) goja.Value { return goja.Undefined() }})
	}
	v.jsCtx, v.jsGroup = ctx, group
	// Per-iteration values, refreshed before each run.
	api := v.js.Get("pyro").ToObject(v.js)
	api.Get("vu").ToObject(v.js).Set("iteration", v.iteration)
	api.Set("data", v.vars.Data)
	stop, exited := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(exited)
		select {
		case <-ctx.Done():
			v.js.Interrupt("test stopped")
		case <-stop:
		}
	}()
	defer func() {
		close(stop)
		<-exited
		v.js.ClearInterrupt()
	}()
	_, err := v.js.RunProgram(prog)
	if err == nil || ctx.Err() != nil {
		return ctx.Err()
	}
	var ie *goja.InterruptedError
	if errors.As(err, &ie) {
		return ctx.Err()
	}
	msg := err.Error()
	var ex *goja.Exception
	if errors.As(err, &ex) {
		msg = ex.Error()
		if i := strings.Index(msg, " at "); i > 0 {
			msg = msg[:i]
		}
	}
	name := st.Name
	if name == "" {
		name = "script"
	}
	msg = strings.TrimPrefix(strings.TrimPrefix(msg, "GoError: "), "Error: ")
	v.t.errors.add(name + ": " + msg)
	return nil // like a failed request, a failing script doesn't stop the VU
}

// pyroAPI builds the pyro object for this VU.
func (v *vu) pyroAPI() map[string]any {
	vm := func() *goja.Runtime { return v.js }
	throw := func(format string, a ...any) { panic(vm().NewGoError(fmt.Errorf(format, a...))) }
	response := func(res *engine.Result) map[string]any {
		out := map[string]any{"ok": res.Error == "" && res.Response != nil, "error": res.Error}
		if r := res.Response; r != nil {
			headers := map[string]string{}
			for _, h := range r.Headers {
				headers[strings.ToLower(h.Key)] = h.Value
			}
			body := string(r.Body)
			out["status"], out["statusText"], out["body"], out["headers"] = r.Status, r.StatusText, body, headers
			out["timings"] = map[string]float64{"total": ms(r.Timings.Total), "waiting": ms(r.Timings.Wait)}
			out["json"] = func() goja.Value {
				var x any
				if err := json.Unmarshal(r.Body, &x); err != nil {
					throw("response isn't JSON: %v", err)
				}
				return vm().ToValue(x)
			}
		}
		passed := true
		for _, t := range res.Tests {
			passed = passed && t.Passed
		}
		out["passed"] = passed
		return out
	}
	send := func(in engine.SendInput, tags metrics.Tags, name string) map[string]any {
		if err := v.t.pace.wait(v.jsCtx, v.stopping); err != nil {
			panic(vm().NewGoError(err))
		}
		res := v.eng.Send(v.jsCtx, in)
		if v.jsCtx.Err() == nil {
			v.recordHTTP(res, tags, name)
		}
		return response(res)
	}
	httpCall := func(method, rawURL string, body goja.Value, opts map[string]any) map[string]any {
		u, err := url.Parse(rawURL)
		if err != nil || u.Hostname() == "" {
			throw("pyro.http: invalid URL %q", rawURL)
		}
		if !v.t.hostAllowed(u.Hostname()) {
			throw("pyro.http: %s isn't one of this load test's confirmed hosts; list it under hosts: in the plan", u.Hostname())
		}
		req := &model.Request{Kind: model.KindHTTP, Method: method, URL: rawURL}
		name, _ := opts["name"].(string)
		if name == "" {
			name = method + " " + u.Scheme + "://" + u.Host + u.Path
		}
		if hs, ok := opts["headers"].(map[string]any); ok {
			for k, val := range hs {
				req.Headers = append(req.Headers, model.KeyValue{Key: k, Value: fmt.Sprint(val)})
			}
		}
		if body != nil && !goja.IsUndefined(body) && !goja.IsNull(body) {
			switch b := body.Export().(type) {
			case string:
				req.Body = &model.Body{Type: model.BodyText, Content: b}
			default:
				j, err := json.Marshal(b)
				if err != nil {
					throw("pyro.http: body: %v", err)
				}
				req.Body = &model.Body{Type: model.BodyJSON, Content: string(j)}
				req.Headers = append(req.Headers, model.KeyValue{Key: "Content-Type", Value: "application/json"})
			}
		}
		return send(engine.SendInput{Request: req, Vars: v.vars, Iteration: v.iteration},
			metrics.Tags{Scenario: v.scenario, Request: name, Group: v.jsGroup}, name)
	}
	opts := func(x goja.Value) map[string]any {
		if x == nil || goja.IsUndefined(x) || goja.IsNull(x) {
			return map[string]any{}
		}
		m, _ := x.Export().(map[string]any)
		if m == nil {
			m = map[string]any{}
		}
		return m
	}
	metric := func(t metrics.Type) func(string) map[string]any {
		return func(name string) map[string]any {
			if err := metrics.Register(name, t); err != nil {
				throw("pyro.metrics: %v", err)
			}
			add := func(val goja.Value) {
				var f float64
				switch t {
				case metrics.Rate:
					if val.ToBoolean() {
						f = 1
					}
				default:
					f = val.ToFloat()
				}
				v.buf.Add(metrics.Sample{Metric: name, Tags: metrics.Tags{Scenario: v.scenario, Group: v.jsGroup}, Value: f})
			}
			return map[string]any{"add": add}
		}
	}

	return map[string]any{
		"vu":       map[string]any{"id": v.id, "iteration": 0},
		"scenario": v.scenario,
		// pyro.request(path, {name, vars}) sends a workspace request through the
		// full pipeline (its scripts, auth, assertions, extractors).
		"request": func(path string, o goja.Value) map[string]any {
			target, ok := v.t.targets[path]
			if !ok {
				throw("pyro.request: %s wasn't resolved before the test; call it with a literal path", path)
			}
			op := opts(o)
			if vs, ok := op["vars"].(map[string]any); ok {
				for k, val := range vs {
					v.vars.Runtime[k] = fmt.Sprint(val)
				}
			}
			name, _ := op["name"].(string)
			if name == "" {
				name = target.Path
			}
			in, tags, err := v.input(target, name, v.jsGroup)
			if err != nil {
				throw("pyro.request: %v", err)
			}
			if k := target.Request.Kind; k == model.KindWebSocket || k == model.KindSocketIO {
				throw("pyro.request: %s is a %s step; put it in the flow as a request step", path, k)
			}
			return send(in, tags, name)
		},
		"http": map[string]any{
			"request": func(method, u string, body, o goja.Value) map[string]any {
				return httpCall(strings.ToUpper(method), u, body, opts(o))
			},
			"get":  func(u string, o goja.Value) map[string]any { return httpCall("GET", u, nil, opts(o)) },
			"head": func(u string, o goja.Value) map[string]any { return httpCall("HEAD", u, nil, opts(o)) },
			"del":  func(u string, o goja.Value) map[string]any { return httpCall("DELETE", u, nil, opts(o)) },
			"post": func(u string, body, o goja.Value) map[string]any { return httpCall("POST", u, body, opts(o)) },
			"put":  func(u string, body, o goja.Value) map[string]any { return httpCall("PUT", u, body, opts(o)) },
			"patch": func(u string, body, o goja.Value) map[string]any {
				return httpCall("PATCH", u, body, opts(o))
			},
		},
		// pyro.check(value, {"name": v => bool}) records each as a check
		// (the checks rate) and returns whether all passed.
		"check": func(val goja.Value, checks map[string]goja.Value) bool {
			all := true
			for name, fn := range checks {
				call, ok := goja.AssertFunction(fn)
				pass := false
				if ok {
					r, err := call(goja.Undefined(), val)
					pass = err == nil && r.ToBoolean()
				}
				all = all && pass
				p := 0.0
				if pass {
					p = 1
				}
				v.buf.Add(metrics.Sample{Metric: "checks", Tags: metrics.Tags{Scenario: v.scenario, Request: name, Group: v.jsGroup}, Value: p})
			}
			return all
		},
		"sleep": func(seconds float64) {
			if err := sleep(v.jsCtx, time.Duration(seconds*float64(time.Second))); err != nil {
				panic(vm().NewGoError(err))
			}
		},
		// pyro.group(name, fn) tags what fn sends with the group and records
		// group_duration.
		"group": func(name string, fn goja.Value) goja.Value {
			call, ok := goja.AssertFunction(fn)
			if !ok {
				throw("pyro.group: the second argument must be a function")
			}
			prev := v.jsGroup
			if prev != "" {
				name = prev + "::" + name
			}
			v.jsGroup = name
			start := time.Now()
			defer func() { v.jsGroup = prev }()
			r, err := call(goja.Undefined())
			if v.jsCtx.Err() == nil {
				v.buf.Add(metrics.Sample{Metric: "group_duration", Tags: metrics.Tags{Scenario: v.scenario, Group: name}, Value: ms(time.Since(start))})
			}
			if err != nil {
				panic(err)
			}
			return r
		},
		"vars": map[string]any{
			"get": func(k string) goja.Value {
				if val, _, ok := v.vars.Resolver().Lookup(k); ok {
					return vm().ToValue(val)
				}
				return goja.Undefined()
			},
			"set": func(k string, val goja.Value) { v.vars.Runtime[k] = val.String() },
		},
		"metrics": map[string]any{
			"counter": metric(metrics.Counter),
			"trend":   metric(metrics.Trend),
			"rate":    metric(metrics.Rate),
		},
		"fail": func(msg string) { panic(vm().NewGoError(&scriptError{msg})) },
	}
}
