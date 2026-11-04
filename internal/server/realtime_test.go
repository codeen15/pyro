package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func wsEcho(t *testing.T) *httptest.Server {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		for {
			typ, data, err := c.Read(context.Background())
			if err != nil {
				return
			}
			c.Write(context.Background(), typ, append([]byte("echo:"), data...))
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func subscribe(t *testing.T, ts *httptest.Server, topic string) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/events?token=" + testToken
	c, _, err := websocket.Dial(context.Background(), url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	b, _ := json.Marshal(map[string]string{"op": "subscribe", "topic": topic})
	c.Write(context.Background(), websocket.MessageText, b)
	time.Sleep(50 * time.Millisecond) // let the subscription register
	return c
}

func readEvent(t *testing.T, c *websocket.Conn) Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ev Event
	json.Unmarshal(data, &ev)
	return ev
}

func TestSessionOverEvents(t *testing.T) {
	target := wsEcho(t)
	ts := newTestServer(t)
	do(t, ts, "PUT", "/api/v1/environments/dev", `{"variables":[{"key":"room","value":"lobby"}]}`, true)
	do(t, ts, "PUT", "/api/v1/requests/demo/chat", `{"kind":"websocket","name":"Chat","url":"`+target.URL+`"}`, true)

	_, body := do(t, ts, "POST", "/api/v1/sessions", `{"path":"demo/chat","environment":"dev"}`, true)
	var opened struct {
		Error   string `json:"error"`
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	json.Unmarshal([]byte(body), &opened)
	if opened.Error != "" || opened.Session.ID == "" {
		t.Fatalf("open: %s", body)
	}
	id := opened.Session.ID
	ev := subscribe(t, ts, "session:"+id)

	res, body := do(t, ts, "POST", "/api/v1/sessions/"+id+"/send", `{"data":"join {{room}}"}`, true)
	if res.StatusCode != 200 || !strings.Contains(body, `"data":"join lobby"`) {
		t.Fatalf("send: %d %s", res.StatusCode, body)
	}
	got := map[string]bool{}
	for len(got) < 2 {
		e := readEvent(t, ev)
		m := e.Data.(map[string]any)
		got[m["direction"].(string)+":"+m["data"].(string)] = true
	}
	if !got["sent:join lobby"] || !got["received:echo:join lobby"] {
		t.Fatalf("events = %v", got)
	}

	_, body = do(t, ts, "GET", "/api/v1/sessions/"+id, "", true)
	if !strings.Contains(body, `"echo:join lobby"`) {
		t.Fatalf("state: %s", body)
	}
	if res, _ := do(t, ts, "DELETE", "/api/v1/sessions/"+id, "", true); res.StatusCode != http.StatusNoContent {
		t.Fatalf("close: %d", res.StatusCode)
	}
	if res, _ := do(t, ts, "GET", "/api/v1/sessions/"+id, "", true); res.StatusCode != http.StatusNotFound {
		t.Fatalf("after close: %d", res.StatusCode)
	}

	// Connection failures come back as an error, not a session.
	do(t, ts, "PUT", "/api/v1/requests/demo/dead", `{"kind":"websocket","name":"Dead","url":"ws://127.0.0.1:1"}`, true)
	_, body = do(t, ts, "POST", "/api/v1/sessions", `{"path":"demo/dead"}`, true)
	if !strings.Contains(body, `"error":"could not connect`) || strings.Contains(body, `"session"`) {
		t.Fatalf("dead: %s", body)
	}
}

func TestEventsRequireToken(t *testing.T) {
	ts := newTestServer(t)
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/events"
	if _, res, err := websocket.Dial(context.Background(), url, nil); err == nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %v", err)
	}
}

func TestRuns(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			time.Sleep(300 * time.Millisecond)
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()
	ts := newTestServer(t)
	do(t, ts, "PUT", "/api/v1/requests/demo/a", `{"name":"A","url":"`+target.URL+`/a","assertions":[{"type":"jsonpath","path":"$.ok","value":"true"}]}`, true)
	do(t, ts, "PUT", "/api/v1/requests/demo/b", `{"name":"B","url":"`+target.URL+`/b","assertions":[{"type":"status","value":"201"}]}`, true)

	_, body := do(t, ts, "POST", "/api/v1/runs", `{"path":"demo","iterations":2}`, true)
	var run struct {
		ID     string `json:"id"`
		Total  int    `json:"total"`
		Status string `json:"status"`
	}
	json.Unmarshal([]byte(body), &run)
	if run.ID == "" || run.Total != 4 {
		t.Fatalf("start: %s", body)
	}
	var final struct {
		Status  string `json:"status"`
		Done    int    `json:"done"`
		Summary struct {
			Requests    int `json:"requests"`
			TestsFailed int `json:"testsFailed"`
		} `json:"summary"`
	}
	for i := 0; i < 50; i++ {
		_, body = do(t, ts, "GET", "/api/v1/runs/"+run.ID, "", true)
		json.Unmarshal([]byte(body), &final)
		if final.Status != "running" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if final.Status != "finished" || final.Done != 4 || final.Summary.Requests != 4 || final.Summary.TestsFailed != 2 {
		t.Fatalf("final: %s", body)
	}

	// A cancelled run stops early.
	do(t, ts, "PUT", "/api/v1/requests/demo/a", `{"name":"A","url":"`+target.URL+`/slow"}`, true)
	_, body = do(t, ts, "POST", "/api/v1/runs", `{"path":"demo","iterations":20}`, true)
	json.Unmarshal([]byte(body), &run)
	ev := subscribe(t, ts, "run:"+run.ID)
	do(t, ts, "DELETE", "/api/v1/runs/"+run.ID, "", true)
	for {
		e := readEvent(t, ev)
		if e.Type == "finished" {
			if e.Data.(map[string]any)["status"] != "cancelled" {
				t.Fatalf("finished event: %+v", e)
			}
			break
		}
	}
}

// Regression: a new session's empty log must serialise as [], not null.
func TestNewSessionHasEmptyMessageList(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err == nil {
			c.Read(context.Background())
		}
	}))
	defer target.Close()
	ts := newTestServer(t)
	_, body := do(t, ts, "POST", "/api/v1/sessions", `{"request":{"kind":"websocket","url":"`+target.URL+`"}}`, true)
	if !strings.Contains(body, `"messages":[]`) {
		t.Fatalf("open: %s", body)
	}
}

