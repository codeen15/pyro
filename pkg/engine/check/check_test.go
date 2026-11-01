package check

import (
	"reflect"
	"testing"
	"time"

	"github.com/pyrohq/pyro/pkg/engine/model"
	"github.com/pyrohq/pyro/pkg/engine/protocol"
)

func resp() *Response {
	return Wrap(&protocol.Response{
		Status:  201,
		Headers: []model.KeyValue{{Key: "Content-Type", Value: "application/json"}, {Key: "X-Request-Id", Value: "req-9"}},
		Body:    []byte(`{"id": 42, "name": "Widget", "tags": ["a","b"], "owner": {"email": "x@y.z"}, "nothing": null}`),
		Timings: protocol.Timings{Total: 120 * time.Millisecond},
	})
}

func TestAssert(t *testing.T) {
	cases := []struct {
		a    model.Assertion
		pass bool
	}{
		{model.Assertion{Type: "status", Value: "201"}, true},
		{model.Assertion{Type: "status", Op: "lt", Value: "300"}, true},
		{model.Assertion{Type: "status", Value: "200"}, false},
		{model.Assertion{Type: "header", Path: "x-request-id"}, true},
		{model.Assertion{Type: "header", Path: "content-type", Op: "contains", Value: "json"}, true},
		{model.Assertion{Type: "header", Path: "X-Missing"}, false},
		{model.Assertion{Type: "jsonpath", Path: "$.id"}, true},
		{model.Assertion{Type: "jsonpath", Path: "$.id", Value: "42"}, true},
		{model.Assertion{Type: "jsonpath", Path: "$.name", Op: "matches", Value: "^Wid"}, true},
		{model.Assertion{Type: "jsonpath", Path: "$.tags", Value: `["a","b"]`}, true},
		{model.Assertion{Type: "jsonpath", Path: "$.owner.email", Op: "neq", Value: "a@b.c"}, true},
		{model.Assertion{Type: "jsonpath", Path: "$.nothing", Value: "null"}, true},
		{model.Assertion{Type: "jsonpath", Path: "$.missing", Op: "notexists"}, true},
		{model.Assertion{Type: "jsonpath", Path: "$.missing"}, false},
		{model.Assertion{Type: "jsonpath", Path: "$[bad"}, false},
		{model.Assertion{Type: "duration", Op: "lt", Value: "500"}, true},
		{model.Assertion{Type: "duration", Op: "lt", Value: "100"}, false},
		{model.Assertion{Type: "body", Op: "contains", Value: "Widget"}, true},
		{model.Assertion{Type: "status", Op: "gt", Value: "abc"}, false},
		{model.Assertion{Type: "weird"}, false},
	}
	for _, c := range cases {
		got := Assert([]model.Assertion{c.a}, resp())[0]
		if got.Passed != c.pass {
			t.Errorf("%s: passed=%v error=%q, want passed=%v", got.Name, got.Passed, got.Error, c.pass)
		}
	}
	if r := Assert([]model.Assertion{{Type: "status", Value: "200"}}, nil); r[0].Error != "no response" {
		t.Errorf("nil response: %+v", r)
	}
	if n := Describe(model.Assertion{Type: "jsonpath", Path: "$.id"}); n != "jsonpath $.id exists" {
		t.Errorf("describe = %q", n)
	}
}

func TestNonJSONBody(t *testing.T) {
	r := Wrap(&protocol.Response{Status: 200, Body: []byte("<html>")})
	got := Assert([]model.Assertion{{Type: "jsonpath", Path: "$.a"}}, r)[0]
	if got.Passed || got.Error != "body is not JSON" {
		t.Fatalf("got %+v", got)
	}
}

func TestExtract(t *testing.T) {
	vars, problems := Extract([]model.Extractor{
		{Var: "id", From: "jsonpath", Path: "$.id"},
		{Var: "email", From: "jsonpath", Path: "$.owner.email"},
		{Var: "rid", From: "header", Path: "X-Request-Id"},
		{Var: "code", From: "status"},
		{Var: "word", From: "regex", Path: `"name": "(\w+)"`},
		{Var: "gone", From: "jsonpath", Path: "$.nope"},
	}, resp())
	want := map[string]string{"id": "42", "email": "x@y.z", "rid": "req-9", "code": "201", "word": "Widget"}
	if !reflect.DeepEqual(vars, want) {
		t.Fatalf("vars = %v", vars)
	}
	if len(problems) != 1 {
		t.Fatalf("problems = %v", problems)
	}
}
