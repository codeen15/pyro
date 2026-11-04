// Package engine runs requests through Pyro's shared pipeline:
//
//	pre-processors (collection → folders → request; each level's legacy
//	script first) → resolve variables → apply auth → execute
//	→ post-processors (same order) → legacy assertions → legacy extractors
package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/codeen15/pyro/pkg/engine/auth"
	"github.com/codeen15/pyro/pkg/engine/check"
	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/plugin"
	"github.com/codeen15/pyro/pkg/engine/process"
	"github.com/codeen15/pyro/pkg/engine/protocol"
	"github.com/codeen15/pyro/pkg/engine/protocol/httpdriver"
	"github.com/codeen15/pyro/pkg/engine/script"
	"github.com/codeen15/pyro/pkg/engine/vars"
)

type Engine struct {
	HTTP    *httpdriver.Driver
	Scripts *script.Pool
	// Plugins provides plugin request and auth types; nil disables them.
	Plugins *plugin.Manager
	// OAuth fetches and caches OAuth 2.0 tokens; nil allows only pasted tokens.
	OAuth *auth.OAuth2

	digestOnce sync.Once
	digest     *auth.DigestCache
}

func (e *Engine) digests() *auth.DigestCache {
	e.digestOnce.Do(func() { e.digest = &auth.DigestCache{} })
	return e.digest
}

// PluginID returns the id of a plugin kind ("plugin:tcp" → "tcp").
func PluginID(k model.Kind) (string, bool) {
	return strings.CutPrefix(string(k), "plugin:")
}

func New(http *httpdriver.Driver) *Engine {
	return &Engine{HTTP: http, Scripts: &script.Pool{}, OAuth: auth.NewOAuth2()}
}

// SendInput is everything needed to send one request.
type SendInput struct {
	Request *model.Request
	// Path is the request's workspace path (pm.info.requestId), if saved.
	Path string
	// Chain holds collection then folder settings, outermost first.
	Chain []*model.Collection
	// Vars is mutated by scripts and extractors. Nil means NewVars(nil, Chain).
	Vars      *vars.Set
	Iteration int
	// Script, when set, is used instead of a pooled runtime (one per VU).
	Script *script.Runtime
}

// Result describes one send. Error is set when no response was received;
// HTTP error statuses are still successful sends.
type Result struct {
	Sent       *model.Request     `json:"sent"`
	Response   *protocol.Response `json:"response,omitempty"`
	Unresolved []string           `json:"unresolved,omitempty"`
	Error      string             `json:"error,omitempty"`
	StartedAt  time.Time          `json:"startedAt"`

	Tests        []check.Result    `json:"tests,omitempty"`
	Logs         []script.Log      `json:"logs,omitempty"`
	ScriptErrors []string          `json:"scriptErrors,omitempty"`
	Extracted    map[string]string `json:"extracted,omitempty"`
	// Problems lists extractors that matched nothing.
	Problems []string `json:"problems,omitempty"`
	Skipped  bool     `json:"skipped,omitempty"`

	// NextSet/Next carry setNextRequest for the collection runner.
	NextSet bool    `json:"-"`
	Next    *string `json:"-"`
}

// Passed reports whether the send got a response and every test passed.
func (r *Result) Passed() bool {
	if r.Error != "" || len(r.ScriptErrors) > 0 {
		return false
	}
	for _, t := range r.Tests {
		if !t.Passed {
			return false
		}
	}
	return true
}

// NewVars builds a variable set from an environment and a request's chain.
func NewVars(env *model.Environment, chain []*model.Collection) *vars.Set {
	v := vars.NewSet()
	if env != nil {
		v.Environment = vars.Map(env.Variables)
		v.EnvName = env.Name
	}
	if len(chain) > 0 && chain[0] != nil {
		v.Collection = vars.Map(chain[0].Variables)
	}
	for i := len(chain) - 1; i >= 1; i-- {
		if chain[i] != nil && len(chain[i].Variables) > 0 {
			v.Folders = append(v.Folders, vars.Map(chain[i].Variables))
		}
	}
	return v
}

