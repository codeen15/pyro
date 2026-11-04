// Package history records sent requests, saved load runs and cookie jars
// (SQLite in ~/.pyro/pyro.db locally; SQLite or PostgreSQL on a team server).
package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/codeen15/pyro/internal/db"
	"github.com/codeen15/pyro/pkg/engine"
)

// maxStoredBody caps the response body kept per entry; full bodies of large
// downloads aren't worth the disk.
const maxStoredBody = 256 << 10

// keepPerWorkspace bounds history growth.
const keepPerWorkspace = 1000

var ErrNotFound = errors.New("history entry not found")

type Store struct {
	db  *bun.DB
	own bool // close db with the store
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

type historyRow struct {
	bun.BaseModel `bun:"table:history"`
	ID            int64   `bun:"id,pk,autoincrement"`
	Workspace     string  `bun:"workspace,notnull"`
	At            int64   `bun:"at,notnull"` // unix milliseconds
	Path          string  `bun:"path,notnull,default:''"`
	Name          string  `bun:"name,notnull,default:''"`
	Method        string  `bun:"method,notnull"`
	URL           string  `bun:"url,notnull"`
	Status        int     `bun:"status,notnull,default:0"`
	DurationMS    float64 `bun:"duration_ms,notnull,default:0"`
	Error         string  `bun:"error,notnull,default:''"`
	Result        []byte  `bun:"result,notnull"`
}

// Open opens (creating if needed) a SQLite history database file.
func Open(file string) (*Store, error) {
	d, err := db.Open(file)
	if err != nil {
		return nil, err
	}
	st, err := New(context.Background(), d)
	if err != nil {
		d.Close()
		return nil, err
	}
	st.own = true
	return st, nil
}

// New uses an open database (SQLite or PostgreSQL), creating the tables.
func New(ctx context.Context, d *bun.DB) (*Store, error) {
	if err := db.Migrate(ctx, d, "history", migrations); err != nil {
		return nil, fmt.Errorf("init history db: %w", err)
	}
	return &Store{db: d}, nil
}

// migrations create the tables; a database made by an older Pyro already
// has them, with the same columns.
var migrations = []db.Migration{
	{Name: "1-tables", Up: func(ctx context.Context, tx bun.Tx) error {
		if err := db.CreateTables(ctx, tx, (*historyRow)(nil), (*loadRunRow)(nil), (*cookieRow)(nil)); err != nil {
			return err
		}
		if err := db.CreateIndex(ctx, tx, (*historyRow)(nil), "history_ws_id", "workspace", "id DESC"); err != nil {
			return err
		}
		return db.CreateIndex(ctx, tx, (*loadRunRow)(nil), "loadruns_ws", "workspace", "started DESC")
	}},
}

// Close closes the database if the store opened it.
func (s *Store) Close() error {
	if s.own {
		return s.db.Close()
	}
	return nil
}

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
	row := &historyRow{Workspace: noNUL(workspace), At: r.StartedAt.UnixMilli(), Path: noNUL(path), Name: noNUL(name), Error: noNUL(r.Error), Result: blob}
	if r.Sent != nil {
		row.Method, row.URL = noNUL(r.Sent.Method), noNUL(r.Sent.URL)
	}
	if r.Response != nil {
		row.Status = r.Response.Status
		row.DurationMS = float64(r.Response.Timings.Total.Microseconds()) / 1000
	}
	if _, err := s.db.NewInsert().Model(row).Returning("id").Exec(ctx); err != nil {
		return 0, err
	}
	cutoff := s.db.NewSelect().Model((*historyRow)(nil)).Column("id").
		Where("workspace = ?", workspace).Order("id DESC").Limit(1).Offset(keepPerWorkspace)
	_, err = s.db.NewDelete().Model((*historyRow)(nil)).Where("workspace = ?", workspace).Where("id <= (?)", cutoff).Exec(ctx)
	return row.ID, err
}

// noNUL drops NUL characters, which PostgreSQL text can't hold.
func noNUL(s string) string { return strings.ReplaceAll(s, "\x00", "") }

// List returns the newest entries first.
func (s *Store) List(ctx context.Context, workspace string, limit int) ([]Entry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var rows []historyRow
	err := s.db.NewSelect().Model(&rows).ExcludeColumn("result", "workspace").
		Where("workspace = ?", workspace).Order("id DESC").Limit(int64(limit)).Scan(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(rows))
	for _, r := range rows {
		out = append(out, Entry{ID: r.ID, At: time.UnixMilli(r.At), Path: r.Path, Name: r.Name, Method: r.Method, URL: r.URL, Status: r.Status, DurationMS: r.DurationMS, Error: r.Error})
	}
	return out, nil
}

// Get returns the full stored result as JSON.
func (s *Store) Get(ctx context.Context, workspace string, id int64) (json.RawMessage, error) {
	var blob []byte
	err := s.db.NewSelect().Model((*historyRow)(nil)).Column("result").Where("workspace = ? AND id = ?", workspace, id).Scan(ctx, &blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return blob, err
}

func (s *Store) Clear(ctx context.Context, workspace string) error {
	_, err := s.db.NewDelete().Model((*historyRow)(nil)).Where("workspace = ?", workspace).Exec(ctx)
	return err
}
