package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pyrohq/pyro/pkg/engine/load"
	"github.com/pyrohq/pyro/pkg/engine/model"
)

func newAgent(t *testing.T, name string) (*Server, *httptest.Server) {
	s := &Server{Token: "secret", Name: name, Version: "test", TickInterval: 50 * time.Millisecond}
	hs := httptest.NewServer(s.Handler())
	t.Cleanup(hs.Close)
	return s, hs
}

func target(t *testing.T) (*httptest.Server, *atomic.Int64) {
	var hits atomic.Int64
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/upload" {
			b := make([]byte, 64)
			n, _ := r.Body.Read(b)
			if string(b[:n]) != "file-contents" {
				w.WriteHeader(400)
			}
		}
		time.Sleep(2 * time.Millisecond)
	}))
	t.Cleanup(hs.Close)
	return hs, &hits
}

func plan(t *testing.T, y string) *load.Plan {
	p, err := load.ParsePlan([]byte(y), nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func resolver(base string) func(string) (load.Target, error) {
	return func(p string) (load.Target, error) {
		r := &model.Request{Kind: model.KindHTTP, Name: p, URL: "{{base}}/" + p}
		if p == "upload" {
			r.Method = "POST"
			r.Body = &model.Body{Type: model.BodyBinaryFile, File: "data/f.bin"}
		}
		return load.Target{Path: p, Request: r, Chain: []*model.Collection{{Variables: []model.Variable{{Key: "base", Value: base}}}}}, nil
	}
}

func TestTwoAgentsMergedResults(t *testing.T) {
	tg, hits := target(t)
	_, a1 := newAgent(t, "one")
	_, a2 := newAgent(t, "two")
	root := t.TempDir()
	if err := writeFile(root+"/data/f.bin", "file-contents"); err != nil {
		t.Fatal(err)
	}
	p := plan(t, `
name: two agents
scenarios:
  s:
    executor: constant-arrival-rate
    rate: 200
    duration: 1s
    preAllocatedVUs: 10
    flow: [ { request: ok }, { request: upload } ]
thresholds:
  http_req_failed: ["rate==0"]
  http_reqs: ["count>=380"]
`)
	var ticks atomic.Int64
	test, err := load.Start(p, load.Options{
		Resolve: resolver(tg.URL), FileRoot: root, TickInterval: 100 * time.Millisecond,
		OnTick:  func(load.Tick) { ticks.Add(1) },
		Workers: []load.Worker{NewClient(a1.URL, "secret", false), NewClient(strings.Replace(a2.URL, "http://", "ws://", 1), "secret", false)},
	})
	if err != nil {
		t.Fatal(err)
	}
	res := test.Wait()
	if res.Status != load.StatusPassed {
		t.Fatalf("%s: %s %+v %+v", res.Status, res.Reason, res.Thresholds, res.Errors)
	}
	got := res.Metrics["http_reqs"].Value
	if int64(got) != hits.Load() || got < 380 || got > 420 {
		t.Errorf("merged %v requests, target saw %d (want ~400)", got, hits.Load())
	}
	if len(res.Agents) != 2 || res.Agents[0].Requests == 0 || res.Agents[1].Requests == 0 ||
		res.Agents[0].Requests+res.Agents[1].Requests != int64(got) {
		t.Errorf("agents %+v", res.Agents)
	}
	if d := res.Metrics["http_req_duration"]; d.Count != int64(got) || d.P95 < 2 {
		t.Errorf("duration %+v", d)
	}
	if ticks.Load() < 5 {
		t.Errorf("only %d ticks", ticks.Load())
	}
}

func TestAgentAuthAndBusy(t *testing.T) {
	tg, _ := target(t)
	srv, a := newAgent(t, "one")
	ctx := context.Background()
	if _, err := NewClient(a.URL, "wrong", false).Info(ctx); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("wrong token: %v", err)
	}
	in, err := NewClient(a.URL, "secret", false).Info(ctx)
	if err != nil || in.Name != "one" || in.Protocol != Protocol || in.Busy {
		t.Fatalf("info %+v %v", in, err)
	}

	p := plan(t, `
name: long
scenarios:
  s: { executor: constant-vus, vus: 2, duration: 1m, flow: [ { request: ok }, { think: 10ms } ] }
`)
	test, err := load.Start(p, load.Options{Resolve: resolver(tg.URL), Workers: []load.Worker{NewClient(a.URL, "secret", false)}})
	if err != nil {
		t.Fatal(err)
	}
	if !srv.Info().Busy {
		t.Error("agent should be busy")
	}
	_, err = load.Start(p, load.Options{Resolve: resolver(tg.URL), Workers: []load.Worker{NewClient(a.URL, "secret", false)}})
	if err == nil || !strings.Contains(err.Error(), "busy") {
		t.Errorf("second controller: %v", err)
	}

	// Abort drops the connection; the agent must abort its job and free up.
	time.Sleep(200 * time.Millisecond)
	test.Abort()
	res := test.Wait()
	if res.Status != load.StatusStopped {
		t.Errorf("status %s", res.Status)
	}
	deadline := time.Now().Add(5 * time.Second)
	for srv.Info().Busy {
		if time.Now().After(deadline) {
			t.Fatal("agent still busy after the controller aborted")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAgentGracefulStop(t *testing.T) {
	tg, _ := target(t)
	_, a := newAgent(t, "one")
	p := plan(t, `
name: stop
scenarios:
  s: { executor: constant-vus, vus: 3, duration: 1m, flow: [ { request: ok }, { think: 10ms } ] }
`)
	test, err := load.Start(p, load.Options{Resolve: resolver(tg.URL), TickInterval: 100 * time.Millisecond, Workers: []load.Worker{NewClient(a.URL, "secret", false)}})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	test.Stop()
	res := test.Wait()
	if res.Status != load.StatusStopped || res.Duration > 5 || res.Metrics["http_reqs"].Value == 0 {
		t.Errorf("status %s after %.1fs with %v requests", res.Status, res.Duration, res.Metrics["http_reqs"].Value)
	}
	if res.Agents[0].State != "done" {
		t.Errorf("agent %+v", res.Agents[0])
	}
}

func TestAgentMissingPlugin(t *testing.T) {
	_, a := newAgent(t, "one")
	p := plan(t, `
name: tcp
scenarios:
  s: { executor: constant-vus, vus: 1, duration: 1s, flow: [ { request: tcp } ] }
`)
	resolve := func(string) (load.Target, error) {
		return load.Target{Path: "tcp", Request: &model.Request{Kind: "plugin:tcp", URL: "tcp://localhost:1"}}, nil
	}
	_, err := load.Start(p, load.Options{Resolve: resolve, Workers: []load.Worker{NewClient(a.URL, "secret", false)}})
	if err == nil || !strings.Contains(err.Error(), "plugin type protocol:tcp") {
		t.Errorf("err = %v", err)
	}
}