// Send runs the pipeline. It never mutates in.Request.
func (e *Engine) Send(ctx context.Context, in SendInput) *Result {
	res := &Result{StartedAt: time.Now()}
	if in.Request == nil {
		res.Error = "no request"
		return res
	}
	if k := in.Request.Kind; k == model.KindWebSocket || k == model.KindSocketIO {
		res.Error = fmt.Sprintf("%s requests are opened as sessions, not sent", k)
		return res
	}
	if in.Vars == nil {
		in.Vars = NewVars(nil, in.Chain)
	}

	rt := in.Script
	if rt == nil && hasScripts(in) {
		pooled, err := e.Scripts.Get()
		if err != nil {
			res.Error = err.Error()
			return res
		}
		defer e.Scripts.Put(pooled)
		rt = pooled
	}

	req := in.Request
	info := script.Info{RequestName: req.Name, RequestID: in.Path, Iteration: in.Iteration}

	// Pre-processors (scripts may rewrite the request) run before it is resolved.
	for _, st := range stepsFor(in, true) {
		var ok bool
		if req, ok = e.runPre(ctx, st, req, in, rt, info, res); !ok {
			return res
		}
	}

	r := in.Vars.Resolver()
	sent, unresolved := ResolveRequest(req, r)
	if err := e.applyAuth(ctx, sent, resolveAuth(auth.Effective(req.Auth, in.Chain), r, &unresolved), res); err != nil {
		res.Sent, res.Unresolved, res.Error = sent, unresolved, err.Error()
		return res
	}
	ea := resolveAuth(auth.Effective(req.Auth, in.Chain), r, &unresolved)
	sent.Auth = nil // applied as headers/query; keep the record unambiguous
	sent.Scripts = nil
	res.Sent, res.Unresolved = sent, unresolved

	resp, err := e.execute(ctx, sent, res)
	if err == nil && ea != nil && ea.Type == "digest" {
		resp, err = e.answerDigest(ctx, sent, ea, resp, res)
	}
	if err != nil {
		res.Error = err.Error()
		// Assertions still report, as failures, so a run shows what was expected.
		for _, st := range stepsFor(in, false) {
			if st.proc != nil && st.proc.Type == model.ProcAssert && st.proc.Assert != nil {
				res.Tests = append(res.Tests, check.Assert(resolveAssertions([]model.Assertion{*st.proc.Assert}, in.Vars), nil)...)
			}
		}
		res.Tests = append(res.Tests, check.Assert(resolveAssertions(req.Assertions, in.Vars), nil)...)
		return res
	}
	res.Response = resp
	wrapped := check.Wrap(resp)

	for _, st := range stepsFor(in, false) {
		e.runPost(ctx, st, sent, wrapped, in, rt, info, res)
	}

	res.Tests = append(res.Tests, check.Assert(resolveAssertions(req.Assertions, in.Vars), wrapped)...)
	if len(req.Extract) > 0 {
		res.extract(req.Extract, wrapped, in.Vars)
	}
	return res
}

// execute sends a resolved request with the HTTP driver or its plugin.
func (e *Engine) execute(ctx context.Context, sent *model.Request, res *Result) (*protocol.Response, error) {
	id, ok := PluginID(sent.Kind)
	if !ok {
		return e.HTTP.Execute(ctx, sent)
	}
	if e.Plugins == nil {
		return nil, fmt.Errorf("%s requests need plugins, which aren't available here", sent.Kind)
	}
	resp, logs, err := e.Plugins.Execute(ctx, id, sent)
	res.addPluginLogs(logs)
	return resp, err
}

// applyAuth applies built-in auth or runs a plugin auth type.
func (e *Engine) applyAuth(ctx context.Context, sent *model.Request, a *model.Auth, res *Result) error {
	if a == nil {
		return nil
	}
	if a.Type == "digest" {
		// Answered after the server's challenge (answerDigest); a cached
		// challenge for this host lets the first request authenticate.
		u, err := httpdriver.TargetURL(sent)
		if err != nil {
			return nil // the driver reports a bad URL
		}
		if h := e.digests().Authorize(methodOf(sent), u.String(), a.Params["username"], a.Params["password"]); h != "" {
			setHeader(sent, "Authorization", h)
		}
		return nil
	}
	if a.Type == "oauth2" && auth.UsesTokenFetching(a.Params) {
		if e.OAuth == nil {
			return fmt.Errorf("OAuth 2.0 token fetching isn't available here; set an access token")
		}
		tok, err := e.OAuth.Token(ctx, a.Params)
		if err != nil {
			return err
		}
		auth.ApplyOAuth2Token(sent, a.Params, tok.AccessToken)
		return nil
	}
	id, ok := strings.CutPrefix(a.Type, "plugin:")
	if !ok {
		return auth.Apply(sent, a)
	}
	if e.Plugins == nil {
		return fmt.Errorf("auth type %s needs plugins, which aren't available here", a.Type)
	}
	logs, err := e.Plugins.Sign(ctx, id, sent, a.Params)
	if res != nil {
		res.addPluginLogs(logs)
	}
	return err
}

