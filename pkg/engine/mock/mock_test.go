package mock

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codeen15/pyro/pkg/engine/importer"
	"github.com/codeen15/pyro/pkg/engine/model"
)

func shop() (*importer.Bundle, map[string]string) {
	json := []model.KeyValue{{Key: "Content-Type", Value: "application/json"}}
	return &importer.Bundle{Requests: []importer.Request{
		{Path: "orders/get", Request: &model.Request{Kind: model.KindHTTP, Name: "Get order", Method: "GET", URL: "{{baseUrl}}/orders/{{id}}?expand=items",
			Examples: []model.Example{{Name: "404 Missing", Status: 404, Body: `{"error":"nf"}`}, {Name: "200 OK", Status: 200, Headers: json, Body: `{"id":"ord_1"}`}}}},
		{Path: "orders/mine", Request: &model.Request{Kind: model.KindHTTP, Name: "My orders", Method: "GET", URL: "{{baseUrl}}/orders/mine",
			Examples: []model.Example{{Name: "Mine", Status: 200, Body: `[]`}}}},
		{Path: "orders/create", Request: &model.Request{Kind: model.KindHTTP, Name: "Create", Method: "POST", URL: "https://other.test/v1/orders",
			Examples: []model.Example{{Name: "Created", Status: 201, Body: `{"id":"new"}`}}}},
		{Path: "orders/cancel", Request: &model.Request{Kind: model.KindHTTP, Name: "Cancel", Method: "DELETE", URL: "{{baseUrl}}/orders/:id"}}, // no examples
		{Path: "live", Request: &model.Request{Kind: model.KindWebSocket, Name: "Live", URL: "ws://x/live", Examples: []model.Example{{Name: "x"}}}},
	}}, map[string]string{"baseUrl": "https://api.shop.test/v1"}
}

func get(t *testing.T, h http.Handler, method, target string, hdr ...string) (int, string, http.Header) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	b, _ := io.ReadAll(w.Result().Body)
	return w.Code, string(b), w.Result().Header
}

func TestMockRoutesAndExamples(t *testing.T) {
	var hits []Hit
	s := &Server{Source: func() (*importer.Bundle, map[string]string, error) { b, v := shop(); return b, v, nil }, OnHit: func(h Hit) { hits = append(hits, h) }}
	routes, _ := s.Routes()
	var paths []string
	for _, r := range routes {
		paths = append(paths, r.Method+" "+r.Path)
	}
	if got := strings.Join(paths, ", "); got != "GET /v1/orders/mine, POST /v1/orders, GET /v1/orders/{id}" && got != "POST /v1/orders, GET /v1/orders/mine, GET /v1/orders/{id}" {
		t.Errorf("routes: %s", got)
	}
	code, body, h := get(t, s, "GET", "/v1/orders/ord_9")
	if code != 200 || body != `{"id":"ord_1"}` || h.Get("Content-Type") != "application/json" || h.Get("X-Pyro-Mock-Example") != "200 OK" {
		t.Errorf("default example: %d %s %v", code, body, h)
	}
	// The literal route beats the {id} one.
	if code, body, _ := get(t, s, "GET", "/v1/orders/mine"); code != 200 || body != "[]" {
		t.Errorf("literal route: %d %s", code, body)
	}
	if code, body, _ := get(t, s, "GET", "/v1/orders/x", "X-Mock-Status", "404"); code != 404 || body != `{"error":"nf"}` {
		t.Errorf("by status: %d %s", code, body)
	}
	if code, _, _ := get(t, s, "GET", "/v1/orders/x?__example=404%20missing"); code != 404 {
		t.Errorf("by name (query, case-insensitive): %d", code)
	}
	if code, body, _ := get(t, s, "POST", "/v1/orders"); code != 201 || body != `{"id":"new"}` {
		t.Errorf("absolute URL route: %d %s", code, body)
	}
	if code, body, _ := get(t, s, "DELETE", "/v1/orders/1"); code != 404 || !strings.Contains(body, "routes") {
		t.Errorf("route without examples isn't served: %d %s", code, body)
	}
	if code, body, _ := get(t, s, "HEAD", "/v1/orders/mine"); code != 200 || body != "" {
		t.Errorf("HEAD falls back to GET without a body: %d %q", code, body)
	}
	if code, _, h := get(t, s, "OPTIONS", "/v1/orders", "Access-Control-Request-Method", "POST", "Origin", "http://app.test"); code != 204 || h.Get("Access-Control-Allow-Methods") == "" {
		t.Errorf("CORS preflight: %d %v", code, h)
	}
	// Preflights are answered but not logged.
	if len(hits) != 7 || hits[0].Example != "200 OK" || hits[0].Request != "orders/get" {
		t.Errorf("hits: %+v", hits)
	}
}

func TestRoutePath(t *testing.T) {
	for in, want := range map[string]string{
		"{{baseUrl}}/a/b":             "/v1/a/b",
		"{{ baseUrl }}/a":             "/v1/a",
		"{{unknown}}/a":               "/a",
		"https://x.test:8443/p/q?z=1": "/p/q",
		"x.test/p":                    "/p",
		"https://x.test":              "/",
	} {
		if got := routePath(in, map[string]string{"baseUrl": "https://api.test/v1/"}); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}
