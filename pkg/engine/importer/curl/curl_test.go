package curl

import (
	"strings"
	"testing"

	"github.com/codeen15/pyro/pkg/engine/model"
)

func one(t *testing.T, s string) *model.Request {
	t.Helper()
	b, err := Parse("", []byte(s))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Requests) != 1 {
		t.Fatalf("%d requests", len(b.Requests))
	}
	return b.Requests[0].Request
}

func header(r *model.Request, k string) string {
	for _, h := range r.Headers {
		if strings.EqualFold(h.Key, k) {
			return h.Value
		}
	}
	return ""
}

// As copied by Chrome's "Copy as cURL (bash)".
func TestChromeCopy(t *testing.T) {
	r := one(t, `curl 'https://api.example.com/v1/orders?page=2&q=red%20shoes' \
  -H 'accept: application/json' \
  -H 'authorization: Bearer abc.def' \
  -H 'content-type: application/json' \
  --data-raw '{"sku":"A-1","qty":2}' \
  --compressed`)
	if r.Method != "POST" || r.URL != "https://api.example.com/v1/orders" || r.Name != "POST api.example.com/v1/orders" {
		t.Errorf("%s %s (%s)", r.Method, r.URL, r.Name)
	}
	if len(r.Query) != 2 || r.Query[1].Value != "red shoes" {
		t.Errorf("query %+v", r.Query)
	}
	if r.Auth == nil || r.Auth.Type != "bearer" || r.Auth.Params["token"] != "abc.def" || header(r, "authorization") != "" {
		t.Errorf("auth %+v headers %+v", r.Auth, r.Headers)
	}
	if r.Body.Type != model.BodyJSON || !strings.Contains(r.Body.Content, `"qty": 2`) {
		t.Errorf("body %+v", r.Body)
	}
}

func TestFormsAndOptions(t *testing.T) {
	r := one(t, `curl -XPUT -u admin:s3cret -d name=Ada -d 'lang=go lang' http://localhost:8080/users/1 -A pyro/1`)
	if r.Method != "PUT" || r.Auth.Params["password"] != "s3cret" || header(r, "User-Agent") != "pyro/1" {
		t.Errorf("%+v", r)
	}
	if r.Body.Type != model.BodyForm || len(r.Body.Fields) != 2 || r.Body.Fields[1].Value != "lang=go lang" && r.Body.Fields[1].Value != "go lang" {
		t.Errorf("body %+v", r.Body)
	}
	r = one(t, `curl -F 'avatar=@me.png;type=image/png' -F name=Ada https://x.test/upload`)
	if r.Method != "POST" || r.Body.Type != model.BodyMultipart || !r.Body.Fields[0].File || r.Body.Fields[0].Value != "me.png" {
		t.Errorf("multipart %+v", r.Body)
	}
	r = one(t, `curl -G -d q=go --data-urlencode 'tag=a b' https://x.test/search`)
	if r.Method != "GET" || r.Body != nil || len(r.Query) != 2 || r.Query[1].Value != "a b" {
		t.Errorf("get mode %s %+v %+v", r.Method, r.Query, r.Body)
	}
	r = one(t, `curl --json '{"a":1}' https://x.test/j`)
	if header(r, "Content-Type") != "application/json" || header(r, "Accept") != "application/json" || r.Body.Type != model.BodyJSON {
		t.Errorf("--json %+v", r)
	}
	r = one(t, `curl "https://user:pw@x.test/a#frag" -H "X-Quote: say \"hi\"" --request=DELETE`)
	if r.Method != "DELETE" || r.Auth.Params["username"] != "user" || r.URL != "https://x.test/a" || header(r, "X-Quote") != `say "hi"` {
		t.Errorf("%s %s %+v %+v", r.Method, r.URL, r.Auth, r.Headers)
	}
	r = one(t, `curl $'https://x.test/p' --data-raw $'line1\nline2' -H 'Content-Type: text/plain'`)
	if r.Body.Type != model.BodyText || r.Body.Content != "line1\nline2" {
		t.Errorf("ansi-c %+v", r.Body)
	}
}

func TestSeveralCommandsAndWarnings(t *testing.T) {
	b, err := Parse("Shop", []byte("$ curl https://x.test/a\ncurl -X POST https://x.test/b --frobnicate\n\ncurl.exe ^\n  https://x.test/c\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Requests) != 3 || b.Collection.Name != "Shop" || len(b.Collection.Order) != 3 {
		t.Fatalf("%+v", b.Requests)
	}
	if len(b.Report.Warnings) != 1 || b.Report.Warnings[0].Code != "curl.option" {
		t.Errorf("warnings %+v", b.Report.Warnings)
	}
	if _, err := Parse("", []byte(`curl -H 'unterminated`)); err == nil {
		t.Error("unterminated quote accepted")
	}
	if !Looks([]byte("  curl https://x")) || Looks([]byte(`{"info":{}}`)) {
		t.Error("Looks")
	}
}
