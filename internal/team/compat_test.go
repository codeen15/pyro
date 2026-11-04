package team

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/codeen15/pyro/internal/db"
	_ "modernc.org/sqlite"
)

// The schema Pyro 0.x (phases 13–14) created with hand-written SQL, before
// the Bun models. A data folder from then must keep working.
const olderSchema = `
CREATE TABLE IF NOT EXISTS users (
	id       TEXT PRIMARY KEY,
	email    TEXT NOT NULL UNIQUE COLLATE NOCASE,
	name     TEXT NOT NULL,
	pw_hash  TEXT NOT NULL DEFAULT '',
	provider TEXT NOT NULL DEFAULT 'local',
	subject  TEXT NOT NULL DEFAULT '',
	admin    INTEGER NOT NULL DEFAULT 0,
	disabled INTEGER NOT NULL DEFAULT 0,
	created  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
	hash    TEXT PRIMARY KEY,
	user    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	expires INTEGER NOT NULL,
	created INTEGER NOT NULL,
	ip      TEXT NOT NULL DEFAULT '',
	ua      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS sessions_user ON sessions(user);
CREATE TABLE IF NOT EXISTS workspaces (
	id         TEXT PRIMARY KEY,
	name       TEXT NOT NULL,
	kind       TEXT NOT NULL,
	created_by TEXT NOT NULL,
	created    INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS members (
	ws    TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
	user  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	role  TEXT NOT NULL,
	added INTEGER NOT NULL,
	PRIMARY KEY (ws, user)
);
CREATE INDEX IF NOT EXISTS members_user ON members(user);
CREATE TABLE IF NOT EXISTS projects (
	id         TEXT PRIMARY KEY,
	ws         TEXT NOT NULL REFERENCES workspaces(id),
	name       TEXT NOT NULL,
	created_by TEXT NOT NULL,
	created    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS projects_ws ON projects(ws);
CREATE TABLE IF NOT EXISTS invites (
	id         TEXT PRIMARY KEY,
	hash       TEXT NOT NULL UNIQUE,
	ws         TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
	email      TEXT NOT NULL DEFAULT '',
	role       TEXT NOT NULL,
	created_by TEXT NOT NULL,
	created    INTEGER NOT NULL,
	expires    INTEGER NOT NULL,
	max_uses   INTEGER NOT NULL DEFAULT 0,
	uses       INTEGER NOT NULL DEFAULT 0,
	revoked    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS invites_ws ON invites(ws);
CREATE TABLE IF NOT EXISTS audit (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	at      INTEGER NOT NULL,
	user    TEXT NOT NULL,
	ws      TEXT NOT NULL,
	project TEXT NOT NULL DEFAULT '',
	action  TEXT NOT NULL,
	target  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS audit_ws ON audit(ws, id DESC);`

func TestOpensDatabaseFromOlderPyro(t *testing.T) {
	file := filepath.Join(t.TempDir(), "pyro.db")
	raw, err := sql.Open("sqlite", "file:"+file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(olderSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO users (id, email, name, pw_hash, provider, admin, disabled, created) VALUES ('u1', 'Old@Example.com', 'Old', '', 'local', 1, 0, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO workspaces (id, name, kind, created_by, created) VALUES ('w1', 'Personal', 'personal', 'u1', 1);
		INSERT INTO members (ws, user, role, added) VALUES ('w1', 'u1', 'owner', 1);
		INSERT INTO projects (id, ws, name, created_by, created) VALUES ('p1', 'w1', 'Old project', 'u1', 1)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	d, err := db.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	st, err := NewStore(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	u, err := st.UserByEmail(ctx, "old@example.com")
	if err != nil || !u.Admin || u.Name != "Old" {
		t.Fatalf("old user: %+v %v", u, err)
	}
	ws, err := st.Workspaces(ctx, "u1")
	if err != nil || len(ws) != 1 || len(ws[0].Projects) != 1 || ws[0].Projects[0].Name != "Old project" {
		t.Fatalf("old workspaces: %+v %v", ws, err)
	}
	// New rows go in alongside the old ones.
	if _, err := st.CreateUser(ctx, NewUser{Email: "new@example.com", Password: "correct horse battery"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateWorkspace(ctx, "u1", "Team"); err != nil {
		t.Fatal(err)
	}
}
