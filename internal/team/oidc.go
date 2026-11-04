package team

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
)

// OIDCProvider is a single sign-on provider (OpenID Connect): Microsoft
// Entra ID, Google, Keycloak, Okta…
type OIDCProvider struct {
	// ID names the provider in URLs: /auth/oidc/<id>/start and /callback.
	ID string `yaml:"id"`
	// Name is the button label ("Sign in with <Name>").
	Name   string `yaml:"name"`
	Issuer string `yaml:"issuer"`
	// ClientID and ClientSecret come from the app registration. The secret
	// defaults to $PYRO_OIDC_<ID>_SECRET (ID upper-cased, - as _).
	ClientID     string `yaml:"clientID"`
	ClientSecret string `yaml:"clientSecret"`
	// Scopes default to openid, email and profile.
	Scopes []string `yaml:"scopes"`
	// AllowedDomains limits sign-in to these email domains (empty: any).
	AllowedDomains []string `yaml:"allowedDomains"`
	// TrustEmail links a sign-in to an existing account with the same email
	// even when the provider doesn't say the address is verified (Entra ID
	// doesn't send email_verified). Only for providers that control the
	// addresses, like your company's tenant.
	TrustEmail bool `yaml:"trustEmail"`
	// AdminGroup makes members of this group (in the GroupsClaim claim,
	// default "groups") server administrators. Empty: not managed here.
	AdminGroup  string `yaml:"adminGroup"`
	GroupsClaim string `yaml:"groupsClaim"`
}

// ProviderEnvSecret is the variable holding a provider's client secret.
func ProviderEnvSecret(id string) string {
	return "PYRO_OIDC_" + strings.ToUpper(strings.ReplaceAll(id, "-", "_")) + "_SECRET"
}

// Validate checks a provider's settings and fills defaults.
func (p *OIDCProvider) Validate() error {
	if p.ID == "" || strings.ContainsAny(p.ID, "/?#% ") {
		return fmt.Errorf("oidc: provider id %q must be a plain word, e.g. entra", p.ID)
	}
	if p.Issuer == "" || p.ClientID == "" {
		return fmt.Errorf("oidc %s: issuer and clientID are required", p.ID)
	}
	if p.Name == "" {
		p.Name = p.ID
	}
	if p.ClientSecret == "" {
		p.ClientSecret = os.Getenv(ProviderEnvSecret(p.ID))
	}
	if len(p.Scopes) == 0 {
		p.Scopes = []string{oidc.ScopeOpenID, "email", "profile"}
	}
	if p.GroupsClaim == "" {
		p.GroupsClaim = "groups"
	}
	return nil
}

// oidcClients discovers providers lazily (an unreachable provider mustn't
// stop the server from starting) and caches them.
type oidcClients struct {
	mu   sync.Mutex
	byID map[string]*oidc.Provider
}

func (s *Server) oidcProvider(id string) (*OIDCProvider, bool) {
	for i := range s.cfg.OIDC {
		if s.cfg.OIDC[i].ID == id {
			return &s.cfg.OIDC[i], true
		}
	}
	return nil, false
}

func (s *Server) discover(ctx context.Context, p *OIDCProvider) (*oidc.Provider, error) {
	s.oidc.mu.Lock()
	defer s.oidc.mu.Unlock()
	if op := s.oidc.byID[p.ID]; op != nil {
		return op, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	op, err := oidc.NewProvider(ctx, p.Issuer)
	if err != nil {
		return nil, err
	}
	s.oidc.byID[p.ID] = op
	return op, nil
}

func (s *Server) oauthConfig(r *http.Request, p *OIDCProvider, op *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID: p.ClientID, ClientSecret: p.ClientSecret, Endpoint: op.Endpoint(), Scopes: p.Scopes,
		RedirectURL: s.baseURL(r) + "/auth/oidc/" + p.ID + "/callback",
	}
}

// oidcFlow is the sign-in in progress, kept in a short-lived signed cookie.
type oidcFlow struct {
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
	Provider string `json:"provider"`
	Invite   string `json:"invite,omitempty"`
	jwt.RegisteredClaims
}

const flowCookie = "pyro_oidc"

