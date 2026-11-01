// Package history records sent requests in SQLite (~/.pyro/pyro.db).
package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/codeen15/pyro/pkg/engine"
)

// maxStoredBody caps the response body kept per entry; full bodies of large
// downloads aren't worth the disk.
const maxStoredBody = 256 << 10

// keepPerWorkspace bounds history growth.
const keepPerWorkspace = 1000

var ErrNotFound = errors.New("history entry not found")

type Store struct {
	db *sql.DB
}

// Entry is a history list row.
type Entry struct {
	ID         int64     `json:"id"`
	At         time.Time `json:"at"`
	Path       string    `json:"path,omitempty"`
	Name       string    `json:"name,omitempty"`
	Method     string    `json:"method"`
	URL        string    `json:"url"`
	Status     int       `json:"status,omitempty"`
	DurationMS float64   `json:"durationMs"`
	Error      string    `json:"error,omitempty"`
}

func Open(file string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+file+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS history (
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
		CREATE INDEX IF NOT EXISTS history_ws_id ON history(workspace, id DESC);
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("init history db: %w", err)
	}
	st := &Store{db: db}
	if err := st.initLoadRuns(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init history db: %w", err)
	}
	if err := st.initCookies(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init history db: %w", err)
	}
	return st, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Add records a send result and prunes old entries for the workspace.
func (s *Store) Add(ctx context.Context, workspace, path, name string, r *engine.Result) (int64, error) {
	stored := *r
	if r.Response != nil && len(r.Response.Body) > maxStoredBody {
		resp := *r.Response
		resp.Body = resp.Body[:maxStoredBody]
		resp.BodyTruncated = true
		stored.Response = &resp
	}
	blob, err := json.Marshal(stored)
	if err != nil {
		return 0, err
	}
	var method, url string
	if r.Sent != nil {
		method, url = r.Sent.Method, r.Sent.URL
	}
	var status int
	var dur float64
	if r.Response != nil {
		status = r.Response.Status
		dur = float64(r.Response.Timings.Total.Microseconds()) / 1000
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO history (workspace, at, path, name, method, url, status, duration_ms, error, result)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		workspace, r.StartedAt.UnixMilli(), path, name, method, url, status, dur, r.Error, blob)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	_, err = s.db.ExecContext(ctx,
		`DELETE FROM history WHERE workspace = ? AND id <= (
			SELECT id FROM history WHERE workspace = ? ORDER BY id DESC LIMIT 1 OFFSET ?)`,
		workspace, workspace, keepPerWorkspace)
	return id, err
}

// List returns the newest entries first.
func (s *Store) List(ctx context.Context, workspace string, limit int) ([]Entry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, at, path, name, method, url, status, duration_ms, error
		 FROM history WHERE workspace = ? ORDER BY id DESC LIMIT ?`, workspace, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.Path, &e.Name, &e.Method, &e.URL, &e.Status, &e.DurationMS, &e.Error); err != nil {
			return nil, err
		}
		e.At = time.UnixMilli(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Get returns the full stored result as JSON.
func (s *Store) Get(ctx context.Context, workspace string, id int64) (json.RawMessage, error) {
	var blob []byte
	err := s.db.QueryRowContext(ctx, `SELECT result FROM history WHERE workspace = ? AND id = ?`, workspace, id).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return blob, err
}

func (s *Store) Clear(ctx context.Context, workspace string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM history WHERE workspace = ?`, workspace)
	return err
}
