package server

import (
	"crypto/sha256"
	b64std "encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestOAuth2Flows(t *testing.T) {
	var mu sync.Mutex
	challenge, issued := "", 0
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		mu.Lock()
		defer mu.Unlock()
		switch r.Form.Get("grant_type") {
		case "client_credentials":
			if u, p, _ := r.BasicAuth(); u != "app" || p != "s3cret" {
				w.WriteHeader(401)
				fmt.Fprint(w, `{"error":"invalid_client"}`)
				return
			}
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if r.Form.Get("code") != "c0de" || b64std.RawURLEncoding.EncodeToString(sum[:]) != challenge {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":"invalid_grant"}`)
				return
			}
		}
		issued++
		fmt.Fprintf(w, `{"access_token":"tok%d","token_type":"Bearer","expires_in":3600}`, issued)
	}))
	defer idp.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, r.Header.Get("Authorization")) }))
	defer api.Close()

	ts := newTestServer(t)
	do(t, ts, "PUT", "/api/v1/environments/dev", `{"variables":[{"key":"idp","value":"`+idp.URL+`"},{"key":"secret","value":"s3cret"}]}`, true)
	do(t, ts, "PUT", "/api/v1/requests/demo/cc", `{"name":"CC","url":"`+api.URL+`","auth":{"type":"oauth2","params":{"grant_type":"client_credentials","accessTokenUrl":"{{idp}}","clientId":"app","clientSecret":"{{secret}}"}}}`, true)
	_, body := do(t, ts, "POST", "/api/v1/send", `{"path":"demo/cc","environment":"dev"}`, true)
	if !strings.Contains(body, b64std.StdEncoding.EncodeToString([]byte("Bearer tok1"))) {
		t.Fatalf("client credentials send: %s", body)
	}
	_, body = do(t, ts, "POST", "/api/v1/oauth2/token", `{"path":"demo/cc","environment":"dev"}`, true)
	if !strings.Contains(body, `"accessToken":"tok1"`) || !strings.Contains(body, `"grant":"client_credentials"`) {
		t.Fatalf("cached token: %s", body)
	}
	_, body = do(t, ts, "POST", "/api/v1/oauth2/token", `{"path":"demo/cc","environment":"dev","fetch":true}`, true)
	if !strings.Contains(body, `"accessToken":"tok2"`) {
		t.Fatalf("fetch new: %s", body)
	}

	// Authorization code with PKCE through the browser callback.
	do(t, ts, "PUT", "/api/v1/requests/demo/ac", `{"name":"AC","url":"`+api.URL+`","auth":{"type":"oauth2","params":{"grant_type":"authorization_code_with_pkce","authUrl":"https://idp.test/authorize","accessTokenUrl":"{{idp}}","clientId":"app"}}}`, true)
	_, body = do(t, ts, "POST", "/api/v1/send", `{"path":"demo/ac","environment":"dev"}`, true)
	if !strings.Contains(body, "sign in first") {
		t.Fatalf("before sign-in: %s", body)
	}
	res, body := do(t, ts, "POST", "/api/v1/oauth2/authorize", `{"path":"demo/ac","environment":"dev"}`, true)
	var start struct{ URL, RedirectURI string }
	json.Unmarshal([]byte(body), &start)
	u, err := url.Parse(start.URL)
	if res.StatusCode != 200 || err != nil || !strings.HasSuffix(start.RedirectURI, "/oauth2/callback") {
		t.Fatalf("authorize: %d %s", res.StatusCode, body)
	}
	mu.Lock()
	challenge = u.Query().Get("code_challenge")
	mu.Unlock()
	if res, _ := do(t, ts, "GET", "/oauth2/callback?state=forged&code=c0de", "", false); res.StatusCode != 400 {
		t.Fatalf("forged state: %d", res.StatusCode)
	}
	res, body = do(t, ts, "GET", "/oauth2/callback?state="+u.Query().Get("state")+"&code=c0de", "", false)
	if res.StatusCode != 200 || !strings.Contains(body, "Signed in") {
		t.Fatalf("callback: %d %s", res.StatusCode, body)
	}
	_, body = do(t, ts, "POST", "/api/v1/send", `{"path":"demo/ac","environment":"dev"}`, true)
	if !strings.Contains(body, b64std.StdEncoding.EncodeToString([]byte("Bearer tok3"))) {
		t.Fatalf("after sign-in: %s", body)
	}
	res, body = do(t, ts, "POST", "/api/v1/oauth2/token", `{"request":{"name":"x","url":"http://x"}}`, true)
	if res.StatusCode != 200 || !strings.Contains(body, `"oauth":false`) {
		t.Fatalf("non-OAuth request: %d %s", res.StatusCode, body)
	}
	if res, _ := do(t, ts, "POST", "/api/v1/oauth2/authorize", `{"request":{"name":"x","url":"http://x"}}`, true); res.StatusCode != 400 {
		t.Fatalf("authorize non-OAuth: %d", res.StatusCode)
	}
}
