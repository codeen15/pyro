package load

import (
	"strings"
	"testing"
	"time"
)

func TestScriptSteps(t *testing.T) {
	tg := newTarget(t, 0)
	res := run(t, tg.URL, `
name: scripted
scenarios:
  s:
    executor: per-vu-iterations
    vus: 2
    iterations: 3
    flow:
      - request: c/login
      - script: |
          const me = pyro.request("c/me", { name: "me (scripted)" });
          pyro.check(me, {
            "status is 200": (r) => r.status === 200,
            "has token": (r) => r.body.startsWith("t-"),
          });
          pyro.group("browse", () => {
            const r = pyro.http.get(pyro.vars.get("base") + "/ok", { name: "ok via http" });
            pyro.metrics.trend("ok_wait").add(r.timings.total);
            pyro.metrics.counter("scripted_calls").add(1);
            pyro.metrics.rate("ok_json").add(r.json().ok === true);
          });
          pyro.vars.set("seen", String(pyro.vu.iteration));
          if (pyro.vu.iteration === 2) pyro.fail("third iteration");
      - if:
          expr: seen == 1
          then: [ { request: c/ok, name: after-script } ]
thresholds:
  checks: ["rate==1"]
  scripted_calls: ["count==6"]
  ok_json: ["rate==1"]
`)
	if res.Status != StatusPassed {
		t.Fatalf("%s: %s %+v %+v", res.Status, res.Reason, res.Thresholds, res.Errors)
	}
	reqs := map[string]int64{}
	for _, r := range res.Requests {
		reqs[r.Request] = r.Count
	}
	if reqs["me (scripted)"] != 6 || reqs["ok via http"] != 6 || reqs["after-script"] != 2 {
		t.Errorf("requests: %v", reqs)
	}
	if res.Metrics["ok_wait"].Count != 6 || res.Metrics["group_duration"].Count != 6 {
		t.Errorf("custom trend %+v, groups %+v", res.Metrics["ok_wait"], res.Metrics["group_duration"])
	}
	if len(res.Errors) != 1 || res.Errors[0].Message != "script: third iteration" || res.Errors[0].Count != 2 {
		t.Errorf("errors: %+v", res.Errors)
	}
}

func TestScriptSafetyAndValidation(t *testing.T) {
	tg := newTarget(t, 0)
	res := run(t, tg.URL, `
name: unsafe
scenarios:
  s:
    executor: per-vu-iterations
    vus: 1
    iterations: 1
    flow:
      - script: |
          // Built at run time, so the host check before the test can't see it.
          const host = ["not-confirmed", "example", "com"].join(".");
          pyro.http.get("https://" + host + "/x");
`)
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Message, "isn't one of this load test's confirmed hosts") {
		t.Errorf("unconfirmed host: %+v", res.Errors)
	}
	// Script hosts and the plan's hosts: list feed the host confirmation.
	p, err := ParsePlan([]byte(`
hosts: [extra.example.com]
scenarios:
  s:
    executor: constant-vus
    vus: 1
    duration: 1s
    flow:
      - script: |
          pyro.http.post("https://api.example.com/orders", {sku: "A"});
`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if h := strings.Join(Hosts(p, resolver(tg.URL), nil, nil), ","); h != "api.example.com,extra.example.com" {
		t.Errorf("hosts: %s", h)
	}
	for _, bad := range []string{
		"script: \"pyro.request(\"\n",             // syntax error
		"script: 'pyro.request(\"c/missing\")'\n", // unknown request
	} {
		y := "scenarios:\n  s:\n    executor: constant-vus\n    vus: 1\n    duration: 1s\n    flow:\n      - " + bad
		if _, err := ParsePlan([]byte(y), func(p string) bool { return strings.HasPrefix(p, "c/ok") }); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}
}

func TestScriptStopsWithTest(t *testing.T) {
	tg := newTarget(t, 0)
	p, _ := ParsePlan([]byte(`
scenarios:
  s:
    executor: constant-vus
    vus: 2
    duration: 300ms
    gracefulStop: 200ms
    flow:
      - script: "while (true) {}"
`), nil)
	test, err := Start(p, Options{Resolve: resolver(tg.URL), TickInterval: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan *Result)
	go func() { done <- test.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("an endless script kept the test running")
	}
}
