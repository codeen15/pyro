package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/protocol/httpdriver"
)

func TestProcessorsRunInOrderAcrossLevels(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"q":%q,"trace":%q,"xml":"<a><b>42</b></a>"}`, r.URL.Query().Get("n"), r.Header.Get("X-Trace"))
	}))
	defer ts.Close()

	coll := &model.Collection{
		Name: "c",
		Pre:  []model.Processor{{Type: model.ProcVars, Vars: []model.KeyValue{{Key: "trace", Value: "coll"}}}},
		Post: []model.Processor{{Type: model.ProcScript, Script: `pm.variables.set("order", "collection")`}},
	}
	req := &model.Request{
		Kind: model.KindHTTP, Name: "r", Method: "GET", URL: ts.URL + "/?n={{n}}",
		Headers: []model.KeyValue{{Key: "X-Trace", Value: "{{trace}}"}},
		Pre: []model.Processor{
			{Type: model.ProcCounter, Counter: &model.Counter{Var: "n", Start: 5, Step: 5, End: 10, Format: "%03d"}},
			{Type: model.ProcVars, Vars: []model.KeyValue{{Key: "trace", Value: "{{trace}}-req"}}},
			{Type: model.ProcVars, Disabled: true, Vars: []model.KeyValue{{Key: "trace", Value: "disabled"}}},
		},
		Post: []model.Processor{
			{Type: model.ProcScript, Script: `pm.variables.set("order", pm.variables.get("order") + ",request")`},
			{Type: model.ProcExtract, Extract: &model.Extractor{Var: "q", From: "jsonpath", Path: "$.q"}},
			{Type: model.ProcExtract, Extract: &model.Extractor{Var: "mid", From: "boundary", Left: `"trace":"`, Right: `"`}},
			{Type: model.ProcAssert, Assert: &model.Assertion{Type: "jsonpath", Path: "$.trace", Op: "eq", Value: "coll-req"}},
		},
		// Legacy fields still run, after the processors.
		Assertions: []model.Assertion{{Type: "status", Value: "200"}},
	}
	e := New(httpdriver.New(httpdriver.Options{}))
	in := SendInput{Request: req, Chain: []*model.Collection{coll}}
	in.Vars = NewVars(nil, in.Chain)
	var qs []string
	for i := 0; i < 3; i++ {
		res := e.Send(context.Background(), in)
		if !res.Passed() {
			t.Fatalf("send %d failed: %+v %+v %v", i, res.Tests, res.ScriptErrors, res.Error)
		}
		if len(res.Tests) != 2 || !strings.Contains(res.Tests[0].Name, "$.trace") {
			t.Fatalf("tests = %+v", res.Tests)
		}
		qs = append(qs, res.Extracted["q"])
		if res.Extracted["mid"] != "coll-req" {
			t.Fatalf("boundary = %q", res.Extracted["mid"])
		}
	}
	// Counter: 5, 10, then past End wraps to Start; formatted.
	if strings.Join(qs, ",") != "005,010,005" {
		t.Fatalf("counter values = %v", qs)
	}
	if got := in.Vars.Runtime["order"]; got != "collection,request" {
		t.Fatalf("post order = %q", got)
	}
}

func TestProcessorErrors(t *testing.T) {
	hits := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer ts.Close()
	e := New(httpdriver.New(httpdriver.Options{}))
	for _, p := range []model.Processor{
		{Type: model.ProcExtract, Extract: &model.Extractor{Var: "x", From: "status"}}, // post-only
		{Type: "nonsense"},
		{Type: "plugin:nope"}, // no plugin manager
	} {
		res := e.Send(context.Background(), SendInput{Request: &model.Request{Kind: model.KindHTTP, URL: ts.URL, Pre: []model.Processor{p}}})
		if res.Error == "" || len(res.ScriptErrors) != 1 {
			t.Fatalf("%s: error=%q scriptErrors=%v", p.Type, res.Error, res.ScriptErrors)
		}
	}
	if hits != 0 {
		t.Fatalf("a failing pre-processor must stop the request; server saw %d", hits)
	}
	// A post-processor of the wrong kind is reported but the response stands.
	res := e.Send(context.Background(), SendInput{Request: &model.Request{Kind: model.KindHTTP, URL: ts.URL, Post: []model.Processor{{Type: model.ProcCounter, Counter: &model.Counter{Var: "n"}}}}})
	if res.Response == nil || len(res.ScriptErrors) != 1 {
		t.Fatalf("post error: %+v", res)
	}
	if !UsesScripts(&model.Request{Post: []model.Processor{{Type: model.ProcScript}}}, nil) || UsesScripts(&model.Request{Pre: []model.Processor{{Type: model.ProcVars}}}, nil) {
		t.Fatal("UsesScripts")
	}
}
