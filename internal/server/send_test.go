package server

import (
	b64 "encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestSendSavedAndUnsaved(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s %s auth=%s", r.Method, r.URL.Path, r.Header.Get("Authorization"))
	}))
	defer target.Close()
	ts := newTestServer(t)

	do(t, ts, "PUT", "/api/v1/collections/demo",
		`{"name":"Demo","variables":[{"key":"base","value":"`+target.URL+`"}],"auth":{"type":"bearer","params":{"token":"{{tok}}"}}}`, true)
	do(t, ts, "PUT", "/api/v1/environments/dev", `{"variables":[{"key":"tok","value":"T1"}]}`, true)
	do(t, ts, "PUT", "/api/v1/requests/demo/ping", `{"name":"Ping","method":"GET","url":"{{base}}/saved"}`, true)

	var out struct {
		Response struct {
			Status int    `json:"status"`
			Body   []byte `json:"body"`
		} `json:"response"`
		HistoryID int64  `json:"historyId"`
		Error     string `json:"error"`
	}
	res, body := do(t, ts, "POST", "/api/v1/send", `{"path":"demo/ping","environment":"dev"}`, true)
	json.Unmarshal([]byte(body), &out)
	if res.StatusCode != 200 || out.Error != "" || string(out.Response.Body) != "GET /saved auth=Bearer T1" || out.HistoryID == 0 {
		t.Fatalf("saved send: %d %s", res.StatusCode, body)
	}

	// Unsaved edits are sent but inherit from the request's location.
	_, body = do(t, ts, "POST", "/api/v1/send",
		`{"path":"demo/ping","environment":"dev","request":{"method":"POST","url":"{{base}}/edited"}}`, true)
	json.Unmarshal([]byte(body), &out)
	if string(out.Response.Body) != "POST /edited auth=Bearer T1" {
		t.Fatalf("unsaved send: %s", body)
	}

	_, body = do(t, ts, "GET", "/api/v1/history", "", true)
	var hist []map[string]any
	json.Unmarshal([]byte(body), &hist)
	if len(hist) != 2 || hist[0]["method"] != "POST" || hist[1]["path"] != "demo/ping" {
		t.Fatalf("history: %s", body)
	}
	if res, body := do(t, ts, "GET", fmt.Sprintf("/api/v1/history/%v", hist[1]["id"]), "", true); res.StatusCode != 200 || !strings.Contains(body, `"sent"`) {
		t.Fatalf("history entry: %d %s", res.StatusCode, body)
	}
	do(t, ts, "DELETE", "/api/v1/history", "", true)
	if _, body := do(t, ts, "GET", "/api/v1/history", "", true); strings.TrimSpace(body) != "[]" {
		t.Fatalf("after clear: %s", body)
	}
}

func TestSendErrors(t *testing.T) {
	ts := newTestServer(t)
	cases := map[string]int{
		`{}`:                      http.StatusBadRequest,
		`{"path":"demo/missing"}`: http.StatusNotFound,
		`{"path":"nope/x","request":{"url":"x"}}`:   http.StatusNotFound,
		`{"request":{"url":"x"},"environment":"z"}`: http.StatusNotFound,
	}
	for in, want := range cases {
		if res, body := do(t, ts, "POST", "/api/v1/send", in, true); res.StatusCode != want {
			t.Errorf("%s: %d want %d (%s)", in, res.StatusCode, want, body)
		}
	}
	// A transport failure is a successful API call carrying an error.
	_, body := do(t, ts, "POST", "/api/v1/send", `{"request":{"url":"http://127.0.0.1:1"}}`, true)
	if !strings.Contains(body, `"error":"could not connect`) {
		t.Errorf("refused: %s", body)
	}
}

func TestImport(t *testing.T) {
	ts := newTestServer(t)
	data, err := os.ReadFile("../../pkg/engine/importer/postman/testdata/shop.v21.json")
	if err != nil {
		t.Fatal(err)
	}
	res, body := do(t, ts, "POST", "/api/v1/import", string(data), true)
	if res.StatusCode != http.StatusCreated || !strings.Contains(body, `"slug":"shop-api"`) || !strings.Contains(body, `"requests":6`) {
		t.Fatalf("import: %d %s", res.StatusCode, body)
	}
	if res, _ := do(t, ts, "GET", "/api/v1/requests/shop-api/products/get-product", "", true); res.StatusCode != 200 {
		t.Fatalf("imported request missing: %d", res.StatusCode)
	}
	if res, _ := do(t, ts, "POST", "/api/v1/import", `{"foo":1}`, true); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad import: %d", res.StatusCode)
	}
}

func TestSessionVariablesCarryAcrossSends(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			w.Write([]byte(`{"token":"from-login"}`))
			return
		}
		fmt.Fprint(w, r.Header.Get("Authorization"))
	}))
	defer target.Close()
	ts := newTestServer(t)
	do(t, ts, "PUT", "/api/v1/environments/dev", `{"variables":[{"key":"base","value":"`+target.URL+`"},{"key":"token","value":"file-token"}]}`, true)
	do(t, ts, "PUT", "/api/v1/requests/demo/login", `{"name":"Login","url":"{{base}}/login",
		"scripts":{"post":"pm.environment.set('token', pm.response.json().token)"}}`, true)
	do(t, ts, "PUT", "/api/v1/requests/demo/me", `{"name":"Me","url":"{{base}}/me","headers":[{"key":"Authorization","value":"Bearer {{token}}"}]}`, true)

	_, body := do(t, ts, "POST", "/api/v1/send", `{"path":"demo/me","environment":"dev"}`, true)
	if !strings.Contains(body, base64("Bearer file-token")) {
		t.Fatalf("before login: %s", body)
	}
	do(t, ts, "POST", "/api/v1/send", `{"path":"demo/login","environment":"dev"}`, true)
	_, body = do(t, ts, "POST", "/api/v1/send", `{"path":"demo/me","environment":"dev"}`, true)
	if !strings.Contains(body, base64("Bearer from-login")) {
		t.Fatalf("after login: %s", body)
	}
	// The file is untouched; only the session changed.
	_, envBody := do(t, ts, "GET", "/api/v1/environments/dev", "", true)
	if !strings.Contains(envBody, "file-token") {
		t.Fatalf("environment file changed: %s", envBody)
	}
	_, sess := do(t, ts, "GET", "/api/v1/session/variables", "", true)
	if !strings.Contains(sess, `"token":"from-login"`) {
		t.Fatalf("session view: %s", sess)
	}
	do(t, ts, "DELETE", "/api/v1/session/variables", "", true)
	_, body = do(t, ts, "POST", "/api/v1/send", `{"path":"demo/me","environment":"dev"}`, true)
	if !strings.Contains(body, base64("Bearer file-token")) {
		t.Fatalf("after reset: %s", body)
	}
}

func base64(s string) string { return b64.StdEncoding.EncodeToString([]byte(s)) }
