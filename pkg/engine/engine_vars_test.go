package engine

import (
	"testing"

	"github.com/codeen15/pyro/pkg/engine/check"
	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/protocol"
	"github.com/codeen15/pyro/pkg/engine/vars"
)

func TestAssertionsAndExtractorsUseVariables(t *testing.T) {
	v := vars.NewSet()
	v.Runtime["orderId"] = "ord_7"
	v.Runtime["field"] = "id"
	resp := check.Wrap(&protocol.Response{Status: 200, Body: []byte(`{"id":"ord_7","name":"<b>x</b>"}`)})

	got := check.Assert(resolveAssertions([]model.Assertion{
		{Type: "jsonpath", Path: "$.{{field}}", Value: "{{orderId}}"},
		{Type: "jsonpath", Path: "$.id", Value: "{{missing}}"},
	}, v), resp)
	if !got[0].Passed || got[1].Passed {
		t.Fatalf("assertions: %+v", got)
	}

	v.Runtime["open"] = "<b>"
	res := &Result{}
	res.extract([]model.Extractor{{Var: "name", From: "boundary", Left: "{{open}}", Right: "</b>"}}, resp, v)
	if v.Runtime["name"] != "x" {
		t.Fatalf("extracted %q (%v)", v.Runtime["name"], res.Problems)
	}
}
