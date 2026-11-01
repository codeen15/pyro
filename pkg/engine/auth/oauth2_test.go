package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// idp is a fake authorization server.
type idp struct {
	*httptest.Server
	grants    atomic.Int64
	refreshes atomic.Int64
	mu        sync.Mutex
	challenge string
	expiresIn int
	formResp  bool
}

func newIDP(t *testing.T) *idp {
	s := &idp{expiresIn: 3600}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		user, pass, basic := r.BasicAuth()
		clientOK := (basic && user == "app" && pass == "s3cret") || (r.Form.Get("client_id") == "app" && (r.Form.Get("client_secret") == "s3cret" || r.Form.Get("code_verifier") != ""))
		fail := func(code string) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			fmt.Fprintf(w, `{"error":%q,"error_description":"nope"}`, code)
		}
		if !clientOK {
			fail("invalid_client")
			return
		}
		switch r.Form.Get("grant_type") {
		case "client_credentials":
			s.grants.Add(1)
		case "password":
			if r.Form.Get("username") != "ada" || r.Form.Get("password") != "pw" {
				fail("invalid_grant")
				return
			}
			s.grants.Add(1)
		case "refresh_token":
			if r.Form.Get("refresh_token") != "r1" {
				fail("invalid_grant")
				return
			}
			s.refreshes.Add(1)
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			s.mu.Lock()
			ok := r.Form.Get("code") == "c0de" && base64.RawURLEncoding.EncodeToString(sum[:]) == s.challenge
			s.mu.Unlock()
			if !ok {
				fail("invalid_grant")
				return
			}
			s.grants.Add(1)
		default:
			fail("unsupported_grant_type")
			return
		}
		tok := fmt.Sprintf("tok-%d-%d", s.grants.Load(), s.refreshes.Load())
		if s.formResp {
			w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
			fmt.Fprintf(w, "access_token=%s&token_type=bearer&scope=repo", tok)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": tok, "token_type": "Bearer", "expires_in": s.expiresIn, "refresh_token": "r1"})
	}))
	t.Cleanup(s.Close)
	return s
}

func TestClientCredentialsFetchOnceAndRefresh(t *testing.T) {
	s := newIDP(t)
	o := NewOAuth2()
	now := time.Now()
	o.Now = func() time.Time { return now }
	p := map[string]string{"grant_type": GrantClientCredentials, "accessTokenUrl": s.URL, "clientId": "app", "clientSecret": "s3cret", "scope": "read"}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tok, err := o.Token(context.Background(), p); err != nil || tok.AccessToken != "tok-1-0" {
				t.Errorf("token %+v %v", tok, err)
			}
		}()
	}
	wg.Wait()
	if s.grants.Load() != 1 {
		t.Fatalf("%d grants for 20 concurrent callers", s.grants.Load())
	}
	// Past expiry: the refresh token is used.
	now = now.Add(time.Hour)
	tok, err := o.Token(context.Background(), p)
	if err != nil || tok.AccessToken != "tok-1-1" || s.refreshes.Load() != 1 || tok.RefreshToken != "r1" {
		t.Fatalf("after expiry %+v %v", tok, err)
	}
	if !UsesTokenFetching(p) || UsesTokenFetching(map[string]string{"accessToken": "x", "accessTokenUrl": s.URL}) {
		t.Error("UsesTokenFetching")
	}
}

func TestPasswordGrantBodyAuthAndErrors(t *testing.T) {
	s := newIDP(t)
	o := NewOAuth2()
	p := map[string]string{"grant_type": GrantPassword, "accessTokenUrl": s.URL, "clientId": "app", "clientSecret": "s3cret", "client_authentication": "body", "username": "ada", "password": "pw"}
	if _, err := o.Token(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	p["password"] = "wrong"
	if _, err := o.Fetch(context.Background(), p); err == nil || !strings.Contains(err.Error(), "invalid_grant nope") {
		t.Errorf("err = %v", err)
	}
	p["clientSecret"] = "bad"
	if _, err := o.Fetch(context.Background(), p); err == nil || !strings.Contains(err.Error(), "invalid_client") {
		t.Errorf("err = %v", err)
	}
	s.formResp = true
	tok, err := o.Fetch(context.Background(), map[string]string{"grant_type": GrantClientCredentials, "accessTokenUrl": s.URL, "clientId": "app", "clientSecret": "s3cret"})
	if err != nil || tok.Scope != "repo" || !tok.Expiry.IsZero() {
		t.Errorf("form response %+v %v", tok, err)
	}
}

func TestAuthorizationCodeWithPKCE(t *testing.T) {
	s := newIDP(t)
	o := NewOAuth2()
	p := map[string]string{"grant_type": GrantAuthCodePKCE, "authUrl": "https://idp.test/authorize?prompt=login", "accessTokenUrl": s.URL, "clientId": "app", "scope": "openid"}
	if _, err := o.Token(context.Background(), p); !errors.Is(err, ErrNeedsAuthorization) {
		t.Fatalf("before sign-in: %v", err)
	}
	raw, err := o.AuthorizeURL(p, "http://127.0.0.1:7878/oauth2/callback")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	q := u.Query()
	if q.Get("response_type") != "code" || q.Get("client_id") != "app" || q.Get("code_challenge_method") != "S256" || q.Get("prompt") != "login" || q.Get("redirect_uri") == "" {
		t.Fatalf("authorize URL %s", raw)
	}
	s.mu.Lock()
	s.challenge = q.Get("code_challenge")
	s.mu.Unlock()
	if _, err := o.Complete(context.Background(), "forged", "c0de"); err == nil {
		t.Error("unknown state accepted")
	}
	tok, err := o.Complete(context.Background(), q.Get("state"), "c0de")
	if err != nil || tok.AccessToken == "" {
		t.Fatalf("complete: %+v %v", tok, err)
	}
	if got, err := o.Token(context.Background(), p); err != nil || got.AccessToken != tok.AccessToken {
		t.Errorf("cached after sign-in: %+v %v", got, err)
	}
	if _, err := o.Complete(context.Background(), q.Get("state"), "c0de"); err == nil {
		t.Error("state reused")
	}
	if len(o.PendingStates()) != 0 {
		t.Error("pending state left behind")
	}
}
