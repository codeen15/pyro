package runner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"github.com/codeen15/pyro/pkg/engine"
	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/protocol/httpdriver"
)

func target(t *testing.T) (*httptest.Server, *[]string) {
	var log []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log = append(log, r.URL.Path+" "+r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/login":
			fmt.Fprintf(w, `{"token":"tok-%s"}`, r.URL.Query().Get("user"))
		case "/fail":
			w.WriteHeader(500)
		default:
			w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(ts.Close)
	return ts, &log
}

func items(base string) []Item {
	chain := []*model.Collection{{Variables: []model.Variable{{Key: "base", Value: base}}}}
	return []Item{
		{Path: "c/login", Chain: chain, Request: &model.Request{Kind: model.KindHTTP, Name: "Login", URL: "{{base}}/login?user={{user}}",
			Extract: []model.Extractor{{Var: "token", From: "jsonpath", Path: "$.token"}}}},
		{Path: "c/me", Chain: chain, Request: &model.Request{Kind: model.KindHTTP, Name: "Me", URL: "{{base}}/me",
			Headers:    []model.KeyValue{{Key: "Authorization", Value: "{{token}}"}},
			Assertions: []model.Assertion{{Type: "status", Value: "200"}}}},
	}
}

func TestRunWithDataAndCarriedVariables(t *testing.T) {
	ts, log := target(t)
	e := engine.New(httpdriver.New(httpdriver.Options{}))
	var progress int
	sum := Run(context.Background(), e, items(ts.URL), Options{
		Data:     []map[string]string{{"user": "ann"}, {"user": "bob"}},
		OnResult: func(ItemResult) { progress++ },
	})
	want := []string{"/login ", "/me tok-ann", "/login ", "/me tok-bob"}
	if !reflect.DeepEqual(*log, want) {
		t.Fatalf("server saw %v", *log)
	}
	if sum.Iterations != 2 || sum.Requests != 4 || sum.Failed != 0 || sum.TestsPassed != 2 || !sum.OK() || progress != 4 {
		t.Fatalf("summary = %+v progress=%d", sum, progress)
	}
}

// Socket steps share a connection with HTTP steps in between: connect, log
// in over HTTP, send the token on the socket and wait for the reply, close.
func TestSocketStepsBetweenHTTP(t *testing.T) {
	ts, _ := target(t)
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		for {
			_, data, err := c.Read(context.Background())
			if err != nil {
				return
			}
			_ = c.Write(context.Background(), websocket.MessageText, []byte(`{"echo":`+string(data)+`}`))
		}
	}))
	t.Cleanup(ws.Close)
	wsURL := "ws" + strings.TrimPrefix(ws.URL, "http")
	chain := []*model.Collection{{Variables: []model.Variable{{Key: "base", Value: ts.URL}, {Key: "ws", Value: wsURL}}}}
	its := []Item{
		{Path: "c/open", Chain: chain, Request: &model.Request{Kind: model.KindWebSocket, Name: "Open", URL: "{{ws}}", Connection: "chat"}},
		{Path: "c/login", Chain: chain, Request: &model.Request{Kind: model.KindHTTP, Name: "Login", URL: "{{base}}/login?user={{user}}",
			Extract: []model.Extractor{{Var: "token", From: "jsonpath", Path: "$.token"}}}},
		{Path: "c/say", Chain: chain, Request: &model.Request{Kind: model.KindWebSocket, Name: "Say", Action: model.ActionSend, Connection: "chat",
			Messages:   []model.Message{{Content: `"{{token}}"`}},
			Wait:       &model.Wait{For: "message", JSONPath: "$.echo", Equals: "tok-{{user}}", Timeout: "2s"},
			Assertions: []model.Assertion{{Type: "jsonpath", Path: "$.echo", Value: "tok-{{user}}"}}}},
		{Path: "c/close", Chain: chain, Request: &model.Request{Kind: model.KindWebSocket, Name: "Close", Action: model.ActionClose, Connection: "chat"}},
	}
	e := engine.New(httpdriver.New(httpdriver.Options{}))
	sum := Run(context.Background(), e, its, Options{Data: []map[string]string{{"user": "ann"}, {"user": "bob"}}})
	if !sum.OK() || sum.Requests != 8 || sum.TestsPassed != 2 {
		for _, r := range sum.Results {
			t.Logf("%s: %q", r.Path, r.Result.Error)
		}
		t.Fatalf("summary = %+v", sum)
	}
}

func TestBailAndSetNextRequest(t *testing.T) {
	ts, log := target(t)
	e := engine.New(httpdriver.New(httpdriver.Options{}))
	chain := []*model.Collection{{}}
	its := []Item{
		{Path: "c/a", Chain: chain, Request: &model.Request{Name: "A", URL: ts.URL + "/a",
			Scripts: &model.Scripts{Post: `postman.setNextRequest("C")`}}},
		{Path: "c/b", Chain: chain, Request: &model.Request{Name: "B", URL: ts.URL + "/b"}},
		{Path: "c/c", Chain: chain, Request: &model.Request{Name: "C", URL: ts.URL + "/fail",
			Assertions: []model.Assertion{{Type: "status", Value: "200"}}}},
		{Path: "c/d", Chain: chain, Request: &model.Request{Name: "D", URL: ts.URL + "/d"}},
	}
	sum := Run(context.Background(), e, its, Options{Iterations: 3, Bail: true})
	if got := strings.Join(*log, ","); got != "/a ,/fail " {
		t.Fatalf("server saw %s", got)
	}
	if sum.OK() || !strings.Contains(sum.Stopped, "--bail") || sum.TestsFailed != 1 {
		t.Fatalf("summary = %+v", sum)
	}

	*log = nil
	its[1].Request.Scripts = &model.Scripts{Pre: `pm.execution.setNextRequest(null)`}
	its[0].Request.Scripts = nil
	sum = Run(context.Background(), e, its, Options{})
	if got := strings.Join(*log, ","); got != "/a ,/b " || !strings.Contains(sum.Stopped, "setNextRequest(null)") {
		t.Fatalf("stop: saw %s, summary %+v", got, sum)
	}
}

func TestLoadData(t *testing.T) {
	dir := t.TempDir()
	csvFile := filepath.Join(dir, "users.csv")
	os.WriteFile(csvFile, []byte("\xef\xbb\xbfuser,pass\nann,1\nbob,\"2,3\"\n"), 0o644)
	rows, err := LoadData(csvFile)
	if err != nil || len(rows) != 2 || rows[0]["user"] != "ann" || rows[1]["pass"] != "2,3" {
		t.Fatalf("csv: %v %v", rows, err)
	}
	jsonFile := filepath.Join(dir, "users.json")
	os.WriteFile(jsonFile, []byte(`[{"user":"ann","age":30,"tags":["a"]}]`), 0o644)
	rows, err = LoadData(jsonFile)
	if err != nil || rows[0]["age"] != "30" || rows[0]["tags"] != `["a"]` {
		t.Fatalf("json: %v %v", rows, err)
	}
	os.WriteFile(csvFile, []byte("only,header\n"), 0o644)
	if _, err := LoadData(csvFile); err == nil {
		t.Fatal("expected error for header-only CSV")
	}
}
