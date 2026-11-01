// Package engine runs requests through Pyro's shared pipeline:
//
//	pre-request scripts (collection → folders → request) → resolve variables
//	→ apply auth → execute → post-response scripts (same order)
//	→ assertions → extractors
package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/codeen15/pyro/pkg/engine/auth"
	"github.com/codeen15/pyro/pkg/engine/check"
	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/plugin"
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

	// Pre-request scripts may rewrite the request before it is resolved.
	for _, s := range scriptsFor(in, true) {
		out := rt.Run(ctx, script.Input{
			Phase: script.PreRequest, Name: s.name, Code: s.code, Request: req,
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
			return res
		}
		if out.Skip {
			res.Sent, res.Skipped = req, true
			return res
		}
	}

	r := in.Vars.Resolver()
	sent, unresolved := ResolveRequest(req, r)
	if err := e.applyAuth(ctx, sent, resolveAuth(auth.Effective(req.Auth, in.Chain), r, &unresolved), res); err != nil {
		res.Sent, res.Unresolved, res.Error = sent, unresolved, err.Error()
		return res
	}
	sent.Auth = nil // applied as headers/query; keep the record unambiguous
	sent.Scripts = nil
	res.Sent, res.Unresolved = sent, unresolved

	resp, err := e.execute(ctx, sent, res)
	if err != nil {
		res.Error = err.Error()
		res.Tests = append(res.Tests, check.Assert(req.Assertions, nil)...)
		return res
	}
	res.Response = resp
	wrapped := check.Wrap(resp)

	for _, s := range scriptsFor(in, false) {
		res.absorb(rt.Run(ctx, script.Input{
			Phase: script.PostResponse, Name: s.name, Code: s.code, Request: sent, Response: wrapped,
			Vars: in.Vars, Info: info, Send: e.sendRaw,
		}))
	}

	res.Tests = append(res.Tests, check.Assert(req.Assertions, wrapped)...)
	if len(req.Extract) > 0 {
		vals, problems := check.Extract(req.Extract, wrapped)
		for k, v := range vals {
			in.Vars.Runtime[k] = v
		}
		res.Extracted, res.Problems = vals, problems
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

type namedScript struct{ name, code string }

func scriptsFor(in SendInput, pre bool) []namedScript {
	var out []namedScript
	pick := func(s *model.Scripts) string {
		if s == nil {
			return ""
		}
		if pre {
			return s.Pre
		}
		return s.Post
	}
	for i, c := range in.Chain {
		if c == nil {
			continue
		}
		if code := pick(c.Scripts); code != "" {
			name := "folder"
			if i == 0 {
				name = "collection"
			}
			out = append(out, namedScript{name, code})
		}
	}
	if code := pick(in.Request.Scripts); code != "" {
		out = append(out, namedScript{"request", code})
	}
	return out
}

func hasScripts(in SendInput) bool {
	return len(scriptsFor(in, true)) > 0 || len(scriptsFor(in, false)) > 0
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
