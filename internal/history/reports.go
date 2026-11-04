package history

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"time"

	"github.com/uptrace/bun"

	"github.com/codeen15/pyro/pkg/engine/report"
)

// Saved collection runs (reports) for the SQA team. The list combines them
// with saved load test runs.

type reportRow struct {
	bun.BaseModel `bun:"table:reports"`
	ID            string  `bun:"id,pk"`
	Workspace     string  `bun:"workspace,notnull"`
	Title         string  `bun:"title,notnull"`
	Target        string  `bun:"target,notnull"`
	Environment   string  `bun:"environment,notnull,default:''"`
	Status        string  `bun:"status,notnull"`
	Started       int64   `bun:"started,notnull"` // unix milliseconds
	Duration      float64 `bun:"duration,notnull"`
	Requests      int     `bun:"requests,notnull"`
	Failed        int     `bun:"failed,notnull"`
	Tests         int     `bun:"tests,notnull"`
	TestsFailed   int     `bun:"tests_failed,notnull"`
	RunBy         string  `bun:"run_by,notnull,default:''"`
	RunByName     string  `bun:"run_by_name,notnull,default:''"`
	Data          []byte  `bun:"data,notnull"` // gzip JSON of report.RunReport
}

// ReportItem is a row in the reports list (a collection run or a load test).
type ReportItem struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"` // run | load
	Title       string    `json:"title"`
	Target      string    `json:"target"`
	Environment string    `json:"environment,omitempty"`
	Status      string    `json:"status"`
	StartedAt   time.Time `json:"startedAt"`
	Duration    float64   `json:"duration"`
	RunBy       string    `json:"runBy,omitempty"`
	// Collection runs.
	Requests    int `json:"requests,omitempty"`
	Failed      int `json:"failed,omitempty"`
	Tests       int `json:"tests,omitempty"`
	TestsFailed int `json:"testsFailed,omitempty"`
	// Load tests.
	P95       float64 `json:"p95,omitempty"`
	ErrorRate float64 `json:"errorRate,omitempty"`
	MaxVUs    int     `json:"maxVUs,omitempty"`
}

// Who ran something.
type Who struct {
	ID, Name string
}

// AddReport saves a finished collection run.
func (s *Store) AddReport(ctx context.Context, workspace string, r *report.RunReport, by Who) error {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(r); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	row := &reportRow{
		ID: r.ID, Workspace: noNUL(workspace), Title: noNUL(r.Title), Target: noNUL(r.Target), Environment: noNUL(r.Environment),
		Status: r.Status, Started: r.StartedAt.UnixMilli(), Duration: r.Duration, Requests: r.Totals.Requests, Failed: r.Totals.RequestsFailed,
		Tests: r.Totals.Tests, TestsFailed: r.Totals.TestsFailed, RunBy: by.ID, RunByName: noNUL(by.Name), Data: buf.Bytes(),
	}
	_, err := s.db.NewInsert().Model(row).Exec(ctx)
	return err
}

// Report loads a saved collection run.
func (s *Store) Report(ctx context.Context, workspace, id string) (*report.RunReport, error) {
	var row reportRow
	err := s.db.NewSelect().Model(&row).Where("workspace = ? AND id = ?", workspace, id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(row.Data))
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	var r report.RunReport
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	r.ID = row.ID
	return &r, nil
}

// PreviousReport finds the run of the same target just before id.
func (s *Store) PreviousReport(ctx context.Context, workspace, id string) (*report.RunReport, error) {
	var cur reportRow
	if err := s.db.NewSelect().Model(&cur).Column("target", "started").Where("workspace = ? AND id = ?", workspace, id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var prevID string
	err := s.db.NewSelect().Model((*reportRow)(nil)).Column("id").
		Where("workspace = ? AND target = ? AND started < ?", workspace, cur.Target, cur.Started).
		Order("started DESC").Limit(1).Scan(ctx, &prevID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.Report(ctx, workspace, prevID)
}

// DeleteReport removes a saved collection run.
func (s *Store) DeleteReport(ctx context.Context, workspace, id string) error {
	res, err := s.db.NewDelete().Model((*reportRow)(nil)).Where("workspace = ? AND id = ?", workspace, id).Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ReportFilter narrows the reports list.
type ReportFilter struct {
	Kind   string // run | load | "" for both
	Target string
	Limit  int
}

// Reports lists collection runs and load tests together, newest first.
func (s *Store) Reports(ctx context.Context, workspace string, f ReportFilter) ([]ReportItem, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 200
	}
	out := []ReportItem{}
	if f.Kind != "load" {
		var rows []reportRow
		q := s.db.NewSelect().Model(&rows).ExcludeColumn("data").Where("workspace = ?", workspace).Order("started DESC").Limit(int64(f.Limit))
		if f.Target != "" {
			q = q.Where("target = ?", f.Target)
		}
		if err := q.Scan(ctx); err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, ReportItem{ID: r.ID, Kind: "run", Title: r.Title, Target: r.Target, Environment: r.Environment, Status: r.Status,
				StartedAt: time.UnixMilli(r.Started), Duration: r.Duration, RunBy: r.RunByName, Requests: r.Requests, Failed: r.Failed, Tests: r.Tests, TestsFailed: r.TestsFailed})
		}
	}
	if f.Kind != "run" {
		var rows []loadRunRow
		q := s.db.NewSelect().Model(&rows).ExcludeColumn("result").Where("workspace = ?", workspace).Order("started DESC").Limit(int64(f.Limit))
		if f.Target != "" {
			q = q.Where("test = ?", f.Target)
		}
		if err := q.Scan(ctx); err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, ReportItem{ID: r.ID, Kind: "load", Title: r.Name, Target: r.Test, Environment: r.Environment, Status: r.Status,
				StartedAt: time.UnixMilli(r.Started), Duration: r.Duration, RunBy: r.RunByName, Requests: int(r.Requests), P95: r.P95, ErrorRate: r.ErrorRate, MaxVUs: r.MaxVUs})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}
