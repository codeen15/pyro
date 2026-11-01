package postman

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/pyrohq/pyro/pkg/engine/importer"
	"github.com/pyrohq/pyro/pkg/engine/model"
)

func loadFixture(t *testing.T, name string) *importer.Bundle {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func find(b *importer.Bundle, path string) *model.Request {
	for _, r := range b.Requests {
		if r.Path == path {
			return r.Request
		}
	}
	return nil
}

func TestCollectionV21(t *testing.T) {
	b := loadFixture(t, "shop.v21.json")

	if b.Slug != "shop-api" || b.Collection.Name != "Shop API" || b.Collection.Description != "Demo shop" {
		t.Fatalf("collection = %q %+v", b.Slug, b.Collection)
	}
	if b.Report.Format != "postman-v2.1" || b.Report.Requests != 6 || b.Report.Folders != 2 {
		t.Fatalf("report counts = %+v", b.Report)
	}
	wantVars := []model.Variable{
		{Key: "baseUrl", Value: "https://shop.example.com"},
		{Key: "apiSecret", Value: "s3cr3t", Secret: true},
		{Key: "port", Value: "8080"},
	}
	if !reflect.DeepEqual(b.Collection.Variables, wantVars) {
		t.Errorf("variables = %+v", b.Collection.Variables)
	}
	if a := b.Collection.Auth; a == nil || a.Type != "bearer" || a.Params["token"] != "{{token}}" {
		t.Errorf("collection auth = %+v", a)
	}
	if b.Collection.Scripts == nil || !strings.Contains(b.Collection.Scripts.Pre, "pm.variables.set") {
		t.Errorf("collection scripts = %+v", b.Collection.Scripts)
	}

	// Folder with noauth gets a folder.yaml; plain folder with a slug-equal name doesn't.
	if f := b.Folders["auth"]; f == nil || f.Auth == nil || f.Auth.Type != "none" {
		t.Errorf("auth folder = %+v", f)
	}

	login := find(b, "auth/login")
	if login == nil || login.Method != "POST" || login.URL != "{{baseUrl}}/auth/login" ||
		login.Body.Type != model.BodyJSON || login.Auth != nil || !strings.Contains(login.Scripts.Post, "\n") {
		t.Fatalf("login = %+v", login)
	}

	get := find(b, "products/get-product")
	wantQuery := []model.KeyValue{{Key: "expand", Value: "reviews"}, {Key: "debug", Value: "true", Disabled: true}}
	if get == nil || get.URL != "{{baseUrl}}/products/42" || !reflect.DeepEqual(get.Query, wantQuery) || !get.Headers[0].Disabled {
		t.Fatalf("get product = %+v", get)
	}
	// Duplicate names in a folder get unique slugs; the string-URL shorthand works.
	if dup := find(b, "products/get-product-2"); dup == nil || dup.URL != "https://shop.example.com/products/1" || dup.Method != "GET" {
		t.Fatalf("duplicate = %+v", dup)
	}

	upload := find(b, "products/upload-image")
	if upload.Body.Type != model.BodyMultipart || !upload.Body.Fields[1].File || upload.Body.Fields[1].Value != "/Users/me/front.png" {
		t.Errorf("upload body = %+v", upload.Body)
	}
	gql := find(b, "products/search-graphql")
	if gql.Body.Type != model.BodyGraphQL || gql.Body.Variables != `{"first": 10}` {
		t.Errorf("graphql body = %+v", gql.Body)
	}
	checkout := find(b, "checkout")
	if checkout.Auth.Params["key"] != "X-Api-Key" || checkout.Auth.Params["in"] != "header" ||
		checkout.Body.Type != model.BodyForm || !checkout.Body.Fields[1].Disabled {
		t.Errorf("checkout = %+v %+v", checkout.Auth, checkout.Body)
	}

	codes := map[string]string{}
	for _, w := range b.Report.Warnings {
		codes[w.Code] += w.Path + ";"
	}
	for code, path := range map[string]string{
		"examples.skipped": "auth/login;",
		"auth.unsupported": "products/upload-image;",
		"body.file":        "products/upload-image;",
		"script.api":       "checkout;",
		"script.deferred":  ";",
	} {
		if codes[code] != path {
			t.Errorf("warning %s at %q, want %q (all: %+v)", code, codes[code], path, b.Report.Warnings)
		}
	}
}

func TestV20AuthAndHeaderString(t *testing.T) {
	b, err := Parse([]byte(`{
		"info": {"name": "Old", "schema": "https://schema.getpostman.com/json/collection/v2.0.0/collection.json"},
		"item": [{"name": "R", "request": {
			"method": "get",
			"url": "http://x/y",
			"header": "Accept: text/plain\nX-A: 1",
			"auth": {"type": "basic", "basic": {"username": "u", "password": "p"}}
		}}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	r := find(b, "r")
	if b.Report.Format != "postman-v2.0" || r.Method != "GET" || r.Auth.Params["username"] != "u" || len(r.Headers) != 2 || r.Headers[1].Value != "1" {
		t.Fatalf("got %+v / %+v", b.Report, r)
	}
}

func TestEnvironment(t *testing.T) {
	b, err := Parse([]byte(`{
		"name": "Staging",
		"values": [
			{"key": "baseUrl", "value": "https://staging", "enabled": true},
			{"key": "token", "value": "t", "type": "secret", "enabled": true},
			{"key": "old", "value": "x", "enabled": false}
		],
		"_postman_variable_scope": "environment"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if b.Collection != nil || len(b.Environments) != 1 {
		t.Fatalf("bundle = %+v", b)
	}
	env := b.Environments[0]
	want := []model.Variable{
		{Key: "baseUrl", Value: "https://staging"},
		{Key: "token", Value: "t", Secret: true},
		{Key: "old", Value: "x", Disabled: true},
	}
	if env.Name != "Staging" || !reflect.DeepEqual(env.Variables, want) {
		t.Fatalf("env = %+v", env)
	}
}

func TestRejects(t *testing.T) {
	for name, in := range map[string]string{
		"invalid json": `{`,
		"v1":           `{"id": "x", "requests": []}`,
		"unknown":      `{"foo": 1}`,
		"bad schema":   `{"info": {"name": "x", "schema": "https://example.com/v9"}}`,
	} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
