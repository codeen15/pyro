package team

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// fakeIdP is a minimal OpenID Connect provider: discovery, keys, an
// /authorize that approves at once, and a /token that checks PKCE and signs
// an ID token with whatever claims the test sets.
type fakeIdP struct {
	*httptest.Server
	key    *rsa.PrivateKey
	mu     sync.Mutex
	claims map[string]any
	codes  map[string]authReq
	// badNonce makes the ID token carry the wrong nonce.
	badNonce bool
}

type authReq struct{ nonce, challenge, redirect string }

func newIdP(t *testing.T) *fakeIdP {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	p := &fakeIdP{key: key, codes: map[string]authReq{}}
	mux := http.NewServeMux()
	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": p.URL, "authorization_endpoint": p.URL + "/authorize", "token_endpoint": p.URL + "/token",
			"jwks_uri": p.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
			http.Error(w, "PKCE required", 400)
			return
		}
		code := randomToken()
		p.mu.Lock()
		p.codes[code] = authReq{q.Get("nonce"), q.Get("code_challenge"), q.Get("redirect_uri")}
		p.mu.Unlock()
		back, _ := url.Parse(q.Get("redirect_uri"))
		bq := back.Query()
		bq.Set("code", code)
		bq.Set("state", q.Get("state"))
		back.RawQuery = bq.Encode()
		http.Redirect(w, r, back.String(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = r.ParseForm()
		p.mu.Lock()
		req, ok := p.codes[r.Form.Get("code")]
		delete(p.codes, r.Form.Get("code"))
		claims := map[string]any{}
		for k, v := range p.claims {
			claims[k] = v
		}
		bad := p.badNonce
		p.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != req.challenge || r.Form.Get("redirect_uri") != req.redirect {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		claims["iss"], claims["aud"], claims["nonce"] = p.URL, "pyro-client", req.nonce
		if bad {
			claims["nonce"] = "something-else"
		}
		claims["iat"], claims["exp"] = time.Now().Unix(), time.Now().Add(time.Hour).Unix()
		signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: p.key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
		payload, _ := json.Marshal(claims)
		obj, _ := signer.Sign(payload)
		idt, _ := obj.CompactSerialize()
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": idt})
	})
	return p
}

func (p *fakeIdP) set(claims map[string]any) {
	p.mu.Lock()
	p.claims = claims
	p.mu.Unlock()
}

// ssoSignIn runs the browser flow and returns where it ended up (the last
// redirect's Location before it left the team server).
func ssoSignIn(t *testing.T, c *client, path string) *url.URL {
	t.Helper()
	var last *url.URL
	c.c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		last = req.URL
		if req.URL.Host == strings.TrimPrefix(c.ts.URL, "http://") && !strings.HasPrefix(req.URL.Path, "/auth/oidc/") {
			return http.ErrUseLastResponse // back on Pyro's pages: stop
		}
		return nil
	}
	res, err := c.c.Get(c.ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	c.c.CheckRedirect = nil
	if res.StatusCode == http.StatusFound {
		last, _ = url.Parse(res.Header.Get("Location"))
	}
	return last
}

