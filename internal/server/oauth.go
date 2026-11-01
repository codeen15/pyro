package server

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"time"

	"github.com/codeen15/pyro/pkg/engine"
	"github.com/codeen15/pyro/pkg/engine/auth"
	"github.com/codeen15/pyro/pkg/engine/model"
)

var timeNow = time.Now

type oauthBody struct {
	Path        string         `json:"path,omitempty"`
	Request     *model.Request `json:"request,omitempty"`
	Environment string         `json:"environment,omitempty"`
	// Fetch requests a new token (non-interactive grants); otherwise only
	// the cached one is reported.
	Fetch bool `json:"fetch,omitempty"`
	// Clear forgets the cached token.
	Clear bool `json:"clear,omitempty"`
}

type oauthView struct {
	// OAuth is false when the request's auth (own or inherited) isn't
	// OAuth 2.0; nothing else is set then.
	OAuth      bool        `json:"oauth"`
	Grant      string      `json:"grant,omitempty"`
	Token      *auth.Token `json:"token,omitempty"`
	Expired    bool        `json:"expired,omitempty"`
	Unresolved []string    `json:"unresolved,omitempty"`
	// Interactive grants need Get new access token (browser sign-in).
	Interactive bool   `json:"interactive,omitempty"`
	Inherited   bool   `json:"inherited,omitempty"`
	RedirectURI string `json:"redirectUri"`
}

// oauthParams resolves the OAuth 2.0 settings a request would use.
func (s *Server) oauthParams(w http.ResponseWriter, r *http.Request, body oauthBody, required bool) (map[string]string, oauthView, bool) {
	in, slug, ok := s.prepare(w, body.Path, body.Request, body.Environment)
	if !ok {
		return nil, oauthView{}, false
	}
	s.session.apply(in.Vars, slug) // current values, as a send would use
	a, missing := engine.EffectiveAuth(in)
	if a == nil || a.Type != "oauth2" {
		if required {
			writeError(w, http.StatusBadRequest, "this request doesn't use OAuth 2.0 auth")
		} else {
			writeJSON(w, http.StatusOK, oauthView{})
		}
		return nil, oauthView{}, false
	}
	p := a.Params
	if p == nil {
		p = map[string]string{}
	}
	if p["redirect_uri"] == "" {
		p["redirect_uri"] = s.callbackURL(r)
	}
	g := p["grant_type"]
	if g == "" {
		g = auth.GrantAuthCode
	}
	v := oauthView{OAuth: true, Grant: g, Unresolved: missing, Interactive: g == auth.GrantAuthCode || g == auth.GrantAuthCodePKCE,
		Inherited: in.Request.Auth == nil, RedirectURI: p["redirect_uri"]}
	return p, v, true
}

func (s *Server) callbackURL(r *http.Request) string {
	return "http://" + r.Host + "/oauth2/callback"
}

func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request) {
	var body oauthBody
	if !decode(w, r, &body) {
		return
	}
	p, v, ok := s.oauthParams(w, r, body, false)
	if !ok {
		return
	}
	o := s.cfg.Engine.OAuth
	switch {
	case body.Clear:
		o.Forget(p)
	case body.Fetch && !v.Interactive:
		tok, err := o.Fetch(r.Context(), p)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		v.Token = tok
	default:
		v.Token = o.Cached(p)
	}
	if v.Token != nil && !v.Token.Expiry.IsZero() && v.Token.Expiry.Before(timeNow()) {
		v.Expired = true
	}
	writeJSON(w, http.StatusOK, v)
}

// oauthAuthorize starts a browser sign-in and returns the URL to open.
func (s *Server) oauthAuthorize(w http.ResponseWriter, r *http.Request) {
	var body oauthBody
	if !decode(w, r, &body) {
		return
	}
	p, v, ok := s.oauthParams(w, r, body, true)
	if !ok {
		return
	}
	u, err := s.cfg.Engine.OAuth.AuthorizeURL(p, v.RedirectURI)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": u, "redirectUri": v.RedirectURI})
}

// oauthCallback receives the identity provider's redirect. It needs no
// session token (the browser comes from the provider); the random state
// ties it to a sign-in this server started.
func (s *Server) oauthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page := func(status int, title, msg string) {
		w.WriteHeader(status)
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Pyro sign-in</title>
<body style="font:15px system-ui;background:#0a0d10;color:#e7e9ec;display:grid;place-items:center;height:100vh;margin:0">
<div style="max-width:32rem"><h1 style="font-size:18px">%s</h1><p>%s</p></div>
<script>try{window.opener&&window.opener.postMessage({pyroOAuth:%q},location.origin)}catch(e){}</script>`,
			html.EscapeString(title), html.EscapeString(msg), map[bool]string{true: "ok", false: "error"}[status == http.StatusOK])
	}
	if e := q.Get("error"); e != "" {
		page(http.StatusBadRequest, "Sign-in failed", e+" "+q.Get("error_description"))
		return
	}
	if _, err := s.cfg.Engine.OAuth.Complete(r.Context(), q.Get("state"), q.Get("code")); err != nil {
		var msg = err.Error()
		if errors.Is(err, auth.ErrNeedsAuthorization) {
			msg = "Start the sign-in again from Pyro."
		}
		page(http.StatusBadRequest, "Sign-in failed", msg)
		return
	}
	page(http.StatusOK, "Signed in", "Pyro has the access token. You can close this window.")
}
