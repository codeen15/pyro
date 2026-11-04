package team

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func bearerDo(t *testing.T, base, method, path, token, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func TestBearerTokensForAPIClients(t *testing.T) {
	ts, _ := newTeam(t, Config{})
	me := signup(t, newClient(t, ts), "api@example.com")
	pid := firstProject(t, me)

	// Email and password for tokens; no cookies, no CSRF header.
	code, tok := bearerDo(t, ts.URL, "POST", "/api/v1/auth/token", "", `{"email":"api@example.com","password":"correct horse battery"}`)
	if code != 200 || tok["tokenType"] != "Bearer" || tok["accessToken"] == "" || tok["refreshToken"] == "" {
		t.Fatalf("token: %d %v", code, tok)
	}
	access, refresh := tok["accessToken"].(string), tok["refreshToken"].(string)
	if n := tok["expiresIn"].(float64); n < 800 || n > 900 {
		t.Fatalf("expiresIn %v", n)
	}
	if code, _ := bearerDo(t, ts.URL, "POST", "/api/v1/auth/token", "", `{"email":"api@example.com","password":"wrong password!!"}`); code != 400 {
		t.Fatalf("wrong password: %d", code)
	}

	// The access token is a JWT naming the user.
	parts := strings.Split(access, ".")
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims map[string]any
	_ = json.Unmarshal(payload, &claims)
	if claims["sub"] != me["user"].(map[string]any)["id"] || claims["iss"] != "pyro" {
		t.Fatalf("claims %v", claims)
	}

	// Bearer requests work, writes included, without the CSRF header.
	if code, out := bearerDo(t, ts.URL, "GET", "/api/v1/me", access, ""); code != 200 || out["user"] == nil {
		t.Fatalf("me with bearer: %d", code)
	}
	if code, out := bearerDo(t, ts.URL, "POST", "/api/v1/projects/"+pid+"/collections", access, `{"slug":"api","name":"API"}`); code != 201 {
		t.Fatalf("write with bearer: %d %v", code, out)
	}

	// Refresh gives a new access token; signing out ends the refresh token.
	code, again := bearerDo(t, ts.URL, "POST", "/api/v1/auth/refresh", "", `{"refreshToken":"`+refresh+`"}`)
	if code != 200 || again["accessToken"] == "" || again["refreshToken"] != nil {
		t.Fatalf("refresh: %d %v", code, again)
	}
	if code, _ := bearerDo(t, ts.URL, "POST", "/api/v1/auth/logout", access, `{"refreshToken":"`+refresh+`"}`); code != 204 {
		t.Fatalf("logout: %d", code)
	}
	if code, _ := bearerDo(t, ts.URL, "POST", "/api/v1/auth/refresh", "", `{"refreshToken":"`+refresh+`"}`); code != 401 {
		t.Fatalf("refresh after logout: %d", code)
	}
}

func TestForgedAndExpiredTokensAreRefused(t *testing.T) {
	secret := []byte("test-secret-test-secret-test-secret!")
	ts, st := newTeam(t, Config{JWTSecret: secret})
	signup(t, newClient(t, ts), "a@example.com")
	u, _ := st.UserByEmail(t.Context(), "a@example.com")
	good, _, _ := (&issuer{secret: secret, ttl: time.Minute}).mint(u)
	if code, _ := bearerDo(t, ts.URL, "GET", "/api/v1/me", good, ""); code != 200 {
		t.Fatalf("good token: %d", code)
	}

	parts := strings.Split(good, ".")
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"`+u.ID+`","iss":"pyro","admin":true,"exp":9999999999,"iat":1}`)) + "." + parts[2]
	wrongKey, _, _ := (&issuer{secret: []byte("another-secret-another-secret-12345"), ttl: time.Minute}).mint(u)
	expired, _, _ := (&issuer{secret: secret, ttl: -2 * time.Minute}).mint(u)
	none, _ := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"sub": u.ID, "iss": "pyro", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix()}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	otherIssuer, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": u.ID, "iss": "someone", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix()}).SignedString(secret)
	for name, tok := range map[string]string{"tampered": tampered, "wrong key": wrongKey, "expired": expired, "alg none": none, "other issuer": otherIssuer, "garbage": "abc.def.ghi"} {
		if code, _ := bearerDo(t, ts.URL, "GET", "/api/v1/me", tok, ""); code != 401 {
			t.Errorf("%s token: %d, want 401", name, code)
		}
	}
}

func TestBrowserCookiesRefreshAndRevoke(t *testing.T) {
	ts, _ := newTeam(t, Config{})
	alice := newClient(t, ts)
	signup(t, alice, "alice@example.com")
	u, _ := url.Parse(ts.URL)
	names := map[string]bool{}
	for _, c := range alice.c.Jar.Cookies(u) {
		names[c.Name] = true
	}
	if !names[AccessCookie] || !names[RefreshCookie] {
		t.Fatalf("cookies after sign-up: %v", names)
	}

	// A stale access cookie is replaced from the refresh cookie, silently.
	alice.c.Jar.SetCookies(u, []*http.Cookie{{Name: AccessCookie, Value: "expired.or.garbage", Path: "/"}})
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/me", nil)
	res, err := alice.c.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("me with stale access cookie: %v %v", err, res.StatusCode)
	}
	res.Body.Close()
	renewed := false
	for _, c := range res.Cookies() {
		renewed = renewed || (c.Name == AccessCookie && c.Value != "" && c.Value != "expired.or.garbage")
	}
	if !renewed {
		t.Fatal("no new access cookie")
	}

	// An API token issued before a password change stops working; the browser
	// that changed it stays signed in.
	_, tok := bearerDo(t, ts.URL, "POST", "/api/v1/auth/token", "", `{"email":"alice@example.com","password":"correct horse battery"}`)
	old := tok["accessToken"].(string)
	if code, _ := alice.do("POST", "/api/v1/me/password", `{"current":"correct horse battery","password":"even better horse battery"}`); code != 204 {
		t.Fatalf("change password: %d", code)
	}
	if code, _ := bearerDo(t, ts.URL, "GET", "/api/v1/me", old, ""); code != 401 {
		t.Fatalf("old access token after password change: %d", code)
	}
	if code, _ := alice.do("GET", "/api/v1/me", ""); code != 200 {
		t.Fatalf("browser after its own password change: %d", code)
	}

	// Sign out everywhere ends this browser too.
	if code, _ := alice.do("POST", "/api/v1/auth/logout?everywhere=1", ""); code != 204 {
		t.Fatalf("logout everywhere: %d", code)
	}
	if code, _ := alice.do("GET", "/api/v1/me", ""); code != 401 {
		t.Fatalf("after logout everywhere: %d", code)
	}
}

func TestLoadSecret(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadSecret(dir)
	if err != nil || len(a) < minSecret {
		t.Fatalf("generated: %q %v", a, err)
	}
	if st, _ := os.Stat(filepath.Join(dir, "jwt.secret")); st.Mode().Perm() != 0o600 {
		t.Errorf("jwt.secret mode %v", st.Mode().Perm())
	}
	b, _ := LoadSecret(dir)
	if string(a) != string(b) {
		t.Fatal("secret changed between starts")
	}
	t.Setenv("PYRO_JWT_SECRET", "short")
	if _, err := LoadSecret(dir); err == nil {
		t.Fatal("short secret accepted")
	}
	t.Setenv("PYRO_JWT_SECRET", strings.Repeat("k", 40))
	if c, _ := LoadSecret(dir); string(c) != strings.Repeat("k", 40) {
		t.Fatal("env secret ignored")
	}
}
