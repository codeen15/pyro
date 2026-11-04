package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/uptrace/bun"

	"github.com/codeen15/pyro/pkg/engine/load"
)

type loadRunRow struct {
	bun.BaseModel `bun:"table:loadruns"`
	ID            string  `bun:"id,pk"`
	Workspace     string  `bun:"workspace,notnull"`
	Test          string  `bun:"test,notnull"`
	Name          string  `bun:"name,notnull"`
	Started       int64   `bun:"started,notnull"` // unix milliseconds
	Status        string  `bun:"status,notnull"`
	Duration      float64 `bun:"duration,notnull"`
	Requests      int64   `bun:"requests,notnull"`
	P95           float64 `bun:"p95,notnull"`
	ErrorRate     float64 `bun:"error_rate,notnull"`
	MaxVUs        int     `bun:"max_vus,notnull"`
	Result        []byte  `bun:"result,notnull"`
}

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

// AddLoadRun saves a finished load test (replacing a run with the same id).
func (s *Store) AddLoadRun(ctx context.Context, workspace, id, test string, r *load.Result) error {
	blob, err := json.Marshal(r)
	if err != nil {
		return err
	}
	m := r.Metrics
	row := &loadRunRow{
		ID: id, Workspace: workspace, Test: test, Name: r.Name, Started: r.StartedAt.UnixMilli(), Status: r.Status, Duration: r.Duration,
		Requests: int64(m["http_reqs"].Value), P95: m["http_req_duration"].P95, ErrorRate: m["http_req_failed"].Value, MaxVUs: r.MaxVUs, Result: blob,
	}
	q := s.db.NewInsert().Model(row).On("CONFLICT (id) DO UPDATE")
	for _, c := range []string{"workspace", "test", "name", "started", "status", "duration", "requests", "p95", "error_rate", "max_vus", "result"} {
		q = q.Set(c + " = EXCLUDED." + c)
	}
	_, err = q.Exec(ctx)
	return err
}

// LoadRuns lists saved runs, newest first; test filters by load test name.
func (s *Store) LoadRuns(ctx context.Context, workspace, test string, limit int) ([]LoadRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows []loadRunRow
	q := s.db.NewSelect().Model(&rows).ExcludeColumn("result").Where("workspace = ?", workspace).Order("started DESC").Limit(int64(limit))
	if test != "" {
		q = q.Where("test = ?", test)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]LoadRun, 0, len(rows))
	for _, r := range rows {
		out = append(out, LoadRun{ID: r.ID, Test: r.Test, Name: r.Name, StartedAt: time.UnixMilli(r.Started), Status: r.Status, Duration: r.Duration,
			Requests: r.Requests, P95: r.P95, ErrorRate: r.ErrorRate, MaxVUs: r.MaxVUs})
	}
	return out, nil
}

// LoadRunResult returns a saved run's full result.
func (s *Store) LoadRunResult(ctx context.Context, workspace, id string) (*load.Result, string, error) {
	var row loadRunRow
	err := s.db.NewSelect().Model(&row).Column("test", "result").Where("workspace = ? AND id = ?", workspace, id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	var r load.Result
	return &r, row.Test, json.Unmarshal(row.Result, &r)
}

func (s *Store) DeleteLoadRun(ctx context.Context, workspace, id string) error {
	_, err := s.db.NewDelete().Model((*loadRunRow)(nil)).Where("workspace = ? AND id = ?", workspace, id).Exec(ctx)
	return err
}
