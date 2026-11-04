package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/codeen15/pyro/internal/history"
	"github.com/codeen15/pyro/internal/version"
	"github.com/codeen15/pyro/pkg/engine/load"
	"github.com/codeen15/pyro/pkg/engine/report"
	"github.com/codeen15/pyro/pkg/engine/runner"
)

// Test reports: every collection run is saved (secrets masked), listed
// with load test runs, compared with the previous run, and exported as
// printable HTML, Excel, CSV, JUnit or JSON.

// targetTitle names what a run covered: the collection, folder or request.
func (s *Server) targetTitle(p string) string {
	p = strings.Trim(p, "/")
	if !strings.Contains(p, "/") {
		if c, err := s.ws.ReadCollection(p); err == nil && c.Name != "" {
			return c.Name
		}
	} else if f, err := s.ws.ReadFolder(p); err == nil && f.Name != "" {
		return f.Name
	} else if r, err := s.ws.ReadRequest(p); err == nil && r.Name != "" {
		return r.Name
	}
	return p
}

// saveReport stores a finished run; it returns the report's ID ("" if
// there's no history database or saving failed).
func (s *Server) saveReport(m report.Meta, sum *runner.Summary, secrets []string, who Principal) string {
	if s.cfg.History == nil {
		return ""
	}
	rep := report.NewRunReport(m, sum, secrets)
	rep.ID = newID()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.cfg.History.AddReport(ctx, s.key, rep, history.Who{ID: who.ID, Name: who.Name}); err != nil {
		s.log.Warn("saving the run report failed", "err", err)
		return ""
	}
	return rep.ID
}

func (s *Server) needHistory(w http.ResponseWriter) bool {
	if s.cfg.History == nil {
		writeError(w, http.StatusServiceUnavailable, "reports need the history database, which isn't available")
		return false
	}
	return true
}

