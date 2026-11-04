package report

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"

	"github.com/xuri/excelize/v2"

	"github.com/codeen15/pyro/pkg/engine/load"
	"github.com/codeen15/pyro/pkg/engine/metrics"
)

// Excel workbooks for the SQA team: a Summary sheet, then one row per
// result, ready to filter, chart or paste into a test management tool.

type sheet struct {
	f        *excelize.File
	name     string
	row      int
	bold     int
	pass     int
	fail     int
	widths   map[int]float64
	numStyle int
}

func newBook() (*excelize.File, int, int, int, int) {
	f := excelize.NewFile()
	bold, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}, Fill: excelize.Fill{Type: "pattern", Color: []string{"F2F3F5"}, Pattern: 1}})
	pass, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Color: "1A7F45", Bold: true}})
	fail, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Color: "C4231B", Bold: true}})
	num, _ := f.NewStyle(&excelize.Style{NumFmt: 2}) // 0.00
	return f, bold, pass, fail, num
}

func (b *sheet) put(cells ...any) {
	b.row++
	for i, v := range cells {
		if s, ok := v.(string); ok {
			v = csvSafe(s) // no formulas from response data
			if l := float64(len(s)); l > b.widths[i] {
				b.widths[i] = l
			}
		}
		ref, _ := excelize.CoordinatesToCellName(i+1, b.row)
		_ = b.f.SetCellValue(b.name, ref, v)
	}
}

func (b *sheet) style(col, style int) {
	ref, _ := excelize.CoordinatesToCellName(col, b.row)
	_ = b.f.SetCellStyle(b.name, ref, ref, style)
}

func (b *sheet) header(cols ...string) {
	b.put(toAny(cols)...)
	first, _ := excelize.CoordinatesToCellName(1, b.row)
	last, _ := excelize.CoordinatesToCellName(len(cols), b.row)
	_ = b.f.SetCellStyle(b.name, first, last, b.bold)
	_ = b.f.SetPanes(b.name, &excelize.Panes{Freeze: true, YSplit: b.row, TopLeftCell: fmt.Sprintf("A%d", b.row+1), ActivePane: "bottomLeft"})
	_ = b.f.AutoFilter(b.name, first+":"+last, nil)
}

func (b *sheet) finish() {
	for col, w := range b.widths {
		name, _ := excelize.ColumnNumberToName(col + 1)
		w = min(max(w+2, 8), 70)
		_ = b.f.SetColWidth(b.name, name, name, w)
	}
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func book(f *excelize.File, name string, bold, pass, fail, num int) *sheet {
	if f.SheetCount == 1 && f.GetSheetName(0) == "Sheet1" {
		_ = f.SetSheetName("Sheet1", name)
	} else {
		_, _ = f.NewSheet(name)
	}
	return &sheet{f: f, name: name, bold: bold, pass: pass, fail: fail, numStyle: num, widths: map[int]float64{}}
}

func save(f *excelize.File) ([]byte, error) {
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), f.Close()
}