// Socket steps by hand: open a named connection from a connect request,
// then send / ping / close requests run on it and show in its console.
func TestSocketStepsOnAnOpenConnection(t *testing.T) {
	echo := wsEcho(t)
	ts := newTestServer(t)
	wsURL := "ws" + strings.TrimPrefix(echo.URL, "http")
	do(t, ts, "PUT", "/api/v1/requests/demo/open", `{"kind":"websocket","name":"Open","url":"`+wsURL+`","connection":"chat"}`, true)
	do(t, ts, "PUT", "/api/v1/requests/demo/say", `{"kind":"websocket","name":"Say","action":"send","connection":"chat","messages":[{"content":"hi"}],"wait":{"for":"message","contains":"echo:hi"},"extract":[{"var":"reply","from":"regex","path":"(echo:.*)"}]}`, true)
	do(t, ts, "PUT", "/api/v1/requests/demo/ping", `{"kind":"websocket","name":"Ping","action":"ping","connection":"chat"}`, true)
	do(t, ts, "PUT", "/api/v1/requests/demo/bye", `{"kind":"websocket","name":"Bye","action":"close","connection":"chat"}`, true)

	// Before the connection is open, a step says so.
	_, out := do(t, ts, "POST", "/api/v1/send", `{"path":"demo/say"}`, true)
	if !strings.Contains(out, `connection \"chat\" isn't open`) {
		t.Fatalf("send before connect: %s", out)
	}
	_, out = do(t, ts, "POST", "/api/v1/sessions", `{"path":"demo/open"}`, true)
	var opened struct {
		Session struct {
			ID, Connection string
		}
	}
	_ = json.Unmarshal([]byte(out), &opened)
	if opened.Session.Connection != "chat" {
		t.Fatalf("open: %s", out)
	}

	res, out := do(t, ts, "POST", "/api/v1/send", `{"path":"demo/say"}`, true)
	if res.StatusCode != 200 || !strings.Contains(out, `"matched"`) || strings.Contains(out, `"error"`) {
		t.Fatalf("send step: %d %s", res.StatusCode, out)
	}
	if _, out := do(t, ts, "GET", "/api/v1/session/variables", "", true); !strings.Contains(out, `"reply":"echo:hi"`) {
		t.Fatalf("extracted value not kept: %s", out)
	}
	// The console shows the step's message and the reply.
	_, out = do(t, ts, "GET", "/api/v1/sessions/"+opened.Session.ID, "", true)
	if !strings.Contains(out, `"data":"hi"`) || !strings.Contains(out, `"data":"echo:hi"`) {
		t.Fatalf("console log: %s", out)
	}
	if _, out := do(t, ts, "POST", "/api/v1/send", `{"path":"demo/ping"}`, true); !strings.Contains(out, `"rttMs"`) && !strings.Contains(out, `"pong"`) {
		t.Fatalf("ping: %s", out)
	}
	if _, out := do(t, ts, "POST", "/api/v1/send", `{"path":"demo/bye"}`, true); strings.Contains(out, `"error"`) {
		t.Fatalf("close: %s", out)
	}
	for i := 0; i < 50; i++ {
		_, out = do(t, ts, "GET", "/api/v1/sessions/"+opened.Session.ID, "", true)
		if strings.Contains(out, `"closed":true`) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(out, `"closed":true`) {
		t.Fatalf("console after close: %s", out)
	}
	if _, out := do(t, ts, "POST", "/api/v1/send", `{"path":"demo/open"}`, true); !strings.Contains(out, "Connect button") {
		t.Fatalf("connect via send: %s", out)
	}
}
