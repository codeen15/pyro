package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pyrohq/pyro/internal/history"
	"github.com/pyrohq/pyro/internal/store"
	"github.com/pyrohq/pyro/pkg/engine"
	"github.com/pyrohq/pyro/pkg/engine/model"
	"github.com/pyrohq/pyro/pkg/engine/protocol/httpdriver"
)

const testToken = "secret"

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	ws, err := store.Init(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.CreateCollection("demo", model.Collection{Name: "Demo"}); err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{
		"index.html":    {Data: []byte("<html>pyro</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	hist, err := history.Open(filepath.Join(t.TempDir(), "pyro.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hist.Close() })
	eng := engine.New(httpdriver.New(httpdriver.Options{}))
	ts := httptest.NewServer(New(Config{Workspace: ws, Engine: eng, History: hist, Token: testToken, UI: ui}).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func do(t *testing.T, ts *httptest.Server, method, path, body string, token bool) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token {
		req.Header.Set(TokenHeader, testToken)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res, string(data)
}

func TestTokenRequired(t *testing.T) {
	ts := newTestServer(t)
	if res, _ := do(t, ts, "GET", "/api/v1/health", "", false); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: status %d", res.StatusCode)
	}
	if res, _ := do(t, ts, "GET", "/api/v1/health", "", true); res.StatusCode != http.StatusOK {
		t.Fatalf("with token: status %d", res.StatusCode)
	}
}

func TestRequestCRUDAndTree(t *testing.T) {
	ts := newTestServer(t)

	res, body := do(t, ts, "PUT", "/api/v1/requests/demo/users/list",
		`{"name":"List users","method":"GET","url":"{{baseUrl}}/users"}`, true)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("put: %d %s", res.StatusCode, body)
	}

	res, body = do(t, ts, "GET", "/api/v1/requests/demo/users/list", "", true)
	var got model.Request
	if err := json.Unmarshal([]byte(body), &got); err != nil || res.StatusCode != 200 {
		t.Fatalf("get: %d %s", res.StatusCode, body)
	}
	if got.Kind != model.KindHTTP || got.Method != "GET" {
		t.Fatalf("got %+v", got)
	}

	_, body = do(t, ts, "GET", "/api/v1/workspace", "", true)
	if !strings.Contains(body, `"path":"demo/users/list"`) {
		t.Fatalf("tree missing request: %s", body)
	}

	if res, _ := do(t, ts, "DELETE", "/api/v1/requests/demo/users/list", "", true); res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	if res, _ := do(t, ts, "GET", "/api/v1/requests/demo/users/list", "", true); res.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete: %d", res.StatusCode)
	}
}

func TestErrorMapping(t *testing.T) {
	ts := newTestServer(t)
	cases := []struct {
		method, path, body string
		want               int
	}{
		{"GET", "/api/v1/requests/demo/..%2f..%2fetc", "", http.StatusBadRequest},
		{"PUT", "/api/v1/requests/nope/x", `{"name":"x"}`, http.StatusNotFound},
		{"PUT", "/api/v1/requests/demo/x", `{"bogus":1}`, http.StatusBadRequest},
		{"POST", "/api/v1/collections", `{"slug":"demo"}`, http.StatusConflict},
	}
	for _, c := range cases {
		if res, body := do(t, ts, c.method, c.path, c.body, true); res.StatusCode != c.want {
			t.Errorf("%s %s: got %d want %d (%s)", c.method, c.path, res.StatusCode, c.want, body)
		}
	}
}

func TestSPAFallback(t *testing.T) {
	ts := newTestServer(t)
	for _, p := range []string{"/", "/requests/demo/x"} {
		res, body := do(t, ts, "GET", p, "", false)
		if res.StatusCode != 200 || body != "<html>pyro</html>" {
			t.Errorf("%s: %d %q", p, res.StatusCode, body)
		}
	}
	res, _ := do(t, ts, "GET", "/assets/app.js", "", false)
	if !strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("asset cache header = %q", res.Header.Get("Cache-Control"))
	}
}

func TestEscapedRequestPath(t *testing.T) {
	ts := newTestServer(t)
	if res, body := do(t, ts, "PUT", "/api/v1/requests/demo/get%20user", `{"name":"Get user"}`, true); res.StatusCode != 200 {
		t.Fatalf("put: %d %s", res.StatusCode, body)
	}
	_, body := do(t, ts, "GET", "/api/v1/workspace", "", true)
	if !strings.Contains(body, `"path":"demo/get user"`) {
		t.Fatalf("expected decoded path in tree: %s", body)
	}
}
