package docs

import (
	"strings"
	"testing"

	"github.com/codeen15/pyro/pkg/engine/importer"
	"github.com/codeen15/pyro/pkg/engine/model"
)

func TestRender(t *testing.T) {
	b := &importer.Bundle{
		Slug: "shop",
		Collection: &model.Collection{
			Name: "Shop API", Description: "Orders and **products**.\n\n<script>alert('x')</script>\n\n[Status](https://status.shop.test)",
			Variables: []model.Variable{{Key: "baseUrl", Value: "https://api.shop.test"}, {Key: "token", Value: "sk_live_SECRET", Secret: true}},
			Auth:      &model.Auth{Type: "bearer", Params: map[string]string{"token": "{{token}}"}},
		},
		Folders: map[string]*model.Collection{"admin": {Name: "Admin", Description: "Staff only.", Auth: &model.Auth{Type: "basic", Params: map[string]string{"username": "root", "password": "hunter2"}}}},
		Requests: []importer.Request{
			{Path: "list", Request: &model.Request{Kind: model.KindHTTP, Name: "List products", Method: "GET", URL: "{{baseUrl}}/products",
				Query:    []model.KeyValue{{Key: "page", Value: "1"}, {Key: "q", Value: "", Disabled: true}},
				Examples: []model.Example{{Name: "200 OK", Status: 200, Body: `{"items":[]}`}}}},
			{Path: "admin/purge", Request: &model.Request{Kind: model.KindHTTP, Name: "Purge <cache>", Method: "DELETE", URL: "{{baseUrl}}/cache",
				Body: &model.Body{Type: model.BodyJSON, Content: `{"all":true}`}}},
		},
	}
	out, err := Render(b)
	if err != nil {
		t.Fatal(err)
	}
	html := string(out)
	for _, want := range []string{
		"<strong>products</strong>", `href="https://status.shop.test"`, // Markdown
		`<span class="var">{{baseUrl}}</span>/products`, // variables marked
		"Bearer token using {{token}} (from the collection)", "Basic auth (from folder Admin)",
		"Purge &lt;cache&gt;", "&#34;all&#34;: true", "optional",
		"curl -X DELETE &#39;{{baseUrl}}/cache&#39;", "-u &#39;&lt;username&gt;:&lt;password&gt;&#39;",
		`<span class="st s2">200</span> 200 OK`, "••••••",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, leak := range []string{"<script>alert", "sk_live_SECRET", "hunter2", ">root<"} {
		if strings.Contains(html, leak) {
			t.Errorf("page contains %q", leak)
		}
	}
}
