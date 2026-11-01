package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/pyrohq/pyro/pkg/engine/model"
	"github.com/pyrohq/pyro/pkg/engine/protocol/httpdriver"
)

func TestSendResolvesVariablesAndInheritsAuth(t *testing.T) {
	var gotAuth, gotPath, gotHeader string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath, gotHeader = r.Header.Get("Authorization"), r.URL.RequestURI(), r.Header.Get("X-Env")
		w.WriteHeader(http.StatusTeapot)
	}))
	defer ts.Close()

	req := &model.Request{
		Kind:    model.KindHTTP,
		Method:  "GET",
		URL:     "{{baseUrl}}/users/{{userId}}",
		Query:   []model.KeyValue{{Key: "v", Value: "{{version}}"}},
		Headers: []model.KeyValue{{Key: "X-Env", Value: "{{envName}}-{{missing}}"}},
	}
	in := SendInput{
		Request: req,
		Chain: []*model.Collection{
			{Variables: []model.Variable{{Key: "baseUrl", Value: ts.URL}, {Key: "version", Value: "coll"}}},
			{Variables: []model.Variable{{Key: "version", Value: "folder"}}, Auth: &model.Auth{Type: "bearer", Params: map[string]string{"token": "{{token}}"}}},
		},
		Environment: &model.Environment{Variables: []model.Variable{{Key: "envName", Value: "dev"}, {Key: "token", Value: "env-token"}}},
		Runtime:     map[string]string{"userId": "42"},
	}

	res := New(httpdriver.New(httpdriver.Options{})).Send(context.Background(), in)
	if res.Error != "" {
		t.Fatal(res.Error)
	}
	if res.Response.Status != http.StatusTeapot {
		t.Fatalf("status = %d", res.Response.Status)
	}
	if gotPath != "/users/42?v=folder" || gotAuth != "Bearer env-token" || gotHeader != "dev-{{missing}}" {
		t.Fatalf("server saw path=%q auth=%q header=%q", gotPath, gotAuth, gotHeader)
	}
	if !reflect.DeepEqual(res.Unresolved, []string{"missing"}) {
		t.Fatalf("unresolved = %v", res.Unresolved)
	}
	if req.URL != "{{baseUrl}}/users/{{userId}}" || len(req.Headers) != 1 {
		t.Fatal("input request was mutated")
	}
}

func TestSendErrors(t *testing.T) {
	e := New(httpdriver.New(httpdriver.Options{}))
	if res := e.Send(context.Background(), SendInput{Request: &model.Request{Kind: model.KindWebSocket}}); res.Error == "" {
		t.Error("websocket send should fail")
	}
	res := e.Send(context.Background(), SendInput{Request: &model.Request{URL: "http://x", Auth: &model.Auth{Type: "hawk"}}})
	if res.Error == "" || res.Sent == nil {
		t.Errorf("unsupported auth: %+v", res)
	}
	res = e.Send(context.Background(), SendInput{Request: &model.Request{URL: "http://127.0.0.1:1"}})
	if res.Error == "" || res.Response != nil {
		t.Errorf("connection refused: %+v", res)
	}
}