func (res *Result) addPluginLogs(logs []plugin.Log) {
	for _, l := range logs {
		res.Logs = append(res.Logs, script.Log{Level: l.Level, Message: "[plugin] " + l.Message})
	}
}

func (res *Result) absorb(out script.Output) {
	res.Tests = append(res.Tests, out.Tests...)
	res.Logs = append(res.Logs, out.Logs...)
	if out.Error != "" {
		res.ScriptErrors = append(res.ScriptErrors, out.Error)
	}
	if out.NextSet {
		res.NextSet, res.Next = true, out.Next
	}
}

// sendRaw executes pm.sendRequest calls: no scripts, no inheritance.
func (e *Engine) sendRaw(ctx context.Context, r *model.Request) (*protocol.Response, error) {
	return e.HTTP.Execute(ctx, r)
}

// step is one pre or post step: a legacy script (code) or a processor.
type step struct {
	label string
	code  string
	proc  *model.Processor
}

// stepsFor lists a phase's steps, outer level first: each level's legacy
// script, then its processors. Disabled processors are left out.
func stepsFor(in SendInput, pre bool) []step {
	var out []step
	level := func(name string, s *model.Scripts, procs []model.Processor) {
		if s != nil {
			code := s.Post
			if pre {
				code = s.Pre
			}
			if code != "" {
				out = append(out, step{label: name, code: code})
			}
		}
		for i := range procs {
			if !procs[i].Disabled {
				out = append(out, step{label: process.Label(procs[i], name, pre, i), proc: &procs[i]})
			}
		}
	}
	pick := func(p, q []model.Processor) []model.Processor {
		if pre {
			return p
		}
		return q
	}
	for i, c := range in.Chain {
		if c == nil {
			continue
		}
		name := "folder"
		if i == 0 {
			name = "collection"
		}
		level(name, c.Scripts, pick(c.Pre, c.Post))
	}
	level("request", in.Request.Scripts, pick(in.Request.Pre, in.Request.Post))
	return out
}

// UsesScripts reports whether sending req runs any JavaScript (legacy
// scripts or script processors at any level), so callers know whether to
// prepare a script runtime.
func UsesScripts(req *model.Request, chain []*model.Collection) bool {
	return hasScripts(SendInput{Request: req, Chain: chain})
}

func hasScripts(in SendInput) bool {
	for _, pre := range []bool{true, false} {
		for _, st := range stepsFor(in, pre) {
			if st.code != "" || st.proc.Type == model.ProcScript {
				return true
			}
		}
	}
	return false
}

// runPre runs one pre step and returns the (possibly rewritten) request.
// It returns false when the request must not be sent; res is then final.
func (e *Engine) runPre(ctx context.Context, st step, req *model.Request, in SendInput, rt *script.Runtime, info script.Info, res *Result) (*model.Request, bool) {
	fail := func(msg string) (*model.Request, bool) {
		res.ScriptErrors = append(res.ScriptErrors, msg)
		res.Sent = req
		res.Error = "not sent: pre-processor failed"
		return req, false
	}
	code := st.code
	if st.proc != nil {
		p := st.proc
		if err := process.ValidateProcessor(*p, true); err != nil {
			return fail(fmt.Sprintf("%s: %v", st.label, err))
		}
		switch {
		case p.Type == model.ProcVars:
			process.SetVars(p.Vars, in.Vars)
			return req, true
		case p.Type == model.ProcCounter:
			if _, err := process.Count(p.Counter, in.Vars); err != nil {
				return fail(fmt.Sprintf("%s: %v", st.label, err))
			}
			return req, true
		case strings.HasPrefix(p.Type, "plugin:"):
			out, err := e.runPluginProcessor(ctx, p, "pre", req, nil, in.Vars, res)
			if err != nil {
				return fail(fmt.Sprintf("%s: %v", st.label, err))
			}
			if out != nil {
				req = out
			}
			return req, true
		}
		code = p.Script
	}
	out := rt.Run(ctx, script.Input{
		Phase: script.PreRequest, Name: st.label, Code: code, Request: req,
		Vars: in.Vars, Info: info, Send: e.sendRaw,
	})
	res.absorb(out)
	if out.Request != nil {
		req = out.Request
	}
	if out.Error != "" {
		// Like Postman, a failing pre-request script stops the request.
		res.Sent = req
		res.Error = "not sent: pre-request script failed"
		return req, false
	}
	if out.Skip {
		res.Sent, res.Skipped = req, true
		return req, false
	}
	return req, true
}

