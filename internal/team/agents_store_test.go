package team

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codeen15/pyro/internal/db"
	"github.com/codeen15/pyro/internal/db/dbtest"
)

var testSecret = bytes.Repeat([]byte("s"), 32)

func newAgentStore(t *testing.T) *Store {
	t.Helper()
	st, err := NewStore(context.Background(), dbtest.Open(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentKey(testSecret); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestAgentDirectAddAndList(t *testing.T) {
	ctx := context.Background()
	st := newAgentStore(t)
	a, err := st.AddDirectAgent(ctx, " zeta ", "10.0.0.2:7979", "secret-token", true, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == "" || a.Name != "zeta" || a.Mode != "direct" || a.Address != "10.0.0.2:7979" || !a.InsecureTLS || a.CreatedBy != "u1" || a.Created.IsZero() {
		t.Fatalf("agent = %+v", a)
	}
	if _, err := st.AddDirectAgent(ctx, "alpha", "https://a.example.com", "t2", false, "u1"); err != nil {
		t.Fatal(err)
	}
	list, err := st.Agents(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "alpha" || list[1].Name != "zeta" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	tok, err := st.AgentToken(ctx, a.ID)
	if err != nil || tok != "secret-token" {
		t.Fatalf("token = %q, %v", tok, err)
	}
	var row agentRow
	if err := st.db.NewSelect().Model(&row).Where("id = ?", a.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if row.TokenEnc == "" || strings.Contains(row.TokenEnc, "secret-token") || row.TokenEnc == "secret-token" {
		t.Fatalf("token_enc = %q", row.TokenEnc)
	}
}

func TestAgentConnect(t *testing.T) {
	ctx := context.Background()
	st := newAgentStore(t)
	a, tok, err := st.AddConnectAgent(ctx, "edge", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if a.Mode != "connect" || !strings.HasPrefix(tok, connectTokenPrefix) {
		t.Fatalf("agent %+v token %q", a, tok)
	}
	got, ok, err := st.AgentByToken(ctx, tok)
	if err != nil || !ok || got.ID != a.ID {
		t.Fatalf("by token = %+v %v %v", got, ok, err)
	}
	if _, ok, err := st.AgentByToken(ctx, tok+"x"); err != nil || ok {
		t.Fatalf("wrong token found: %v %v", ok, err)
	}
	if _, ok, _ := st.AgentByToken(ctx, ""); ok {
		t.Fatal("empty token found")
	}
	var row agentRow
	if err := st.db.NewSelect().Model(&row).Where("id = ?", a.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if row.TokenEnc != "" || row.TokenHash == tok || row.TokenHash == "" {
		t.Fatalf("row = %+v", row)
	}
	if _, err := st.AgentToken(ctx, a.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("AgentToken on connect agent = %v, want ErrInvalid", err)
	}
}

func TestAgentValidationAndDuplicates(t *testing.T) {
	ctx := context.Background()
	st := newAgentStore(t)
	if _, err := st.AddDirectAgent(ctx, "a", "host:1", "t", false, "u"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDirectAgent(ctx, "a", "host:2", "t", false, "u"); !errors.Is(err, ErrExists) {
		t.Fatalf("dup direct = %v", err)
	}
	if _, _, err := st.AddConnectAgent(ctx, "A", "u"); !errors.Is(err, ErrExists) {
		t.Fatalf("dup connect = %v", err)
	}
	for _, tc := range []struct{ name, addr string }{
		{"", "host:1"}, {"   ", "host:1"}, {strings.Repeat("x", 65), "host:1"},
		{"ok", ""}, {"ok", "nohostport"}, {"ok", "ftp://h:1"}, {"ok", "http://"},
	} {
		if _, err := st.AddDirectAgent(ctx, tc.name, tc.addr, "t", false, "u"); !errors.Is(err, ErrInvalid) {
			t.Errorf("AddDirectAgent(%q, %q) = %v, want ErrInvalid", tc.name, tc.addr, err)
		}
	}
	if _, err := st.AddDirectAgent(ctx, "ok", "host:1", "", false, "u"); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty token = %v", err)
	}
	if _, _, err := st.AddConnectAgent(ctx, "", "u"); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty connect name = %v", err)
	}
}

func TestAgentUpdate(t *testing.T) {
	ctx := context.Background()
	st := newAgentStore(t)
	a, _ := st.AddDirectAgent(ctx, "one", "h:1", "tok1", false, "u")
	other, _ := st.AddDirectAgent(ctx, "two", "h:2", "tok2", false, "u")
	name, addr, tok, yes := "uno", "https://h:9", "tok9", true
	got, err := st.UpdateAgent(ctx, a.ID, &name, &addr, &tok, &yes)
	if err != nil || got.Name != "uno" || got.Address != "https://h:9" || !got.InsecureTLS {
		t.Fatalf("update = %+v %v", got, err)
	}
	if v, _ := st.AgentToken(ctx, a.ID); v != "tok9" {
		t.Fatalf("token = %q", v)
	}
	// nil leaves fields alone
	got, err = st.UpdateAgent(ctx, a.ID, nil, nil, nil, nil)
	if err != nil || got.Name != "uno" || got.Address != "https://h:9" {
		t.Fatalf("noop update = %+v %v", got, err)
	}
	if v, _ := st.AgentToken(ctx, a.ID); v != "tok9" {
		t.Fatalf("token changed: %q", v)
	}
	if _, err := st.UpdateAgent(ctx, a.ID, &other.Name, nil, nil, nil); !errors.Is(err, ErrExists) {
		t.Fatalf("rename to taken = %v", err)
	}
	same := "uno"
	if _, err := st.UpdateAgent(ctx, a.ID, &same, nil, nil, nil); err != nil {
		t.Fatalf("rename to self = %v", err)
	}
	bad := "nope"
	if _, err := st.UpdateAgent(ctx, a.ID, nil, &bad, nil, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad address = %v", err)
	}
	if _, err := st.UpdateAgent(ctx, "missing", &name, nil, nil, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing = %v", err)
	}
	c, _, _ := st.AddConnectAgent(ctx, "conn", "u")
	if _, err := st.UpdateAgent(ctx, c.ID, nil, nil, &tok, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("token on connect = %v", err)
	}
	if _, err := st.UpdateAgent(ctx, c.ID, nil, &addr, nil, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("address on connect = %v", err)
	}
	if _, err := st.UpdateAgent(ctx, c.ID, nil, nil, nil, &yes); !errors.Is(err, ErrInvalid) {
		t.Fatalf("insecureTLS on connect = %v", err)
	}
}

func TestAgentRotate(t *testing.T) {
	ctx := context.Background()
	st := newAgentStore(t)
	a, old, _ := st.AddConnectAgent(ctx, "edge", "u")
	fresh, err := st.RotateAgentToken(ctx, a.ID)
	if err != nil || fresh == old {
		t.Fatalf("rotate = %q %v", fresh, err)
	}
	if _, ok, _ := st.AgentByToken(ctx, old); ok {
		t.Fatal("old token still works")
	}
	if got, ok, _ := st.AgentByToken(ctx, fresh); !ok || got.ID != a.ID {
		t.Fatal("new token not found")
	}
	d, _ := st.AddDirectAgent(ctx, "d", "h:1", "t", false, "u")
	if _, err := st.RotateAgentToken(ctx, d.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("rotate direct = %v", err)
	}
	if _, err := st.RotateAgentToken(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rotate missing = %v", err)
	}
}

// An update writes only the columns it changes: a rename that read the row
// before a concurrent rotate (or touch) can't write the old token hash (or
// last seen) back.
func TestAgentUpdateKeepsConcurrentWrites(t *testing.T) {
	ctx := context.Background()
	st := newAgentStore(t)
	a, old, _ := st.AddConnectAgent(ctx, "edge", "u")
	stale, err := st.loadAgent(ctx, st.db, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := st.RotateAgentToken(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_700_000_000, 0)
	if err := st.TouchAgent(ctx, a.ID, at); err != nil {
		t.Fatal(err)
	}
	// The rename's write, from the row it read before the rotate.
	stale.Name = "edge-2"
	if err := saveAgent(ctx, st.db, stale, []string{"name"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.AgentByToken(ctx, old); ok {
		t.Fatal("the rename reinstated the revoked token")
	}
	got, ok, _ := st.AgentByToken(ctx, fresh)
	if !ok || got.Name != "edge-2" {
		t.Fatalf("after rename: %+v %v", got, ok)
	}
	if !got.LastSeen.Equal(at) {
		t.Fatalf("the rename lost last seen: %v", got.LastSeen)
	}
}

func TestAgentTouchAndDelete(t *testing.T) {
	ctx := context.Background()
	st := newAgentStore(t)
	a, _ := st.AddDirectAgent(ctx, "d", "h:1", "t", false, "u")
	if !a.LastSeen.IsZero() {
		t.Fatalf("new agent LastSeen = %v", a.LastSeen)
	}
	at := time.Unix(1_700_000_000, 0)
	if err := st.TouchAgent(ctx, a.ID, at); err != nil {
		t.Fatal(err)
	}
	list, _ := st.Agents(ctx)
	if len(list) != 1 || !list[0].LastSeen.Equal(at) {
		t.Fatalf("list = %+v", list)
	}
	if err := st.DeleteAgent(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.Agents(ctx); len(list) != 0 {
		t.Fatalf("after delete = %+v", list)
	}
	if err := st.DeleteAgent(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice = %v", err)
	}
	if _, err := st.AgentToken(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("token of deleted = %v", err)
	}
}

func TestAgentTokenAfterSecretChange(t *testing.T) {
	ctx := context.Background()
	d := dbtest.Open(t)
	st, err := NewStore(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentKey(testSecret); err != nil {
		t.Fatal(err)
	}
	a, err := st.AddDirectAgent(ctx, "d", "h:1", "tok", false, "u")
	if err != nil {
		t.Fatal(err)
	}
	st2, err := NewStore(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if err := st2.SetAgentKey(bytes.Repeat([]byte("z"), 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := st2.AgentToken(ctx, a.ID); !errors.Is(err, errTokenUnreadable) {
		t.Fatalf("err = %v, want errTokenUnreadable", err)
	}
	// Listing still works, and re-entering the token repairs it.
	if list, err := st2.Agents(ctx); err != nil || len(list) != 1 {
		t.Fatalf("list = %v %v", list, err)
	}
	tok := "again"
	if _, err := st2.UpdateAgent(ctx, a.ID, nil, nil, &tok, nil); err != nil {
		t.Fatal(err)
	}
	if v, err := st2.AgentToken(ctx, a.ID); err != nil || v != "again" {
		t.Fatalf("token = %q %v", v, err)
	}
}

func TestAgentKeyNotSet(t *testing.T) {
	ctx := context.Background()
	st, err := NewStore(ctx, dbtest.Open(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDirectAgent(ctx, "d", "h:1", "t", false, "u"); err == nil {
		t.Fatal("AddDirectAgent without key succeeded")
	}
}

func TestAgentsMigrationOnExistingDatabase(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "pyro.db")
	d, err := db.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	st, err := NewStore(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentKey(testSecret); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDirectAgent(ctx, "keep", "h:1", "t", false, "u"); err != nil {
		t.Fatal(err)
	}
	d.Close()
	st, err = Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	list, err := st.Agents(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "keep" {
		t.Fatalf("after reopen = %+v %v", list, err)
	}
}
