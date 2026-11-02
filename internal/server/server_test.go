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

	"github.com/codeen15/pyro/internal/history"
	"github.com/codeen15/pyro/internal/store"
	"github.com/codeen15/pyro/pkg/engine"
	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/protocol/httpdriver"

	"time"
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

// A DNS-rebinding page reaches 127.0.0.1 but sends its own domain as Host.
func TestRejectsNonLoopbackHost(t *testing.T) {
	ts := newTestServer(t)
	for host, want := range map[string]int{"evil.example:80": 421, "localhost:7878": 200, "127.0.0.1": 200, "[::1]:7878": 200, "app.localhost": 200} {
		req, _ := http.NewRequest("GET", ts.URL+"/api/v1/health", nil)
		req.Host = host
		req.Header.Set(TokenHeader, testToken)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("Host %s: %d, want %d", host, res.StatusCode, want)
		}
	}
	open := httptest.NewServer(New(Config{Workspace: nil, AnyHost: true}).Handler())
	defer open.Close()
	req, _ := http.NewRequest("GET", open.URL+"/api/v1/health", nil)
	req.Host = "pyro.internal:7878"
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 200 {
		t.Errorf("AnyHost: %v %v", res.StatusCode, err)
	}
}

func TestCollectionAndFolderSettings(t *testing.T) {
	ts := newTestServer(t)
	do(t, ts, "PUT", "/api/v1/requests/demo/orders/create", `{"name":"Create","url":"http://x"}`, true)
	_, body := do(t, ts, "GET", "/api/v1/folders/demo/orders", "", true)
	if !strings.Contains(body, `"name":"orders"`) {
		t.Fatalf("folder without folder.yaml: %s", body)
	}
	res, body := do(t, ts, "PUT", "/api/v1/folders/demo/orders", `{"name":"Orders","auth":{"type":"bearer","params":{"token":"{{t}}"}},"variables":[{"key":"v","value":"1"}]}`, true)
	if res.StatusCode != 200 {
		t.Fatalf("put folder: %d %s", res.StatusCode, body)
	}
	_, body = do(t, ts, "GET", "/api/v1/workspace", "", true)
	if !strings.Contains(body, `"name":"Orders"`) {
		t.Fatalf("tree shows the folder's name: %s", body)
	}
	if res, _ := do(t, ts, "GET", "/api/v1/folders/demo", "", true); res.StatusCode != 400 {
		t.Fatalf("collection as folder: %d", res.StatusCode)
	}
	if res, _ := do(t, ts, "PUT", "/api/v1/folders/demo/nope", `{"name":"x"}`, true); res.StatusCode != 404 {
		t.Fatalf("missing folder: %d", res.StatusCode)
	}
}

func TestCreateAndDeleteFoldersAndCollections(t *testing.T) {
	ts := newTestServer(t)
	if res, body := do(t, ts, "POST", "/api/v1/folders/demo/orders", `{"name":"Orders"}`, true); res.StatusCode != 201 {
		t.Fatalf("create folder: %d %s", res.StatusCode, body)
	}
	if res, _ := do(t, ts, "POST", "/api/v1/folders/demo/orders/refunds", `{}`, true); res.StatusCode != 201 {
		t.Fatalf("nested folder: %d", res.StatusCode)
	}
	_, body := do(t, ts, "GET", "/api/v1/workspace", "", true)
	if !strings.Contains(body, `"name":"Orders"`) || !strings.Contains(body, `"path":"demo/orders/refunds"`) {
		t.Fatalf("empty folders show in the tree: %s", body)
	}
	for path, want := range map[string]int{
		"/api/v1/folders/demo/orders": 409, // exists
		"/api/v1/folders/demo/nope/x": 404, // no parent
		"/api/v1/folders/demo":        400, // a collection
		"/api/v1/folders/demo/../etc": 400,
	} {
		if res, _ := do(t, ts, "POST", path, `{}`, true); res.StatusCode != want {
			t.Errorf("POST %s: %d, want %d", path, res.StatusCode, want)
		}
	}
	do(t, ts, "PUT", "/api/v1/requests/demo/orders/refunds/list", `{"name":"List","url":"http://x"}`, true)
	if res, _ := do(t, ts, "DELETE", "/api/v1/requests/demo/orders/refunds/list", "", true); res.StatusCode != 204 {
		t.Fatalf("delete request: %d", res.StatusCode)
	}
	if res, _ := do(t, ts, "DELETE", "/api/v1/folders/demo/orders", "", true); res.StatusCode != 204 {
		t.Fatalf("delete folder: %d", res.StatusCode)
	}
	if res, _ := do(t, ts, "GET", "/api/v1/folders/demo/orders/refunds", "", true); res.StatusCode != 404 {
		t.Fatalf("folder contents should be gone: %d", res.StatusCode)
	}
	if res, _ := do(t, ts, "DELETE", "/api/v1/collections/demo", "", true); res.StatusCode != 204 {
		t.Fatalf("delete collection: %d", res.StatusCode)
	}
	if res, _ := do(t, ts, "DELETE", "/api/v1/collections/demo", "", true); res.StatusCode != 404 {
		t.Fatalf("deleted twice: %d", res.StatusCode)
	}
}

func TestMockServerAPI(t *testing.T) {
	ts := newTestServer(t)
	do(t, ts, "PUT", "/api/v1/collections/demo", `{"name":"Demo","variables":[{"key":"baseUrl","value":"https://api.test/v2"}]}`, true)
	do(t, ts, "PUT", "/api/v1/requests/demo/get-user", `{"name":"Get user","method":"GET","url":"{{baseUrl}}/users/{{id}}","examples":[{"name":"200 OK","status":200,"headers":[{"key":"Content-Type","value":"application/json"}],"body":"{\"id\":7}"}]}`, true)
	res, body := do(t, ts, "POST", "/api/v1/mocks", `{"collection":"demo"}`, true)
	var started struct{ URL string }
	json.Unmarshal([]byte(body), &started)
	if res.StatusCode != 201 || !strings.HasPrefix(started.URL, "http://127.0.0.1:") {
		t.Fatalf("start: %d %s", res.StatusCode, body)
	}
	if res, _ := do(t, ts, "POST", "/api/v1/mocks", `{"collection":"demo"}`, true); res.StatusCode != 409 {
		t.Fatalf("second start: %d", res.StatusCode)
	}
	resp, err := http.Get(started.URL + "/v2/users/42")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(b) != `{"id":7}` {
		t.Fatalf("mocked: %d %s", resp.StatusCode, b)
	}
	// A newly saved example is served without restarting (routes refresh within a second).
	do(t, ts, "PUT", "/api/v1/requests/demo/ping", `{"name":"Ping","url":"{{baseUrl}}/ping","examples":[{"name":"pong","status":200,"body":"pong"}]}`, true)
	time.Sleep(1100 * time.Millisecond)
	if resp, err := http.Get(started.URL + "/v2/ping"); err != nil || resp.StatusCode != 200 {
		t.Fatalf("new example: %v %v", resp, err)
	}
	_, body = do(t, ts, "GET", "/api/v1/mocks/demo", "", true)
	if !strings.Contains(body, `"path":"/v2/users/{id}"`) || !strings.Contains(body, `"example":"200 OK"`) {
		t.Fatalf("mock view: %s", body)
	}
	if res, _ := do(t, ts, "DELETE", "/api/v1/mocks/demo", "", true); res.StatusCode != 204 {
		t.Fatalf("stop: %d", res.StatusCode)
	}
	if _, err := http.Get(started.URL + "/v2/ping"); err == nil {
		t.Fatal("mock still answering after stop")
	}
}
