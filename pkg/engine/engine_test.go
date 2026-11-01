package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
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
	}
	in.Vars = NewVars(&model.Environment{Variables: []model.Variable{{Key: "envName", Value: "dev"}, {Key: "token", Value: "env-token"}}}, in.Chain)
	in.Vars.Runtime["userId"] = "42"

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

func TestSendScriptsAssertionsExtractors(t *testing.T) {
	var seen []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("X-Order")+" "+r.Header.Get("X-Trace"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"token":"tok-1","items":[1,2,3]}`))
	}))
	defer ts.Close()

	chain := []*model.Collection{
		{Scripts: &model.Scripts{
			Pre:  `pm.variables.set("order", "c"); pm.request.headers.add({key: "X-Trace", value: "{{trace}}"});`,
			Post: `pm.test("collection sees response", () => pm.response.to.be.ok);`,
		}},
		{Scripts: &model.Scripts{Pre: `pm.variables.set("order", pm.variables.get("order") + "f"); pm.variables.set("trace", "t-9");`}},
	}
	req := &model.Request{
		Kind: model.KindHTTP, Name: "Login", Method: "POST", URL: ts.URL,
		Headers: []model.KeyValue{{Key: "X-Order", Value: "{{order}}"}},
		Scripts: &model.Scripts{
			Pre:  `pm.variables.set("order", pm.variables.get("order") + "r");`,
			Post: `pm.test("three items", () => pm.expect(pm.response.json().items).to.have.length(3)); console.log(pm.info.requestName);`,
		},
		Assertions: []model.Assertion{{Type: "status", Value: "200"}, {Type: "jsonpath", Path: "$.items[0]", Value: "9"}},
		Extract:    []model.Extractor{{Var: "token", From: "jsonpath", Path: "$.token"}},
	}
	e := New(httpdriver.New(httpdriver.Options{}))
	v := NewVars(nil, chain)
	res := e.Send(context.Background(), SendInput{Request: req, Chain: chain, Vars: v})
	if res.Error != "" || len(res.ScriptErrors) > 0 {
		t.Fatalf("error=%q scripts=%v", res.Error, res.ScriptErrors)
	}
	if len(seen) != 1 || seen[0] != "cfr t-9" {
		t.Fatalf("server saw %v (scripts must run collection → folder → request, before resolution)", seen)
	}
	var names []string
	for _, tr := range res.Tests {
		names = append(names, fmt.Sprintf("%s=%v", tr.Name, tr.Passed))
	}
	want := []string{"collection sees response=true", "three items=true", "status eq 200=true", "jsonpath $.items[0] eq 9=false"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("tests = %v", names)
	}
	if res.Passed() {
		t.Fatal("result with a failed assertion must not pass")
	}
	if v.Runtime["token"] != "tok-1" || res.Extracted["token"] != "tok-1" {
		t.Fatalf("extracted = %v runtime = %v", res.Extracted, v.Runtime)
	}
	if len(res.Logs) != 1 || res.Logs[0].Message != "Login" {
		t.Fatalf("logs = %+v", res.Logs)
	}
	if req.Headers[0].Value != "{{order}}" || len(req.Headers) != 1 {
		t.Fatal("input request was mutated by scripts")
	}
}

func TestPreRequestFailureAndSkip(t *testing.T) {
	hits := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer ts.Close()
	e := New(httpdriver.New(httpdriver.Options{}))

	res := e.Send(context.Background(), SendInput{Request: &model.Request{URL: ts.URL, Scripts: &model.Scripts{Pre: `throw new Error("boom")`}}})
	if res.Error == "" || len(res.ScriptErrors) != 1 || !strings.Contains(res.ScriptErrors[0], "boom") {
		t.Fatalf("failure: %+v", res)
	}
	res = e.Send(context.Background(), SendInput{Request: &model.Request{URL: ts.URL, Scripts: &model.Scripts{Pre: `pm.execution.skipRequest()`}}})
	if !res.Skipped || res.Error != "" {
		t.Fatalf("skip: %+v", res)
	}
	if hits != 0 {
		t.Fatalf("server was hit %d times", hits)
	}
}
