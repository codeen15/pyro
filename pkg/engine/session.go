package engine

import (
	"context"

	"github.com/codeen15/pyro/pkg/engine/auth"
	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/protocol"
	"github.com/codeen15/pyro/pkg/engine/protocol/sioclient"
	"github.com/codeen15/pyro/pkg/engine/protocol/wsdriver"
	"github.com/codeen15/pyro/pkg/engine/vars"
)

// ConnectResult describes an attempted session. Session is nil when Error is set.
type ConnectResult struct {
	Session    protocol.Session `json:"-"`
	Sent       *model.Request   `json:"sent"`
	Unresolved []string         `json:"unresolved,omitempty"`
	Error      string           `json:"error,omitempty"`
}

// Connect opens a WebSocket or Socket.IO session for a request, resolving
// variables and applying inherited auth to the handshake.
func (e *Engine) Connect(ctx context.Context, in SendInput) *ConnectResult {
	res := &ConnectResult{}
	if in.Request == nil {
		res.Error = "no request"
		return res
	}
	if in.Vars == nil {
		in.Vars = NewVars(nil, in.Chain)
	}
	r := in.Vars.Resolver()
	sent, unresolved := ResolveRequest(in.Request, r)
	if err := e.applyAuth(ctx, sent, resolveAuth(auth.Effective(in.Request.Auth, in.Chain), r, &unresolved), nil); err != nil {
		res.Sent, res.Unresolved, res.Error = sent, unresolved, err.Error()
		return res
	}
	sent.Auth, sent.Scripts = nil, nil
	if sent.SocketIO != nil {
		sio := *sent.SocketIO
		sio.AuthJSON, _ = r.Resolve(sio.AuthJSON)
		sent.SocketIO = &sio
	}
	res.Sent, res.Unresolved = sent, unresolved

	s, err := e.dial(ctx, sent)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Session = s
	return res
}

func wsDial(ctx context.Context, sent *model.Request) (protocol.Session, error) {
	return wsdriver.Dial(ctx, sent, nil)
}

func sioDial(ctx context.Context, sent *model.Request) (protocol.Session, error) {
	opts := sioclient.Options{Headers: sent.Headers}
	if c := sent.SocketIO; c != nil {
		opts.Path, opts.Namespace, opts.Auth, opts.Transport = c.Path, c.Namespace, c.AuthJSON, c.Transport
	}
	return sioclient.Dial(ctx, sent.URL, opts)
}

// ResolveOutgoing substitutes variables in a message before it is sent.
func ResolveOutgoing(m protocol.Outgoing, v *vars.Set) (protocol.Outgoing, []string) {
	r := v.Resolver()
	data, missing := r.Resolve(m.Data)
	event, missing2 := r.Resolve(m.Event)
	m.Data, m.Event = data, event
	return m, append(missing, missing2...)
}
