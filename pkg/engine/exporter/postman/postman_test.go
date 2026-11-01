package postman

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codeen15/pyro/pkg/engine"
	"github.com/codeen15/pyro/pkg/engine/importer"
	importpm "github.com/codeen15/pyro/pkg/engine/importer/postman"
	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/protocol/httpdriver"
)

func bundle(base string) *importer.Bundle {
	return &importer.Bundle{
		Slug: "shop",
		Collection: &model.Collection{
			Name: "Shop", Description: "Demo",
			Variables: []model.Variable{{Key: "base", Value: base}, {Key: "token", Value: "s3", Secret: true}},
			Auth:      &model.Auth{Type: "bearer", Params: map[string]string{"token": "{{token}}"}},
			Scripts:   &model.Scripts{Pre: "pm.variables.set('fromPre', 'yes');"},
		},
		Folders: map[string]*model.Collection{
			"orders": {Name: "Orders", Auth: &model.Auth{Type: "none"}},
			"empty":  {Name: "Empty"},
		},
		Requests: []importer.Request{
			{Path: "orders/create", Request: &model.Request{
				Kind: model.KindHTTP, Name: "Create order", Method: "POST", URL: "{{base}}/orders",
				Query:   []model.KeyValue{{Key: "dry", Value: "1"}, {Key: "debug", Value: "1", Disabled: true}},
				Headers: []model.KeyValue{{Key: "X-Client", Value: "pyro"}},
				Body:    &model.Body{Type: model.BodyJSON, Content: `{"sku":"A-1"}`},
				Assertions: []model.Assertion{
					{Type: "status", Value: "201"},
					{Type: "header", Path: "Location"},
					{Type: "jsonpath", Path: "$.order.items[0]['sku']", Value: "A-1"},
					{Type: "jsonpath", Path: "$.order.total", Op: "gt", Value: "10"},
					{Type: "body", Op: "contains", Value: "A-1"},
					{Type: "jsonpath", Path: "$..sku", Value: "A-1"}, // not convertible
				},
				Extract: []model.Extractor{
					{Var: "orderId", From: "jsonpath", Path: "$.order.id"},
					{Var: "loc", From: "header", Path: "Location"},
					{Var: "num", From: "regex", Path: `ord_(\d+)`},
				},
			}},
			{Path: "status", Request: &model.Request{Kind: model.KindHTTP, Name: "Status", URL: "{{base}}/status?verbose=1"}},
			{Path: "live", Request: &model.Request{Kind: model.KindWebSocket, Name: "Live", URL: "ws://x"}},
		},
	}
}

func TestExportShape(t *testing.T) {
	out, rep, err := Export(bundle("https://api.shop.test:8443"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["info"].(map[string]any)["schema"] != schemaV21 {
		t.Error("schema")
	}
	items := doc["item"].([]any)
	if len(items) != 3 { // orders, status, empty (live skipped)
		t.Fatalf("%d top-level items: %s", len(items), out)
	}
	empty := items[2].(map[string]any)
	if empty["name"] != "Empty" || empty["item"] == nil {
		t.Errorf("empty folder must keep item: [] (%v)", empty)
	}
	create := items[0].(map[string]any)["item"].([]any)[0].(map[string]any)["request"].(map[string]any)
	u := create["url"].(map[string]any)
	if u["raw"] != "{{base}}/orders?dry=1" || len(u["query"].([]any)) != 2 {
		t.Errorf("url %v", u)
	}
	if rep.Requests != 2 || rep.Folders != 2 {
		t.Errorf("report %+v", rep)
	}
	codes := map[string]int{}
	for _, w := range rep.Warnings {
		codes[w.Code]++
	}
	if codes["export.kind"] != 1 || codes["export.assertion"] != 1 {
		t.Errorf("warnings %+v", rep.Warnings)
	}
	if !strings.Contains(string(out), `"type": "noauth"`) {
		t.Error("folder 'none' auth should export as noauth")
	}
}

// Round trip: export, import with the Postman importer, and run the result
// through the engine; the converted assertions must pass and the converted
// extractors must set the same variables.
func TestRoundTripRunsConvertedChecks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/orders" {
			if r.Header.Get("Authorization") != "" || r.URL.Query().Get("dry") != "1" || r.URL.Query().Has("debug") {
				w.WriteHeader(400)
				return
			}
			w.Header().Set("Location", "/orders/ord_77")
			w.WriteHeader(201)
			w.Write([]byte(`{"order":{"id":"ord_77","total":12.5,"items":[{"sku":"A-1"}]}}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	out, _, err := Export(bundle(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	back, err := importpm.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if back.Collection.Variables[1].Secret != true || back.Collection.Auth.Type != "bearer" {
		t.Errorf("collection settings lost: %+v", back.Collection)
	}
	var req *model.Request
	for _, r := range back.Requests {
		if r.Path == "orders/create-order" {
			req = r.Request
		}
	}
	if req == nil {
		t.Fatalf("paths: %+v", back.Requests)
	}
	chain := []*model.Collection{back.Collection, back.Folders["orders"]}
	eng := engine.New(httpdriver.New(httpdriver.Options{}))
	v := engine.NewVars(nil, chain)
	res := eng.Send(context.Background(), engine.SendInput{Request: req, Chain: chain, Vars: v})
	if res.Error != "" || len(res.ScriptErrors) > 0 {
		t.Fatalf("send: %s %v", res.Error, res.ScriptErrors)
	}
	if len(res.Tests) != 5 {
		t.Fatalf("%d tests: %+v", len(res.Tests), res.Tests)
	}
	for _, tr := range res.Tests {
		if !tr.Passed {
			t.Errorf("converted test failed: %s: %s", tr.Name, tr.Error)
		}
	}
	for k, want := range map[string]string{"orderId": "ord_77", "loc": "/orders/ord_77", "num": "77", "fromPre": "yes"} {
		if got := v.Runtime[k]; got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestExportEnvironment(t *testing.T) {
	out, err := ExportEnvironment(&model.Environment{Name: "Staging", Variables: []model.Variable{{Key: "a", Value: "1"}, {Key: "pw", Value: "x", Secret: true, Disabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	back, err := importpm.Parse(out)
	if err != nil || back.Environments[0].Name != "Staging" || !back.Environments[0].Variables[1].Secret || !back.Environments[0].Variables[1].Disabled {
		t.Fatalf("%v %+v", err, back)
	}
}
