package httpdriver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pyrohq/pyro/pkg/engine/model"
)

// echo replies with a JSON description of the request it received.
func echo(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/redirect" {
		http.Redirect(w, r, "/final", http.StatusFound)
		return
	}
	if r.URL.Path == "/big" {
		w.Write([]byte(strings.Repeat("x", 5000)))
		return
	}
	if r.URL.Path == "/slow" {
		time.Sleep(200 * time.Millisecond)
	}
	ct := r.Header.Get("Content-Type")
	out := map[string]any{
		"method": r.Method, "path": r.URL.Path, "query": r.URL.Query(),
		"contentType": ct, "auth": r.Header.Get("Authorization"), "host": r.Host,
	}
	if strings.HasPrefix(ct, "multipart/") {
		if err := r.ParseMultipartForm(1 << 20); err == nil {
			out["form"] = r.MultipartForm.Value
			files := map[string]string{}
			for k, fhs := range r.MultipartForm.File {
				f, _ := fhs[0].Open()
				b, _ := io.ReadAll(f)
				files[k] = fhs[0].Filename + ":" + string(b)
			}
			out["files"] = files
		}
	} else {
		b, _ := io.ReadAll(r.Body)
		out["body"] = string(b)
	}
	http.SetCookie(w, &http.Cookie{Name: "sid", Value: "abc", Path: "/"})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(out)
}

func run(t *testing.T, d *Driver, req *model.Request) map[string]any {
	t.Helper()
	res, err := d.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(res.Body, &m); err != nil {
		t.Fatalf("body %q: %v", res.Body, err)
	}
	return m
}

func TestJSONBodyQueryAndHeaders(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(echo))
	defer ts.Close()
	d := New(Options{})

	res, err := d.Execute(context.Background(), &model.Request{
		Method: "post",
		URL:    ts.URL + "/orders?existing=1",
		Query:  []model.KeyValue{{Key: "a", Value: "1"}, {Key: "skip", Value: "x", Disabled: true}},
		Headers: []model.KeyValue{
			{Key: "Authorization", Value: "Bearer t"},
			{Key: "Host", Value: "example.test"},
			{Key: "X-Off", Value: "1", Disabled: true},
		},
		Body: &model.Body{Type: model.BodyJSON, Content: `{"qty":1}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 201 || res.StatusText != "Created" {
		t.Fatalf("status = %d %q", res.Status, res.StatusText)
	}
	if res.Timings.Total <= 0 || res.Size != int64(len(res.Body)) {
		t.Fatalf("timings/size = %+v %d", res.Timings, res.Size)
	}
	if len(res.Cookies) != 1 || res.Cookies[0].Name != "sid" {
		t.Fatalf("cookies = %+v", res.Cookies)
	}
	var m map[string]any
	json.Unmarshal(res.Body, &m)
	q := m["query"].(map[string]any)
	if m["method"] != "POST" || m["body"] != `{"qty":1}` || m["contentType"] != "application/json" ||
		m["auth"] != "Bearer t" || m["host"] != "example.test" || q["a"] == nil || q["existing"] == nil || q["skip"] != nil {
		t.Fatalf("echo = %v", m)
	}
}

func TestFormMultipartAndGraphQL(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(echo))
	defer ts.Close()
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644)
	d := New(Options{FileRoot: root})

	m := run(t, d, &model.Request{Method: "POST", URL: ts.URL, Body: &model.Body{
		Type: model.BodyForm, Fields: []model.KeyValue{{Key: "a", Value: "1 2"}, {Key: "b", Value: "x", Disabled: true}},
	}})
	if m["body"] != "a=1+2" || m["contentType"] != "application/x-www-form-urlencoded" {
		t.Fatalf("form echo = %v", m)
	}

	m = run(t, d, &model.Request{Method: "POST", URL: ts.URL, Body: &model.Body{
		Type: model.BodyMultipart, Fields: []model.KeyValue{{Key: "name", Value: "pyro"}, {Key: "doc", Value: "a.txt", File: true}},
	}})
	if m["files"].(map[string]any)["doc"] != "a.txt:hello" || m["form"].(map[string]any)["name"] == nil {
		t.Fatalf("multipart echo = %v", m)
	}

	m = run(t, d, &model.Request{Method: "POST", URL: ts.URL, Body: &model.Body{
		Type: model.BodyGraphQL, Content: "query { me { id } }", Variables: `{"x":1}`,
	}})
	if m["body"] != `{"query":"query { me { id } }","variables":{"x":1}}` {
		t.Fatalf("graphql echo = %v", m["body"])
	}

	if _, err := d.Execute(context.Background(), &model.Request{URL: ts.URL, Body: &model.Body{Type: model.BodyGraphQL, Variables: "{bad"}}); err == nil {
		t.Fatal("expected error for invalid GraphQL variables")
	}
}

func TestRedirectsTimeoutTruncation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(echo))
	defer ts.Close()

	res, err := New(Options{}).Execute(context.Background(), &model.Request{URL: ts.URL + "/redirect"})
	if err != nil || res.Status != http.StatusFound {
		t.Fatalf("no-follow: %v %v", res, err)
	}
	m := run(t, New(Options{FollowRedirects: true}), &model.Request{URL: ts.URL + "/redirect"})
	if m["path"] != "/final" {
		t.Fatalf("follow: %v", m)
	}

	_, err = New(Options{Timeout: 50 * time.Millisecond}).Execute(context.Background(), &model.Request{URL: ts.URL + "/slow"})
	if err == nil || err.Error() != "request timed out" {
		t.Fatalf("timeout err = %v", err)
	}

	res, err = New(Options{MaxBodyBytes: 100}).Execute(context.Background(), &model.Request{URL: ts.URL + "/big"})
	if err != nil || !res.BodyTruncated || len(res.Body) != 100 || res.Size != 5000 {
		t.Fatalf("truncation: len=%d size=%d trunc=%v err=%v", len(res.Body), res.Size, res.BodyTruncated, err)
	}
}

func TestURLErrors(t *testing.T) {
	d := New(Options{Timeout: time.Second})
	for _, u := range []string{"ftp://x", "http://", "://bad"} {
		if _, err := d.Execute(context.Background(), &model.Request{URL: u}); err == nil {
			t.Errorf("%q: expected error", u)
		}
	}
	_, err := d.Execute(context.Background(), &model.Request{URL: "http://127.0.0.1:1"})
	if err == nil || !strings.Contains(err.Error(), "could not connect") {
		t.Errorf("refused err = %v", err)
	}
}

func TestForVUHasOwnCookies(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("sid"); err == nil {
			w.Write([]byte(c.Value))
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: r.URL.Query().Get("v"), Path: "/"})
	}))
	defer ts.Close()
	base := New(Options{})
	a, b := base.ForVU(), base.ForVU()
	a.Execute(context.Background(), &model.Request{URL: ts.URL + "?v=A"})
	b.Execute(context.Background(), &model.Request{URL: ts.URL + "?v=B"})
	ra, _ := a.Execute(context.Background(), &model.Request{URL: ts.URL})
	rb, _ := b.Execute(context.Background(), &model.Request{URL: ts.URL})
	if string(ra.Body) != "A" || string(rb.Body) != "B" {
		t.Fatalf("cookies leaked between VUs: %q %q", ra.Body, rb.Body)
	}
}
