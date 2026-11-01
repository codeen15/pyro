package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/codeen15/pyro/pkg/engine/load"
)

const loadRunsSchema = `
CREATE TABLE IF NOT EXISTS loadruns (
	id         TEXT PRIMARY KEY,
	workspace  TEXT NOT NULL,
	test       TEXT NOT NULL,
	name       TEXT NOT NULL,
	started    INTEGER NOT NULL,
	status     TEXT NOT NULL,
	duration   REAL NOT NULL,
	requests   INTEGER NOT NULL,
	p95        REAL NOT NULL,
	error_rate REAL NOT NULL,
	max_vus    INTEGER NOT NULL,
	result     BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS loadruns_ws ON loadruns(workspace, started DESC);`

// LoadRun is a saved load test run (list row).
type LoadRun struct {
	ID        string    `json:"id"`
	Test      string    `json:"test"`
	Name      string    `json:"name"`
	StartedAt time.Time `json:"startedAt"`
	Status    string    `json:"status"`
	Duration  float64   `json:"duration"`
	Requests  int64     `json:"requests"`
	P95       float64   `json:"p95"`
	ErrorRate float64   `json:"errorRate"`
	MaxVUs    int       `json:"maxVUs"`
}

func (s *Store) initLoadRuns() error {
	_, err := s.db.Exec(loadRunsSchema)
	return err
}

// AddLoadRun saves a finished load test.
func (s *Store) AddLoadRun(ctx context.Context, workspace, id, test string, r *load.Result) error {
	blob, err := json.Marshal(r)
	if err != nil {
		return err
	}
	m := r.Metrics
	_, err = s.db.ExecContext(ctx, `INSERT OR REPLACE INTO loadruns
		(id, workspace, test, name, started, status, duration, requests, p95, error_rate, max_vus, result)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, workspace, test, r.Name, r.StartedAt.UnixMilli(), r.Status, r.Duration,
		int64(m["http_reqs"].Value), m["http_req_duration"].P95, m["http_req_failed"].Value, r.MaxVUs, blob)
	return err
}

// LoadRuns lists saved runs, newest first; test filters by load test name.
func (s *Store) LoadRuns(ctx context.Context, workspace, test string, limit int) ([]LoadRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, test, name, started, status, duration, requests, p95, error_rate, max_vus
		FROM loadruns WHERE workspace = ? AND (? = '' OR test = ?) ORDER BY started DESC LIMIT ?`, workspace, test, test, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LoadRun{}
	for rows.Next() {
		var r LoadRun
		var started int64
		if err := rows.Scan(&r.ID, &r.Test, &r.Name, &started, &r.Status, &r.Duration, &r.Requests, &r.P95, &r.ErrorRate, &r.MaxVUs); err != nil {
			return nil, err
		}
		r.StartedAt = time.UnixMilli(started)
		out = append(out, r)
	}
	return out, rows.Err()
}

// LoadRunResult returns a saved run's full result.
func (s *Store) LoadRunResult(ctx context.Context, workspace, id string) (*load.Result, string, error) {
	var blob []byte
	var test string
	err := s.db.QueryRowContext(ctx, `SELECT test, result FROM loadruns WHERE workspace = ? AND id = ?`, workspace, id).Scan(&test, &blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	var r load.Result
	return &r, test, json.Unmarshal(blob, &r)
}

func (s *Store) DeleteLoadRun(ctx context.Context, workspace, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM loadruns WHERE workspace = ? AND id = ?`, workspace, id)
	return err
}