func (s *Server) listReports(w http.ResponseWriter, r *http.Request) {
	if !s.needHistory(w) {
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, err := s.cfg.History.Reports(r.Context(), s.key, history.ReportFilter{Kind: q.Get("kind"), Target: q.Get("target"), Limit: limit})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) getReport(w http.ResponseWriter, r *http.Request) {
	if !s.needHistory(w) {
		return
	}
	rep, err := s.cfg.History.Report(r.Context(), s.key, chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// compareReport compares a run with ?with=<id>, or the previous run of the
// same target.
func (s *Server) compareReport(w http.ResponseWriter, r *http.Request) {
	if !s.needHistory(w) {
		return
	}
	id := chi.URLParam(r, "id")
	cur, err := s.cfg.History.Report(r.Context(), s.key, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	var old *report.RunReport
	if with := r.URL.Query().Get("with"); with != "" && with != "previous" {
		old, err = s.cfg.History.Report(r.Context(), s.key, with)
	} else {
		old, err = s.cfg.History.PreviousReport(r.Context(), s.key, id)
	}
	if errors.Is(err, history.ErrNotFound) {
		w.WriteHeader(http.StatusNoContent) // the first run of this target
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report.Compare(old, cur))
}

var exportTypes = map[string]struct{ ext, mime string }{
	"html":  {"html", "text/html; charset=utf-8"},
	"xlsx":  {"xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
	"csv":   {"csv", "text/csv; charset=utf-8"},
	"junit": {"xml", "application/xml"},
	"json":  {"json", "application/json"},
}

// exportReport downloads a collection run or a load test run.
func (s *Server) exportReport(w http.ResponseWriter, r *http.Request) {
	if !s.needHistory(w) {
		return
	}
	format := r.URL.Query().Get("format")
	t, ok := exportTypes[format]
	if !ok {
		writeError(w, http.StatusBadRequest, "format must be html, xlsx, csv, junit or json")
		return
	}
	id := chi.URLParam(r, "id")
	var data []byte
	var title string
	var started time.Time
	rep, err := s.cfg.History.Report(r.Context(), s.key, id)
	switch {
	case err == nil:
		title, started = rep.Title, rep.StartedAt
		switch format {
		case "html":
			var cmp *report.Comparison
			if prev, err := s.cfg.History.PreviousReport(r.Context(), s.key, id); err == nil {
				cmp = report.Compare(prev, rep)
			}
			data, err = report.RunHTML(rep, cmp)
		case "xlsx":
			data, err = report.RunXLSX(rep)
		case "csv":
			data, err = report.RunCSV(rep)
		case "junit":
			data, err = report.JUnit(rep.Title, rep.Summary)
		case "json":
			data, err = report.JSONIndent(rep)
		}
	case errors.Is(err, history.ErrNotFound):
		var res *load.Result
		var test string
		if res, test, err = s.cfg.History.LoadRunResult(r.Context(), s.key, id); err != nil {
			s.fail(w, err)
			return
		}
		title, started = res.Name, res.StartedAt
		meta := report.LoadMeta{Test: test, Project: s.ws.Meta.Name, Version: version.Version}
		if row, ok := s.loadRunRow(r.Context(), id); ok {
			meta.RunBy, meta.Environment = row.RunBy, row.Environment
		}
		switch format {
		case "html":
			data, err = report.LoadHTML(res)
		case "xlsx":
			data, err = report.LoadXLSX(res, meta)
		case "csv":
			data, err = report.LoadCSV(res)
		case "json":
			data, err = report.LoadJSON(res)
		default:
			writeError(w, http.StatusBadRequest, "load test runs export as html, xlsx, csv or json")
			return
		}
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", t.mime)
	name := fmt.Sprintf("%s %s.%s", fileSafe(title), started.Local().Format("2006-01-02 1504"), t.ext)
	if format != "html" || r.URL.Query().Get("download") != "" {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	}
	_, _ = w.Write(data)
}

func (s *Server) loadRunRow(ctx context.Context, id string) (history.LoadRun, bool) {
	runs, err := s.cfg.History.LoadRuns(ctx, s.key, "", 200)
	if err != nil {
		return history.LoadRun{}, false
	}
	for _, r := range runs {
		if r.ID == id {
			return r, true
		}
	}
	return history.LoadRun{}, false
}

func fileSafe(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`"\/:*?<>|`, r) {
			return '-'
		}
		return r
	}, s)
	if s == "" {
		return "report"
	}
	return s
}

// uploadReport saves a run made elsewhere (pyro run --report-to): a
// collection run report, or a load test result.
func (s *Server) uploadReport(w http.ResponseWriter, r *http.Request) {
	if !s.needHistory(w) {
		return
	}
	var body struct {
		Kind        string            `json:"kind"` // run | load
		Report      *report.RunReport `json:"report,omitempty"`
		Test        string            `json:"test,omitempty"`
		Environment string            `json:"environment,omitempty"`
		Result      *load.Result      `json:"result,omitempty"`
	}
	if !decode(w, r, &body) {
		return
	}
	p := principal(r)
	who := history.Who{ID: p.ID, Name: p.Name}
	id := newID()
	switch {
	case body.Kind == "run" && body.Report != nil && body.Report.Summary != nil:
		// Mask again on our side: sensitive headers never get stored, whatever the client sent.
		rep := report.NewRunReport(report.Meta{
			Title: body.Report.Title, Target: body.Report.Target, Project: s.ws.Meta.Name, Environment: body.Report.Environment,
			RunBy: p.Name, DataFile: body.Report.DataFile, Version: body.Report.Version,
		}, body.Report.Summary, nil)
		if rep.Title == "" {
			rep.Title = rep.Target
		}
		rep.ID = id
		if err := s.cfg.History.AddReport(r.Context(), s.key, rep, who); err != nil {
			s.fail(w, err)
			return
		}
	case body.Kind == "load" && body.Result != nil:
		test := body.Test
		if test == "" {
			test = body.Result.Name
		}
		if err := s.cfg.History.AddLoadRunBy(r.Context(), s.key, id, test, body.Result, who, body.Environment); err != nil {
			s.fail(w, err)
			return
		}
	default:
		writeError(w, http.StatusBadRequest, `send {"kind":"run","report":…} or {"kind":"load","result":…}`)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id, "kind": body.Kind})
}

func (s *Server) deleteReport(w http.ResponseWriter, r *http.Request) {
	if !s.needHistory(w) {
		return
	}
	id := chi.URLParam(r, "id")
	err := s.cfg.History.DeleteReport(r.Context(), s.key, id)
	if errors.Is(err, history.ErrNotFound) {
		err = s.cfg.History.DeleteLoadRun(r.Context(), s.key, id)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