// runPost runs one post step against the response.
func (e *Engine) runPost(ctx context.Context, st step, sent *model.Request, resp *check.Response, in SendInput, rt *script.Runtime, info script.Info, res *Result) {
	code := st.code
	if st.proc != nil {
		p := st.proc
		if err := process.ValidateProcessor(*p, false); err != nil {
			res.ScriptErrors = append(res.ScriptErrors, fmt.Sprintf("%s: %v", st.label, err))
			return
		}
		switch {
		case p.Type == model.ProcVars:
			process.SetVars(p.Vars, in.Vars)
			return
		case p.Type == model.ProcExtract:
			res.extract([]model.Extractor{*p.Extract}, resp, in.Vars)
			return
		case p.Type == model.ProcAssert:
			res.Tests = append(res.Tests, check.Assert(resolveAssertions([]model.Assertion{*p.Assert}, in.Vars), resp)...)
			return
		case strings.HasPrefix(p.Type, "plugin:"):
			if _, err := e.runPluginProcessor(ctx, p, "post", sent, resp.Response, in.Vars, res); err != nil {
				res.ScriptErrors = append(res.ScriptErrors, fmt.Sprintf("%s: %v", st.label, err))
			}
			return
		}
		code = p.Script
	}
	res.absorb(rt.Run(ctx, script.Input{
		Phase: script.PostResponse, Name: st.label, Code: code, Request: sent, Response: resp,
		Vars: in.Vars, Info: info, Send: e.sendRaw,
	}))
}

// resolveAssertions fills {{variables}} into assertion paths and expected
// values, e.g. to expect the id an earlier request captured.
func resolveAssertions(as []model.Assertion, v *vars.Set) []model.Assertion {
	if len(as) == 0 || v == nil {
		return as
	}
	r := v.Resolver()
	out := make([]model.Assertion, len(as))
	for i, a := range as {
		a.Path, _ = r.Resolve(a.Path)
		a.Value, _ = r.Resolve(a.Value)
		out[i] = a
	}
	return out
}

// extract runs extractors into the runtime variables and records results.
func (res *Result) extract(ex []model.Extractor, resp *check.Response, v *vars.Set) {
	// Paths and boundaries may use {{variables}} too.
	r := v.Resolver()
	resolved := make([]model.Extractor, len(ex))
	for i, x := range ex {
		x.Path, _ = r.Resolve(x.Path)
		x.Left, _ = r.Resolve(x.Left)
		x.Right, _ = r.Resolve(x.Right)
		resolved[i] = x
	}
	vals, problems := check.Extract(resolved, resp)
	for k, val := range vals {
		v.Runtime[k] = val
		if res.Extracted == nil {
			res.Extracted = map[string]string{}
		}
		res.Extracted[k] = val
	}
	res.Problems = append(res.Problems, problems...)
}

// runPluginProcessor calls a processor plugin. Its options are resolved
// first. It sets the variables the plugin returns and, before the request,
// returns the plugin's rewritten request (nil when unchanged).
func (e *Engine) runPluginProcessor(ctx context.Context, p *model.Processor, phase string, req *model.Request, resp *protocol.Response, v *vars.Set, res *Result) (*model.Request, error) {
	if e.Plugins == nil {
		return nil, fmt.Errorf("%s needs plugins, which aren't available here", p.Type)
	}
	id := strings.TrimPrefix(p.Type, "plugin:")
	r := v.Resolver()
	opts := make(map[string]string, len(p.Options))
	for k, val := range p.Options {
		opts[k], _ = r.Resolve(val)
	}
	out, err := e.Plugins.Process(ctx, id, plugin.ProcessInput{Phase: phase, Request: req, Response: resp, Vars: allVars(v), Options: opts})
	res.addPluginLogs(out.Logs)
	if err != nil {
		return nil, err
	}
	for k, val := range out.Vars {
		v.Runtime[k] = val
	}
	res.Tests = append(res.Tests, out.Tests...)
	return out.Request, nil
}

