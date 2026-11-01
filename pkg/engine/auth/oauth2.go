package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/codeen15/pyro/pkg/engine/model"
)

// OAuth 2.0 grant types, named as in Postman so imported settings work.
const (
	GrantClientCredentials = "client_credentials"
	GrantPassword          = "password_credentials"
	GrantAuthCode          = "authorization_code"
	GrantAuthCodePKCE      = "authorization_code_with_pkce"
)

// Token is an OAuth 2.0 access token.
type Token struct {
	AccessToken  string    `json:"accessToken"`
	TokenType    string    `json:"tokenType,omitempty"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	Expiry       time.Time `json:"expiresAt,omitzero"`
	ObtainedAt   time.Time `json:"obtainedAt"`
}

// expired reports whether t is unusable (with 30 s of slack).
func (t *Token) expired(now time.Time) bool {
	return !t.Expiry.IsZero() && now.Add(30*time.Second).After(t.Expiry)
}

// ErrNeedsAuthorization means the grant needs a browser sign-in first.
var ErrNeedsAuthorization = errors.New("this OAuth 2.0 grant needs you to sign in first: use Get new access token")

// OAuth2 fetches, caches and refreshes tokens. Tokens are cached per
// configuration (grant, token URL, client, scope, user), in memory only.
// Safe for concurrent use; one load test shares one OAuth2 across VUs.
type OAuth2 struct {
	Client *http.Client
	Now    func() time.Time

	mu      sync.Mutex
	tokens  map[string]*Token
	pending map[string]*pendingAuth // by state
	flight  map[string]*call        // fetches in progress, by config key
}

type pendingAuth struct {
	params      map[string]string
	verifier    string
	redirectURI string
	created     time.Time
}

type call struct {
	done chan struct{}
	tok  *Token
	err  error
}

func NewOAuth2() *OAuth2 {
	return &OAuth2{Client: &http.Client{Timeout: 30 * time.Second}, Now: time.Now,
		tokens: map[string]*Token{}, pending: map[string]*pendingAuth{}, flight: map[string]*call{}}
}

// key identifies a token configuration.
func key(p map[string]string) string {
	parts := []string{p["grant_type"], p["accessTokenUrl"], p["clientId"], p["scope"], p["username"], p["audience"]}
	return strings.Join(parts, "\x00")
}

// Cached returns the cached token for a configuration, if any (even expired).
func (o *OAuth2) Cached(p map[string]string) *Token {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.tokens[key(p)]
}

// Forget drops the cached token for a configuration.
func (o *OAuth2) Forget(p map[string]string) {
	o.mu.Lock()
	delete(o.tokens, key(p))
	o.mu.Unlock()
}

// Token returns a valid token for the configuration: cached, refreshed, or
// newly fetched for grants that need no user interaction. Concurrent callers
// with the same configuration share one fetch.
func (o *OAuth2) Token(ctx context.Context, p map[string]string) (*Token, error) {
	k := key(p)
	o.mu.Lock()
	if t := o.tokens[k]; t != nil && !t.expired(o.Now()) {
		o.mu.Unlock()
		return t, nil
	}
	if c := o.flight[k]; c != nil {
		o.mu.Unlock()
		select {
		case <-c.done:
			return c.tok, c.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	old := o.tokens[k]
	c := &call{done: make(chan struct{})}
	o.flight[k] = c
	o.mu.Unlock()

	c.tok, c.err = o.obtain(ctx, p, old)
	o.mu.Lock()
	delete(o.flight, k)
	if c.err == nil {
		o.tokens[k] = c.tok
	}
	o.mu.Unlock()
	close(c.done)
	return c.tok, c.err
}

// Fetch always requests a new token (non-interactive grants), replacing the
// cached one.
func (o *OAuth2) Fetch(ctx context.Context, p map[string]string) (*Token, error) {
	o.Forget(p)
	return o.Token(ctx, p)
}

func (o *OAuth2) obtain(ctx context.Context, p map[string]string, old *Token) (*Token, error) {
	if old != nil && old.RefreshToken != "" {
		form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {old.RefreshToken}}
		tokenURL := p["refreshTokenUrl"]
		if tokenURL == "" {
			tokenURL = p["accessTokenUrl"]
		}
		if t, err := o.request(ctx, tokenURL, p, form); err == nil {
			if t.RefreshToken == "" {
				t.RefreshToken = old.RefreshToken
			}
			return t, nil
		}
		// A rejected refresh falls through to a fresh grant.
	}
	form := url.Values{}
	switch grant(p) {
	case GrantClientCredentials:
		form.Set("grant_type", "client_credentials")
	case GrantPassword:
		form.Set("grant_type", "password")
		form.Set("username", p["username"])
		form.Set("password", p["password"])
	case GrantAuthCode, GrantAuthCodePKCE:
		return nil, ErrNeedsAuthorization
	case "implicit":
		return nil, errors.New("the implicit grant isn't supported (it's deprecated); use authorization code with PKCE")
	default:
		return nil, fmt.Errorf("OAuth 2.0 grant type %q isn't supported", p["grant_type"])
	}
	if s := p["scope"]; s != "" {
		form.Set("scope", s)
	}
	if a := p["audience"]; a != "" {
		form.Set("audience", a)
	}
	return o.request(ctx, p["accessTokenUrl"], p, form)
}

func grant(p map[string]string) string {
	if g := p["grant_type"]; g != "" {
		return g
	}
	return GrantAuthCode // Postman's default
}

// request posts a token request, authenticating the client as configured
// (client_authentication "header" = HTTP basic, the default; "body" = form).
func (o *OAuth2) request(ctx context.Context, tokenURL string, p map[string]string, form url.Values) (*Token, error) {
	if tokenURL == "" {
		return nil, errors.New("OAuth 2.0: the access token URL is empty")
	}
	if p["client_authentication"] == "body" {
		form.Set("client_id", p["clientId"])
		if p["clientSecret"] != "" {
			form.Set("client_secret", p["clientSecret"])
		}
	} else if p["clientSecret"] == "" && p["clientId"] != "" {
		form.Set("client_id", p["clientId"]) // public clients (PKCE)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("OAuth 2.0 token URL: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if p["client_authentication"] != "body" && p["clientSecret"] != "" {
		req.SetBasicAuth(url.QueryEscape(p["clientId"]), url.QueryEscape(p["clientSecret"]))
	}
	resp, err := o.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OAuth 2.0 token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	// JSON is standard; some servers (GitHub) answer form-encoded, and some
	// label JSON as text/plain, so JSON is tried first.
	var raw map[string]any
	if json.Unmarshal(body, &raw) != nil {
		raw = nil
		if vals, err := url.ParseQuery(string(body)); err == nil && (vals.Has("access_token") || vals.Has("error")) {
			raw = map[string]any{}
			for k := range vals {
				raw[k] = vals.Get(k)
			}
		}
	}
	if raw == nil {
		return nil, fmt.Errorf("OAuth 2.0 token response (%s) isn't JSON: %.200s", resp.Status, body)
	}
	if e, _ := raw["error"].(string); e != "" || resp.StatusCode/100 != 2 {
		desc, _ := raw["error_description"].(string)
		if e == "" {
			e = resp.Status
		}
		return nil, fmt.Errorf("OAuth 2.0 token request failed: %s %s", e, desc)
	}
	t := &Token{ObtainedAt: o.Now()}
	t.AccessToken, _ = raw["access_token"].(string)
	t.TokenType, _ = raw["token_type"].(string)
	t.RefreshToken, _ = raw["refresh_token"].(string)
	t.Scope, _ = raw["scope"].(string)
	if t.AccessToken == "" {
		return nil, errors.New("OAuth 2.0 token response has no access_token")
	}
	switch v := raw["expires_in"].(type) {
	case float64:
		t.Expiry = t.ObtainedAt.Add(time.Duration(v) * time.Second)
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			t.Expiry = t.ObtainedAt.Add(time.Duration(n) * time.Second)
		}
	}
	return t, nil
}

// AuthorizeURL starts an authorization-code sign-in: it returns the URL to
// open in a browser and remembers the configuration under a random state
// until Complete is called with the redirect's code.
func (o *OAuth2) AuthorizeURL(p map[string]string, redirectURI string) (string, error) {
	g := grant(p)
	if g != GrantAuthCode && g != GrantAuthCodePKCE {
		return "", fmt.Errorf("grant %q doesn't use a browser sign-in", g)
	}
	if p["authUrl"] == "" {
		return "", errors.New("OAuth 2.0: the auth URL is empty")
	}
	u, err := url.Parse(p["authUrl"])
	if err != nil {
		return "", fmt.Errorf("OAuth 2.0 auth URL: %w", err)
	}
	state := randomString(24)
	pa := &pendingAuth{params: p, redirectURI: redirectURI, created: o.Now()}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", p["clientId"])
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	if s := p["scope"]; s != "" {
		q.Set("scope", s)
	}
	if a := p["audience"]; a != "" {
		q.Set("audience", a)
	}
	if g == GrantAuthCodePKCE {
		pa.verifier = randomString(48)
		if p["challengeAlgorithm"] == "plain" {
			q.Set("code_challenge", pa.verifier)
			q.Set("code_challenge_method", "plain")
		} else {
			sum := sha256.Sum256([]byte(pa.verifier))
			q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
			q.Set("code_challenge_method", "S256")
		}
	}
	u.RawQuery = q.Encode()
	o.mu.Lock()
	for s, old := range o.pending { // sign-ins left unfinished for 10 minutes are dropped
		if o.Now().Sub(old.created) > 10*time.Minute {
			delete(o.pending, s)
		}
	}
	o.pending[state] = pa
	o.mu.Unlock()
	return u.String(), nil
}

// Complete exchanges the code from the redirect for a token and caches it.
func (o *OAuth2) Complete(ctx context.Context, state, code string) (*Token, error) {
	o.mu.Lock()
	pa := o.pending[state]
	delete(o.pending, state)
	o.mu.Unlock()
	if pa == nil {
		return nil, errors.New("unknown or expired sign-in (state); start again with Get new access token")
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {pa.redirectURI}}
	if pa.verifier != "" {
		form.Set("code_verifier", pa.verifier)
	}
	t, err := o.request(ctx, pa.params["accessTokenUrl"], pa.params, form)
	if err != nil {
		return nil, err
	}
	o.mu.Lock()
	o.tokens[key(pa.params)] = t
	o.mu.Unlock()
	return t, nil
}

// PendingStates lists sign-ins in progress (for tests).
func (o *OAuth2) PendingStates() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]string, 0, len(o.pending))
	for s := range o.pending {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)[:n]
}

// UsesTokenFetching reports whether oauth2 params configure a grant (as
// opposed to just a pasted access token).
func UsesTokenFetching(p map[string]string) bool {
	return p["accessToken"] == "" && (p["accessTokenUrl"] != "" || p["authUrl"] != "")
}

// ApplyOAuth2Token adds an access token as configured: addTokenTo
// "queryParams" adds ?access_token=, otherwise an Authorization header with
// headerPrefix (default "Bearer").
func ApplyOAuth2Token(req *model.Request, p map[string]string, token string) {
	if p["addTokenTo"] == "queryParams" {
		if !hasKey(req.Query, "access_token") {
			req.Query = append(req.Query, model.KeyValue{Key: "access_token", Value: token})
		}
		return
	}
	prefix := p["headerPrefix"]
	if prefix == "" {
		prefix = "Bearer"
	}
	setHeader(req, "Authorization", prefix+" "+token)
}
