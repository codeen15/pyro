// Package engine runs requests through Pyro's shared pipeline:
// resolve variables → apply auth → execute via the protocol driver.
// Scripts, assertions and extractors join the pipeline in Phase 2.
package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/pyrohq/pyro/pkg/engine/auth"
	"github.com/pyrohq/pyro/pkg/engine/model"
	"github.com/pyrohq/pyro/pkg/engine/protocol"
	"github.com/pyrohq/pyro/pkg/engine/protocol/httpdriver"
	"github.com/pyrohq/pyro/pkg/engine/vars"
)

type Engine struct {
	HTTP *httpdriver.Driver
}

func New(http *httpdriver.Driver) *Engine {
	return &Engine{HTTP: http}
}

// SendInput is everything needed to send one request.
type SendInput struct {
	Request *model.Request
	// Chain holds collection then folder settings, outermost first.
	Chain       []*model.Collection
	Environment *model.Environment
	// Runtime variables take precedence over every other scope.
	Runtime map[string]string
}

// Result describes one send. Error is set when no response was received;
// HTTP error statuses are still successful sends.
type Result struct {
	Sent       *model.Request     `json:"sent"`
	Response   *protocol.Response `json:"response,omitempty"`
	Unresolved []string           `json:"unresolved,omitempty"`
	Error      string             `json:"error,omitempty"`
	StartedAt  time.Time          `json:"startedAt"`
}

// Send runs the pipeline. It never mutates in.Request.
func (e *Engine) Send(ctx context.Context, in SendInput) *Result {
	res := &Result{StartedAt: time.Now()}
	if in.Request == nil {
		res.Error = "no request"
		return res
	}
	if k := in.Request.Kind; k != "" && k != model.KindHTTP {
		res.Error = fmt.Sprintf("%s requests are opened as sessions, not sent", k)
		return res
	}

	r := NewResolver(in)
	sent, unresolved := ResolveRequest(in.Request, r)
	if err := auth.Apply(sent, resolveAuth(auth.Effective(in.Request.Auth, in.Chain), r, &unresolved)); err != nil {
		res.Sent, res.Unresolved, res.Error = sent, unresolved, err.Error()
		return res
	}
	sent.Auth = nil // applied as headers/query; keep the record unambiguous
	res.Sent, res.Unresolved = sent, unresolved

	resp, err := e.HTTP.Execute(ctx, sent)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Response = resp
	return res
}

// NewResolver layers scopes narrowest first: runtime, environment, folders
// (innermost first), collection.
func NewResolver(in SendInput) *vars.Resolver {
	scopes := []vars.Scope{{Name: "runtime", Vars: in.Runtime}}
	if in.Environment != nil {
		scopes = append(scopes, vars.FromVariables("environment", in.Environment.Variables))
	}
	for i := len(in.Chain) - 1; i >= 0; i-- {
		if in.Chain[i] == nil {
			continue
		}
		name := "folder"
		if i == 0 {
			name = "collection"
		}
		scopes = append(scopes, vars.FromVariables(name, in.Chain[i].Variables))
	}
	return vars.New(scopes...)
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
