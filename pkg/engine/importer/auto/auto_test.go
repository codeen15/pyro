package auto

import (
	"strings"
	"testing"
)

func TestDetect(t *testing.T) {
	cases := map[string]string{
		"curl https://x.test/a": "curl",
		"openapi: 3.1.0\ninfo: {title: T}\npaths:\n  /a:\n    get: {}\n":                                                  "openapi-3.1.0",
		`{"swagger":"2.0","info":{"title":"T"},"paths":{"/a":{"get":{}}}}`:                                                "swagger-2.0",
		`{"log":{"entries":[{"request":{"method":"GET","url":"https://x.test/a","headers":[]}}]}}`:                        "har",
		`{"info":{"name":"P","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},"item":[]}`: "postman-v2.1",
		`{"name":"dev","values":[]}`: "postman-environment",
	}
	for in, want := range cases {
		b, err := Parse("calls.txt", []byte(in))
		if err != nil {
			t.Errorf("%.30q: %v", in, err)
			continue
		}
		if b.Report.Format != want {
			t.Errorf("%.30q: format %s, want %s", in, b.Report.Format, want)
		}
	}
	b, _ := Parse("/tmp/calls.sh", []byte("curl https://x.test/a"))
	if b.Collection.Name != "calls" {
		t.Errorf("name from file: %q", b.Collection.Name)
	}
	if _, err := Parse("", []byte("hello")); err == nil || !strings.Contains(err.Error(), "unrecognised") {
		t.Errorf("err = %v", err)
	}
}
