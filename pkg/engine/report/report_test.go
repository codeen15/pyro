package report

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/pyrohq/pyro/pkg/engine"
	"github.com/pyrohq/pyro/pkg/engine/check"
	"github.com/pyrohq/pyro/pkg/engine/protocol"
	"github.com/pyrohq/pyro/pkg/engine/runner"
)

func TestJUnit(t *testing.T) {
	sum := &runner.Summary{Iterations: 1, Duration: 2 * time.Second, Results: []runner.ItemResult{
		{Path: "c/login", Name: "Login", Result: &engine.Result{
			Response: &protocol.Response{Status: 200, Timings: protocol.Timings{Total: 120 * time.Millisecond}},
			Tests: []check.Result{
				{Name: "status ok", Passed: true},
				{Name: "has <token>", Passed: false, Error: `expected "a" & "b"`},
			},
		}},
		{Path: "c/down", Name: "Down", Result: &engine.Result{Error: "could not connect"}},
		{Path: "c/plain", Name: "Plain", Result: &engine.Result{Response: &protocol.Response{Status: 200}}},
		{Path: "c/ws", Name: "WS", Note: "skipped"},
	}}
	b, err := JUnit("demo", sum)
	if err != nil {
		t.Fatal(err)
	}
	var parsed junitSuites
	if err := xml.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("invalid XML: %v\n%s", err, b)
	}
	if parsed.Tests != 4 || parsed.Failures != 1 || parsed.Errors != 1 || len(parsed.Suites) != 4 {
		t.Fatalf("counts = %+v", parsed)
	}
	if parsed.Suites[0].Time != "0.120" || parsed.Suites[0].Cases[1].Failure.Message != `expected "a" & "b"` {
		t.Fatalf("suite 0 = %+v", parsed.Suites[0])
	}
	if !strings.HasPrefix(string(b), "<?xml") || parsed.Suites[3].Skipped != 1 {
		t.Fatalf("header/skip:\n%s", b)
	}
}