// allVars flattens every scope as a script would see it.
func allVars(v *vars.Set) map[string]string {
	all := map[string]string{}
	r := v.Resolver()
	scopes := []map[string]string{v.Globals, v.Collection, v.Environment, v.Data, v.Runtime}
	for _, f := range v.Folders {
		scopes = append(scopes, f)
	}
	for _, s := range scopes {
		for k := range s {
			if !strings.HasPrefix(k, "__") {
				all[k], _, _ = r.Lookup(k)
			}
		}
	}
	return all
}

// ResolveRequest returns a deep copy of req with every placeholder resolved.
func ResolveRequest(req *model.Request, r *vars.Resolver) (*model.Request, []string) {
	var missing []string
	seen := map[string]bool{}
	res := func(s string) string {
		out, m := r.Resolve(s)
		for _, name := range m {
			if !seen[name] {
				seen[name] = true
				missing = append(missing, name)
			}
		}
		return out
	}
	kvs := func(in []model.KeyValue) []model.KeyValue {
		if in == nil {
			return nil
		}
		out := make([]model.KeyValue, len(in))
		for i, kv := range in {
			out[i] = kv
			if !kv.Disabled {
				out[i].Key, out[i].Value = res(kv.Key), res(kv.Value)
			}
		}
		return out
	}

	out := *req
	out.URL = res(req.URL)
	out.Query = kvs(req.Query)
	out.Headers = kvs(req.Headers)
	if req.Body != nil {
		b := *req.Body
		b.Content, b.File, b.Variables = res(b.Content), res(b.File), res(b.Variables)
		b.Fields = kvs(b.Fields)
		out.Body = &b
	}
	if req.Auth != nil {
		out.Auth = resolveAuthWith(req.Auth, res)
	}
	if req.Options != nil {
		out.Options = make(map[string]string, len(req.Options))
		for k, v := range req.Options {
			out.Options[k] = res(v)
		}
	}
	return &out, missing
}

func resolveAuth(a *model.Auth, r *vars.Resolver, missing *[]string) *model.Auth {
	if a == nil {
		return nil
	}
	return resolveAuthWith(a, func(s string) string {
		out, m := r.Resolve(s)
		for _, name := range m {
			if !contains(*missing, name) {
				*missing = append(*missing, name)
			}
		}
		return out
	})
}

func resolveAuthWith(a *model.Auth, res func(string) string) *model.Auth {
	out := &model.Auth{Type: a.Type}
	if a.Params != nil {
		out.Params = make(map[string]string, len(a.Params))
		for k, v := range a.Params {
			out.Params[k] = res(v)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// EffectiveAuth returns the auth a send would use (own or inherited) with
// variables resolved, and the names it couldn't resolve.
func EffectiveAuth(in SendInput) (*model.Auth, []string) {
	if in.Vars == nil {
		in.Vars = NewVars(nil, in.Chain)
	}
	var missing []string
	return resolveAuth(auth.Effective(in.Request.Auth, in.Chain), in.Vars.Resolver(), &missing), missing
}

// answerDigest re-sends a request once with digest credentials when the
// server answered 401 with a Digest challenge (or said the cached one is
// stale).
func (e *Engine) answerDigest(ctx context.Context, sent *model.Request, a *model.Auth, resp *protocol.Response, res *Result) (*protocol.Response, error) {
	if resp.Status != 401 {
		return resp, nil
	}
	var c *auth.DigestChallenge
	for _, h := range resp.Headers {
		if strings.EqualFold(h.Key, "WWW-Authenticate") {
			if ch, ok := auth.ParseDigest(h.Value); ok {
				c = ch
				break
			}
		}
	}
	if c == nil {
		return resp, nil
	}
	u, err := httpdriver.TargetURL(sent)
	if err != nil {
		return resp, nil
	}
	retry := *sent
	retry.Headers = withoutHeader(sent.Headers, "Authorization")
	setHeader(&retry, "Authorization", e.digests().Answer(c, methodOf(sent), u.String(), a.Params["username"], a.Params["password"]))
	res.Sent = &retry
	return e.execute(ctx, &retry, res)
}

func methodOf(r *model.Request) string {
	if r.Method == "" {
		return "GET"
	}
	return strings.ToUpper(r.Method)
}

func setHeader(r *model.Request, k, v string) {
	r.Headers = append(withoutHeader(r.Headers, k), model.KeyValue{Key: k, Value: v})
}

func withoutHeader(in []model.KeyValue, k string) []model.KeyValue {
	out := make([]model.KeyValue, 0, len(in))
	for _, h := range in {
		if !strings.EqualFold(h.Key, k) {
			out = append(out, h)
		}
	}
	return out
}
