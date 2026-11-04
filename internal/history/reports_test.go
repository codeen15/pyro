package history

import (
	"context"
	"testing"
	"time"

	"github.com/codeen15/pyro/pkg/engine/load"
	"github.com/codeen15/pyro/pkg/engine/report"
	"github.com/codeen15/pyro/pkg/engine/runner"
)

func TestReportsSaveListCompareDelete(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	t0 := time.Now().Add(-time.Hour)
	mk := func(id, target string, at time.Time, status string) *report.RunReport {
		return &report.RunReport{ID: id, Title: "Shop", Target: target, StartedAt: at, Status: status,
			Totals: report.Totals{Requests: 3, RequestsFailed: 1}, Summary: &runner.Summary{StartedAt: at}}
	}
	for _, r := range []*report.RunReport{mk("a", "shop", t0, "passed"), mk("b", "shop", t0.Add(time.Minute), "failed"), mk("c", "other", t0.Add(2*time.Minute), "passed")} {
		if err := s.AddReport(ctx, "proj", r, Who{ID: "u1", Name: "Ada"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddLoadRunBy(ctx, "proj", "L1", "checkout", &load.Result{Name: "Checkout", Status: "passed", StartedAt: t0.Add(3 * time.Minute)}, Who{ID: "u1", Name: "Ada"}, "staging"); err != nil {
		t.Fatal(err)
	}

	all, err := s.Reports(ctx, "proj", ReportFilter{})
	if err != nil || len(all) != 4 || all[0].ID != "L1" || all[0].Kind != "load" || all[0].RunBy != "Ada" || all[0].Environment != "staging" || all[3].ID != "a" {
		t.Fatalf("list: %+v %v", all, err)
	}
	runs, _ := s.Reports(ctx, "proj", ReportFilter{Kind: "run", Target: "shop"})
	if len(runs) != 2 || runs[0].ID != "b" || runs[0].Failed != 1 {
		t.Fatalf("filtered: %+v", runs)
	}
	if other, _ := s.Reports(ctx, "elsewhere", ReportFilter{}); len(other) != 0 {
		t.Fatal("reports leak between projects")
	}

	got, err := s.Report(ctx, "proj", "b")
	if err != nil || got.Status != "failed" || got.Totals.Requests != 3 {
		t.Fatalf("load: %+v %v", got, err)
	}
	prev, err := s.PreviousReport(ctx, "proj", "b")
	if err != nil || prev.ID != "a" {
		t.Fatalf("previous: %+v %v", prev, err)
	}
	if _, err := s.PreviousReport(ctx, "proj", "a"); err != ErrNotFound {
		t.Fatalf("no previous: %v", err)
	}
	if err := s.DeleteReport(ctx, "proj", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Report(ctx, "proj", "a"); err != ErrNotFound {
		t.Fatalf("deleted: %v", err)
	}
}
