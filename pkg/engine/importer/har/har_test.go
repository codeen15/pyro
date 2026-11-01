package har

import (
	"testing"

	"github.com/pyrohq/pyro/pkg/engine/model"
)

const sample = `{"log":{"version":"1.2","creator":{"name":"WebInspector"},
 "pages":[{"title":"https://shop.test/cart"}],
 "entries":[
  {"_resourceType":"document","request":{"method":"GET","url":"https://shop.test/cart","headers":[{"name":":authority","value":"shop.test"},{"name":"accept","value":"text/html"}],"queryString":[]}},
  {"_resourceType":"script","request":{"method":"GET","url":"https://cdn.test/app.js","headers":[]}},
  {"_resourceType":"preflight","request":{"method":"OPTIONS","url":"https://api.shop.test/cart","headers":[]}},
  {"_resourceType":"fetch","request":{"method":"POST","url":"https://api.shop.test/cart/items?ref=home","headers":[{"name":"content-type","value":"application/json"},{"name":"content-length","value":"20"},{"name":"cookie","value":"sid=1"}],
   "queryString":[{"name":"ref","value":"home"}],"postData":{"mimeType":"application/json","text":"{\"sku\":\"A\",\"qty\":1}"}}},
  {"_resourceType":"fetch","request":{"method":"POST","url":"https://api.shop.test/cart/items","headers":[],"postData":{"mimeType":"application/x-www-form-urlencoded","text":"a=1&b=two%20words"}}},
  {"_resourceType":"websocket","request":{"method":"GET","url":"wss://api.shop.test/live","headers":[{"name":"Sec-WebSocket-Key","value":"x"}]}}
 ]}}`

func TestHAR(t *testing.T) {
	if !Looks([]byte(sample)) {
		t.Fatal("Looks")
	}
	b, err := Parse("", []byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if b.Collection.Name != "shop.test" || len(b.Requests) != 4 || b.Report.Folders != 2 {
		t.Fatalf("%s: %d requests, %d folders", b.Collection.Name, len(b.Requests), b.Report.Folders)
	}
	add := b.Requests[1]
	if add.Path != "api-shop-test/post-cart-items" || add.Request.URL != "https://api.shop.test/cart/items" || len(add.Request.Query) != 1 {
		t.Errorf("%s %s %+v", add.Path, add.Request.URL, add.Request.Query)
	}
	if add.Request.Body.Type != model.BodyJSON || len(add.Request.Headers) != 2 {
		t.Errorf("body %+v headers %+v", add.Request.Body, add.Request.Headers)
	}
	form := b.Requests[2]
	if form.Path != "api-shop-test/post-cart-items-2" || form.Request.Body.Type != model.BodyForm || form.Request.Body.Fields[1].Value != "two words" {
		t.Errorf("form %s %+v", form.Path, form.Request.Body)
	}
	ws := b.Requests[3].Request
	if ws.Kind != model.KindWebSocket || len(ws.Headers) != 0 {
		t.Errorf("ws %+v", ws)
	}
	if doc := b.Requests[0].Request; len(doc.Headers) != 1 {
		t.Errorf("pseudo-headers kept: %+v", doc.Headers)
	}
	codes := map[string]bool{}
	for _, w := range b.Report.Warnings {
		codes[w.Code] = true
	}
	if !codes["har.skipped"] || !codes["har.cookies"] {
		t.Errorf("warnings %+v", b.Report.Warnings)
	}
	if got := b.Folders["api-shop-test"].Order; len(got) != 3 {
		t.Errorf("folder order %v", got)
	}
}