// oidcStart sends the browser to the provider.
func (s *Server) oidcStart(w http.ResponseWriter, r *http.Request) {
	p, ok := s.oidcProvider(chi.URLParam(r, "id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	op, err := s.discover(r.Context(), p)
	if err != nil {
		s.log.Error("OIDC discovery failed", "provider", p.ID, "err", err)
		s.signinError(w, r, p.Name+" couldn't be reached; try again, or tell your Pyro administrator")
		return
	}
	f := oidcFlow{State: randomToken(), Nonce: randomToken(), Verifier: oauth2.GenerateVerifier(), Provider: p.ID, Invite: r.URL.Query().Get("invite"),
		RegisteredClaims: jwt.RegisteredClaims{Issuer: jwtIssuer, Subject: "oidc-flow", ExpiresAt: jwt.NewNumericDate(time.Now().Add(10 * time.Minute))}}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, f).SignedString(s.tokens.secret)
	if err != nil {
		s.fail(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: flowCookie, Value: signed, Path: "/auth/oidc/", HttpOnly: true, Secure: s.secureCookies(r), SameSite: http.SameSiteLaxMode, MaxAge: 600})
	u := s.oauthConfig(r, p, op).AuthCodeURL(f.State, oidc.Nonce(f.Nonce), oauth2.S256ChallengeOption(f.Verifier))
	http.Redirect(w, r, u, http.StatusFound)
}

// oidcCallback finishes sign-in: checks state, exchanges the code (with
// the PKCE verifier), verifies the ID token and nonce, then signs in.
func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	p, ok := s.oidcProvider(chi.URLParam(r, "id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: flowCookie, Value: "", Path: "/auth/oidc/", HttpOnly: true, Secure: s.secureCookies(r), MaxAge: -1})
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		s.signinError(w, r, p.Name+" sign-in was cancelled or refused ("+e+")")
		return
	}
	var f oidcFlow
	c, err := r.Cookie(flowCookie)
	if err == nil {
		_, err = jwt.ParseWithClaims(c.Value, &f, func(*jwt.Token) (any, error) { return s.tokens.secret, nil },
			jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer(jwtIssuer), jwt.WithSubject("oidc-flow"), jwt.WithExpirationRequired())
	}
	if err != nil || f.Provider != p.ID || f.State == "" || q.Get("state") != f.State {
		s.signinError(w, r, "the sign-in took too long or was started elsewhere; try again")
		return
	}
	u, err := s.oidcUser(r, p, q.Get("code"), &f)
	if err != nil {
		var msg string
		if errors.Is(err, ErrInvalid) {
			msg = strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": ")
		} else {
			s.log.Error("OIDC sign-in failed", "provider", p.ID, "err", err)
			msg = p.Name + " sign-in failed; try again, or tell your Pyro administrator"
		}
		s.signinError(w, r, msg)
		return
	}
	if err := s.setSession(w, r, u); err != nil {
		s.fail(w, err)
		return
	}
	next := "/"
	if f.Invite != "" {
		if inv, err := s.st.AcceptInvite(r.Context(), f.Invite, u); err == nil {
			_ = s.st.Record(r.Context(), u.ID, inv.WorkspaceID, "", "joined as "+inv.Role, "")
		} else {
			next = "/invite/" + url.PathEscape(f.Invite)
		}
	}
	http.Redirect(w, r, next, http.StatusFound)
}

func (s *Server) oidcUser(r *http.Request, p *OIDCProvider, code string, f *oidcFlow) (*User, error) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	op, err := s.discover(ctx, p)
	if err != nil {
		return nil, err
	}
	tok, err := s.oauthConfig(r, p, op).Exchange(ctx, code, oauth2.VerifierOption(f.Verifier))
	if err != nil {
		return nil, fmt.Errorf("code exchange: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		return nil, errors.New("no id_token in the token response")
	}
	idt, err := op.Verifier(&oidc.Config{ClientID: p.ClientID}).Verify(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("id token: %w", err)
	}
	if idt.Nonce != f.Nonce {
		return nil, errors.New("id token nonce doesn't match")
	}
	var claims map[string]any
	if err := idt.Claims(&claims); err != nil {
		return nil, err
	}
	str := func(k string) string { v, _ := claims[k].(string); return v }
	email := str("email")
	if email == "" {
		email = str("preferred_username") // Entra ID work accounts
	}
	verified, _ := claims["email_verified"].(bool)
	if len(p.AllowedDomains) > 0 {
		at := strings.LastIndexByte(email, '@')
		okDomain := false
		for _, d := range p.AllowedDomains {
			okDomain = okDomain || (at > 0 && strings.EqualFold(email[at+1:], strings.TrimPrefix(d, "@")))
		}
		if !okDomain {
			return nil, invalid("%s isn't allowed to sign in here (only %s addresses)", email, strings.Join(p.AllowedDomains, ", "))
		}
	}
	name := str("name")
	if name == "" {
		name = strings.TrimSpace(str("given_name") + " " + str("family_name"))
	}
	e := External{Provider: "oidc", Subject: p.Issuer + "|" + idt.Subject, Email: email, Name: name, LinkByEmail: verified || p.TrustEmail}
	if p.AdminGroup != "" {
		admin := false
		if groups, ok := claims[p.GroupsClaim].([]any); ok {
			for _, g := range groups {
				admin = admin || g == p.AdminGroup
			}
		}
		e.Admin = &admin
	}
	return s.st.UpsertExternal(ctx, e)
}

// signinError returns to the sign-in page with a message.
func (s *Server) signinError(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/?signin_error="+url.QueryEscape(msg), http.StatusFound)
}