// RunXLSX is a run report as a workbook: Summary, Results and Failures.
func RunXLSX(r *RunReport) ([]byte, error) {
	f, bold, pass, fail, num := newBook()
	s := book(f, "Summary", bold, pass, fail, num)
	s.put("Test report", r.Title)
	s.style(1, bold)
	for _, kv := range [][2]any{
		{"Project", r.Project}, {"Ran", r.Target}, {"Environment", orNone(r.Environment)}, {"Run by", r.RunBy},
		{"Started", r.StartedAt.Local().Format("2006-01-02 15:04:05")}, {"Duration (s)", r.Duration}, {"Status", r.Status},
		{"Iterations", r.Totals.Iterations}, {"Data file", r.DataFile}, {"Requests", r.Totals.Requests},
		{"Requests failed", r.Totals.RequestsFailed}, {"Requests skipped", r.Totals.Skipped}, {"Tests", r.Totals.Tests},
		{"Tests failed", r.Totals.TestsFailed}, {"Pass rate (%)", r.Totals.PassRate()}, {"Pyro version", r.Version},
	} {
		if v, ok := kv[1].(string); ok && v == "" {
			continue
		}
		s.put(kv[0], kv[1])
		if kv[0] == "Status" {
			s.style(2, map[bool]int{true: pass, false: fail}[r.Status == "passed"])
		}
	}
	s.finish()

	res := book(f, "Results", bold, pass, fail, num)
	res.header(csvHeader...)
	for _, row := range Rows(r) {
		var status any = ""
		if row.Status > 0 {
			status = row.Status
		}
		res.put(row.Iteration, row.Name, row.Path, row.Method, row.URL, status, row.TimeMS, row.Passed, row.Failed, row.Result, row.Problems)
		switch row.Result {
		case "passed":
			res.style(10, pass)
		case "failed":
			res.style(10, fail)
		}
	}
	res.finish()

	fl := book(f, "Failures", bold, pass, fail, num)
	fl.header("Iteration", "Request", "Path", "Kind", "Test", "Message")
	for _, ir := range r.Summary.Results {
		if ir.Note != "" || ir.Result == nil {
			continue
		}
		if ir.Result.Error != "" {
			fl.put(ir.Iteration+1, ir.Name, ir.Path, "request", "", ir.Result.Error)
		}
		for _, e := range ir.Result.ScriptErrors {
			fl.put(ir.Iteration+1, ir.Name, ir.Path, "script", "", e)
		}
		for _, c := range ir.Result.Tests {
			if !c.Passed {
				fl.put(ir.Iteration+1, ir.Name, ir.Path, c.Source, c.Name, c.Error)
			}
		}
	}
	fl.finish()
	f.SetActiveSheet(0)
	return save(f)
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// LoadMeta is what a load test report knows besides the result.
type LoadMeta struct {
	Test, Project, Environment, RunBy, Version string
}

var loadRequestHeader = []string{"Scenario", "Request", "Method", "Requests", "Failed", "Error rate (%)", "Req/s", "Avg (ms)", "Median (ms)", "p90 (ms)", "p95 (ms)", "p99 (ms)", "Max (ms)"}

// LoadXLSX is a load test result as a workbook: Summary, Requests,
// Thresholds and Timeline (one row per second).
func LoadXLSX(r *load.Result, m LoadMeta) ([]byte, error) {
	f, bold, pass, fail, num := newBook()
	s := book(f, "Summary", bold, pass, fail, num)
	s.put("Load test report", r.Name)
	s.style(1, bold)
	met := r.Metrics
	d := met["http_req_duration"]
	for _, kv := range [][2]any{
		{"Load test", m.Test}, {"Project", m.Project}, {"Environment", orNone(m.Environment)}, {"Run by", m.RunBy},
		{"Started", r.StartedAt.Local().Format("2006-01-02 15:04:05")}, {"Duration (s)", r.Duration}, {"Status", r.Status},
		{"Reason", r.Reason}, {"Peak virtual users", r.MaxVUs}, {"Requests", int64(met["http_reqs"].Value)},
		{"Requests per second", met["http_reqs"].PerSecond}, {"Failed requests (%)", met["http_req_failed"].Value * 100},
		{"Response time median (ms)", d.Med}, {"Response time p95 (ms)", d.P95}, {"Response time p99 (ms)", d.P99},
		{"Iterations", int64(met["iterations"].Value)}, {"Pyro version", m.Version},
	} {
		if v, ok := kv[1].(string); ok && v == "" {
			continue
		}
		s.put(kv[0], kv[1])
		if kv[0] == "Status" {
			s.style(2, map[bool]int{true: pass, false: fail}[r.Status == "passed"])
		}
	}
	s.finish()

	rq := book(f, "Requests", bold, pass, fail, num)
	rq.header(loadRequestHeader...)
	for _, q := range r.Requests {
		rq.put(loadRequestRow(q)...)
	}
	rq.finish()

	th := book(f, "Thresholds", bold, pass, fail, num)
	th.header("Metric", "Condition", "Actual", "Result")
	for _, t := range r.Thresholds {
		result := "passed"
		if !t.Passed {
			result = "failed"
		}
		if t.NoData {
			result = "no data"
		}
		th.put(t.Metric, t.Condition, t.Actual, result)
		th.style(4, map[bool]int{true: pass, false: fail}[t.Passed])
	}
	th.finish()

	tl := book(f, "Timeline", bold, pass, fail, num)
	tl.header("Elapsed (s)", "Virtual users", "Req/s", "Failed (%)", "p50 (ms)", "p95 (ms)", "p99 (ms)")
	for _, t := range r.Ticks {
		tl.put(t.Elapsed, t.VUs, t.RPS, t.ErrorRate*100, t.Latency.P50, t.Latency.P95, t.Latency.P99)
	}
	tl.finish()
	f.SetActiveSheet(0)
	return save(f)
}

func loadRequestRow(q metrics.RequestStat) []any {
	rate := 0.0
	if q.Count > 0 {
		rate = 100 * float64(q.Failed) / float64(q.Count)
	}
	return []any{q.Scenario, q.Request, q.Method, q.Count, q.Failed, rate, q.PerSec, q.Duration.Avg, q.Duration.Med, q.Duration.P90, q.Duration.P95, q.Duration.P99, q.Duration.Max}
}

// LoadCSV is the per-request table of a load test.
func LoadCSV(r *load.Result) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("\xef\xbb\xbf")
	w := csv.NewWriter(&buf)
	_ = w.Write(loadRequestHeader)
	for _, q := range r.Requests {
		row := loadRequestRow(q)
		out := make([]string, len(row))
		for i, v := range row {
			switch x := v.(type) {
			case string:
				out[i] = csvSafe(x)
			case float64:
				out[i] = strconv.FormatFloat(x, 'f', 2, 64)
			default:
				out[i] = fmt.Sprint(x)
			}
		}
		_ = w.Write(out)
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}
