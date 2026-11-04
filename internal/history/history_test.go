package history

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/codeen15/pyro/internal/db/dbtest"
	"github.com/codeen15/pyro/pkg/engine"
	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/protocol"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := New(context.Background(), dbtest.Open(t))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func result(status int, body string) *engine.Result {
	return &engine.Result{
		StartedAt: time.Now(),
		Sent:      &model.Request{Method: "GET", URL: "http://x/y"},
		Response:  &protocol.Response{Status: status, Body: []byte(body), Timings: protocol.Timings{Total: 1500 * time.Microsecond}},
	}
}

func TestAddListGet(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	id1, _ := s.Add(ctx, "/ws", "c/a", "A", result(200, "one"))
	if _, err := s.Add(ctx, "/ws", "", "", &engine.Result{StartedAt: time.Now(), Sent: &model.Request{Method: "POST", URL: "http://z"}, Error: "boom"}); err != nil {
		t.Fatal(err)
	}
	s.Add(ctx, "/other", "", "", result(500, ""))

	list, err := s.List(ctx, "/ws", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Error != "boom" || list[1].Status != 200 || list[1].DurationMS != 1.5 || list[1].Name != "A" {
		t.Fatalf("list = %+v", list)
	}

	raw, err := s.Get(ctx, "/ws", id1)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(raw, &got)
	if got["response"].(map[string]any)["status"].(float64) != 200 {
		t.Fatalf("get = %s", raw)
	}
	if _, err := s.Get(ctx, "/other", id1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace get: %v", err)
	}

	s.Clear(ctx, "/ws")
	if list, _ := s.List(ctx, "/ws", 10); len(list) != 0 {
		t.Fatalf("after clear: %+v", list)
	}
}

func TestBodyTruncatedAndPruned(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	id, _ := s.Add(ctx, "/ws", "", "", result(200, strings.Repeat("x", maxStoredBody+10)))
	raw, _ := s.Get(ctx, "/ws", id)
	var got struct {
		Response protocol.Response `json:"response"`
	}
	json.Unmarshal(raw, &got)
	if len(got.Response.Body) != maxStoredBody || !got.Response.BodyTruncated {
		t.Fatalf("stored body len = %d truncated=%v", len(got.Response.Body), got.Response.BodyTruncated)
	}

	for i := 0; i < keepPerWorkspace+5; i++ {
		s.Add(ctx, "/ws", "", "", result(200, ""))
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM history WHERE workspace = '/ws'`).Scan(&n)
	if n != keepPerWorkspace {
		t.Fatalf("kept %d rows, want %d", n, keepPerWorkspace)
	}
}

// PostgreSQL text can't hold NUL; such characters are dropped, not fatal.
func TestOddCharacters(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	r := result(200, "ok")
	r.Sent.URL = "http://x/\x00a'b\"c;--"
	if _, err := s.Add(ctx, "proj#user:1", "c/é ünï", "名前", r); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx, "proj#user:1", 10)
	if err != nil || len(list) != 1 || list[0].URL != "http://x/a'b\"c;--" || list[0].Name != "名前" {
		t.Fatalf("got %+v %v", list, err)
	}
}