func TestOIDCSignIn(t *testing.T) {
	idp := newIdP(t)
	prov := OIDCProvider{ID: "corp", Name: "Corp SSO", Issuer: idp.URL, ClientID: "pyro-client", ClientSecret: "s", AllowedDomains: []string{"example.com"}, AdminGroup: "pyro-admins"}
	if err := prov.Validate(); err != nil {
		t.Fatal(err)
	}
	ts, st := newTeam(t, Config{OIDC: []OIDCProvider{prov}})
	owner := newClient(t, ts)
	signup(t, owner, "owner@example.com")

	// The sign-in page lists the provider.
	_, cfg := newClient(t, ts).do("GET", "/api/v1/auth/config", "")
	if !strings.Contains(toJSON(cfg["providers"]), "Corp SSO") {
		t.Fatalf("providers: %v", cfg["providers"])
	}

	idp.set(map[string]any{"sub": "u-123", "email": "dana@example.com", "email_verified": true, "name": "Dana", "groups": []string{"pyro-admins"}})
	dana := newClient(t, ts)
	if end := ssoSignIn(t, dana, "/auth/oidc/corp/start"); end.Path != "/" || end.Query().Get("signin_error") != "" {
		t.Fatalf("ended at %v", end)
	}
	code, me := dana.do("GET", "/api/v1/me", "")
	if code != 200 {
		t.Fatalf("me after SSO: %d", code)
	}
	u := me["user"].(map[string]any)
	if u["provider"] != "oidc" || u["email"] != "dana@example.com" || u["name"] != "Dana" || u["admin"] != true {
		t.Fatalf("sso user: %v", u)
	}

	// Signing in again (by subject) keeps the account, even with a new email.
	idp.set(map[string]any{"sub": "u-123", "email": "dana.k@example.com", "email_verified": true, "name": "Dana K"})
	again := newClient(t, ts)
	ssoSignIn(t, again, "/auth/oidc/corp/start")
	_, me2 := again.do("GET", "/api/v1/me", "")
	u2 := me2["user"].(map[string]any)
	if u2["id"] != u["id"] || u2["email"] != "dana.k@example.com" || u2["admin"] != false {
		t.Fatalf("second sign-in: %v", u2)
	}

	// Other domains are refused.
	idp.set(map[string]any{"sub": "x", "email": "eve@evil.test", "email_verified": true})
	if end := ssoSignIn(t, newClient(t, ts), "/auth/oidc/corp/start"); !strings.Contains(end.Query().Get("signin_error"), "isn't allowed") {
		t.Fatalf("other domain: %v", end)
	}
	// An unverified email can't take over an existing account.
	idp.set(map[string]any{"sub": "y", "email": "owner@example.com", "email_verified": false})
	if end := ssoSignIn(t, newClient(t, ts), "/auth/oidc/corp/start"); !strings.Contains(end.Query().Get("signin_error"), "already exists") {
		t.Fatalf("unverified takeover: %v", end)
	}
	// A wrong nonce (replayed token) is refused.
	idp.set(map[string]any{"sub": "z", "email": "zed@example.com", "email_verified": true})
	idp.badNonce = true
	if end := ssoSignIn(t, newClient(t, ts), "/auth/oidc/corp/start"); end.Query().Get("signin_error") == "" {
		t.Fatalf("bad nonce accepted: %v", end)
	}
	idp.badNonce = false
	// A callback without the flow cookie (forged or replayed) is refused.
	if end := ssoSignIn(t, newClient(t, ts), "/auth/oidc/corp/callback?code=x&state=y"); end.Query().Get("signin_error") == "" {
		t.Fatalf("callback without flow: %v", end)
	}

	// SSO with an invite joins the workspace.
	_, ws := owner.do("POST", "/api/v1/workspaces", `{"name":"QA"}`)
	_, inv := owner.do("POST", "/api/v1/workspaces/"+ws["id"].(string)+"/invites", `{"role":"editor"}`)
	tok := inv["url"].(string)[strings.LastIndex(inv["url"].(string), "/")+1:]
	idp.set(map[string]any{"sub": "u-456", "email": "fay@example.com", "email_verified": true})
	fay := newClient(t, ts)
	ssoSignIn(t, fay, "/auth/oidc/corp/start?invite="+tok)
	_, fm := fay.do("GET", "/api/v1/me", "")
	if !strings.Contains(toJSON(fm["workspaces"]), `"QA"`) {
		t.Fatalf("invite not accepted: %v", fm["workspaces"])
	}
	_ = st
}

func TestOIDCProviderValidation(t *testing.T) {
	t.Setenv("PYRO_OIDC_MY_CORP_SECRET", "from-env")
	p := OIDCProvider{ID: "my-corp", Issuer: "https://idp", ClientID: "c"}
	if err := p.Validate(); err != nil || p.ClientSecret != "from-env" || p.Name != "my-corp" || len(p.Scopes) != 3 {
		t.Fatalf("%+v %v", p, err)
	}
	if err := (&OIDCProvider{ID: "a b", Issuer: "x", ClientID: "c"}).Validate(); err == nil {
		t.Fatal("bad id accepted")
	}
}
