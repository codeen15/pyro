package history

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// The tables Pyro created with hand-written SQL before the Bun models; an
// existing ~/.pyro/pyro.db must keep working.
const olderSchema = `
CREATE TABLE history (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	workspace   TEXT NOT NULL,
	at          INTEGER NOT NULL,
	path        TEXT NOT NULL DEFAULT '',
	name        TEXT NOT NULL DEFAULT '',
	method      TEXT NOT NULL,
	url         TEXT NOT NULL,
	status      INTEGER NOT NULL DEFAULT 0,
	duration_ms REAL NOT NULL DEFAULT 0,
	error       TEXT NOT NULL DEFAULT '',
	result      BLOB NOT NULL
);
CREATE INDEX history_ws_id ON history(workspace, id DESC);
CREATE TABLE loadruns (
	id TEXT PRIMARY KEY, workspace TEXT NOT NULL, test TEXT NOT NULL, name TEXT NOT NULL, started INTEGER NOT NULL,
	status TEXT NOT NULL, duration REAL NOT NULL, requests INTEGER NOT NULL, p95 REAL NOT NULL, error_rate REAL NOT NULL,
	max_vus INTEGER NOT NULL, result BLOB NOT NULL
);
CREATE INDEX loadruns_ws ON loadruns(workspace, started DESC);
CREATE TABLE cookies (workspace TEXT PRIMARY KEY, data BLOB NOT NULL);
INSERT INTO history (workspace, at, method, url, status, duration_ms, result) VALUES ('/ws', 1700000000000, 'GET', 'http://old', 200, 12.5, '{}');
INSERT INTO cookies (workspace, data) VALUES ('/ws', '[1]');`

func TestOpensDatabaseFromOlderPyro(t *testing.T) {
	file := filepath.Join(t.TempDir(), "pyro.db")
	raw, err := sql.Open("sqlite", "file:"+file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(olderSchema); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	s, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	list, err := s.List(ctx, "/ws", 10)
	if err != nil || len(list) != 1 || list[0].URL != "http://old" || list[0].DurationMS != 12.5 {
		t.Fatalf("old history: %+v %v", list, err)
	}
	if id, err := s.Add(ctx, "/ws", "", "", result(201, "new")); err != nil || id != list[0].ID+1 {
		t.Fatalf("add after upgrade: id %d, %v", id, err)
	}
	if c, err := s.LoadCookies(ctx, "/ws"); err != nil || string(c) != "[1]" {
		t.Fatalf("old cookies: %q %v", c, err)
	}
	if err := s.SaveCookies(ctx, "/ws", []byte("[2]")); err != nil {
		t.Fatal(err)
	}
}
