package openapi

import (
	"os"
	"strings"
	"testing"

	"github.com/codeen15/pyro/pkg/engine/importer"
	"github.com/codeen15/pyro/pkg/engine/model"
)

func load(t *testing.T, file string) *importer.Bundle {
	t.Helper()
	data, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	if !Looks(data) {
		t.Fatal("Looks = false")
	}
	b, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func find(b *importer.Bundle, p string) *model.Request {
	for _, r := range b.Requests {
		if r.Path == p {
			return r.Request
		}
	}
	return nil
}

func vars(b *importer.Bundle) map[string]model.Variable {
	out := map[string]model.Variable{}
	for _, v := range b.Collection.Variables {
		out[v.Key] = v
	}
	return out
}

func kv(list []model.KeyValue, k string) *model.KeyValue {
	for i := range list {
		if list[i].Key == k {
			return &list[i]
		}
	}
	return nil
}

func TestOpenAPI3(t *testing.T) {
	b := load(t, "shop.yaml")
	if b.Report.Format != "openapi-3.0.3" || b.Collection.Name != "Shop API" || b.Report.Requests != 6 || b.Report.Folders != 2 {
		t.Fatalf("%+v", b.Report)
	}
	// Tags-list order first (products, orders), then untagged.
	if got := strings.Join(b.Collection.Order, ","); got != "products,orders,health" {
		t.Errorf("order %s", got)
	}
	v := vars(b)
	if v["baseUrl"].Value != "https://eu.shop.test/v1" || v["orderId"].Value != "ord_42" || !v["bearerToken"].Secret || !v["x_api_key"].Secret && !v["apikey"].Secret {
		t.Errorf("vars %+v", b.Collection.Variables)
	}
	if b.Collection.Auth == nil || b.Collection.Auth.Type != "bearer" || b.Collection.Auth.Params["token"] != "{{bearerToken}}" {
		t.Errorf("collection auth %+v", b.Collection.Auth)
	}

	get := find(b, "orders/getorder")
	if get == nil || get.Name != "Get an order" || get.URL != "{{baseUrl}}/orders/{{orderId}}" || get.Auth != nil {
		t.Fatalf("getOrder %+v", get)
	}
	if q := kv(get.Query, "expand"); q == nil || q.Value != "items" || !q.Disabled {
		t.Errorf("expand %+v", q)
	}
	if h := kv(get.Headers, "X-Request-Id"); h == nil || h.Disabled || h.Value != "00000000-0000-0000-0000-000000000000" || kv(get.Headers, "Accept") != nil {
		t.Errorf("headers %+v", get.Headers)
	}
	if ex := get.Examples; len(ex) != 2 || ex[0].Name != "200 The order" || ex[0].Status != 200 || !strings.Contains(ex[0].Body, `"sku": "A-1"`) ||
		!strings.Contains(ex[0].Body, `"id"`) || ex[1].Status != 404 || ex[1].Body != "{\n  \"error\": \"not_found\"\n}" {
		t.Errorf("response examples: %+v", ex)
	}
	if del := find(b, "orders/deleteorder"); del.Auth == nil || del.Auth.Type != "none" {
		t.Errorf("security: [] should mean no auth, got %+v", del.Auth)
	}
	create := find(b, "orders/createorder")
	if create.Body == nil || create.Body.Type != model.BodyJSON {
		t.Fatalf("create body %+v", create.Body)
	}
	want := `{
  "note": "string",
  "sku": "A-1",
  "qty": 0,
  "placedAt": "2026-01-01T00:00:00Z",
  "lines": []
}`
	if create.Body.Content != want {
		t.Errorf("example body:\n%s\nwant\n%s", create.Body.Content, want)
	}
	list := find(b, "products/list-products")
	if list.Auth == nil || list.Auth.Type != "apikey" || list.Auth.Params["key"] != "X-API-Key" {
		t.Errorf("list auth %+v", list.Auth)
	}
	if q := kv(list.Query, "limit"); q == nil || q.Value != "20" || q.Disabled {
		t.Errorf("limit %+v", q)
	}
	if c := kv(list.Headers, "Cookie"); c == nil || c.Value != "session=string" {
		t.Errorf("cookie %+v", list.Headers)
	}
	up := find(b, "products/uploadimage")
	if up.Body.Type != model.BodyMultipart || !up.Body.Fields[0].File || up.Body.Fields[1].Value != "Front" || !up.Body.Fields[1].Disabled {
		t.Errorf("upload %+v", up.Body)
	}
	if find(b, "health") == nil {
		t.Error("untagged operation missing")
	}
	codes := map[string]bool{}
	for _, w := range b.Report.Warnings {
		codes[w.Code] = true
	}
	if !codes["openapi.servers"] || !codes["body.file"] {
		t.Errorf("warnings %+v", b.Report.Warnings)
	}
}

func TestSwagger2(t *testing.T) {
	b := load(t, "pets-v2.json")
	if b.Report.Format != "swagger-2.0" || b.Report.Requests != 2 {
		t.Fatalf("%+v", b.Report)
	}
	v := vars(b)
	if v["baseUrl"].Value != "https://petstore.test/api" || v["petId"].Value != "7" {
		t.Errorf("vars %+v", b.Collection.Variables)
	}
	if a := b.Collection.Auth; a == nil || a.Type != "apikey" || a.Params["in"] != "query" || a.Params["key"] != "api_key" {
		t.Errorf("auth %+v", a)
	}
	add := find(b, "add-a-pet")
	if add.Body.Type != model.BodyJSON || !strings.Contains(add.Body.Content, `"name": "Rex"`) {
		t.Errorf("body %+v", add.Body)
	}
	up := find(b, "uploadphoto")
	if up.Body.Type != model.BodyMultipart || !up.Body.Fields[0].File || up.URL != "{{baseUrl}}/pets/{{petId}}/photo" {
		t.Errorf("upload %+v %s", up.Body, up.URL)
	}
}

func TestNotOpenAPI(t *testing.T) {
	if Looks([]byte(`{"info":{"name":"x"}}`)) {
		t.Error("Postman collection taken for OpenAPI")
	}
	if _, err := Parse([]byte("openapi: 3.0.0\ninfo: {title: x}\npaths: {}\n")); err == nil {
		t.Error("empty paths accepted")
	}
}
