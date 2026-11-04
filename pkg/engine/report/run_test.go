package report

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/codeen15/pyro/pkg/engine"
	"github.com/codeen15/pyro/pkg/engine/check"
	"github.com/codeen15/pyro/pkg/engine/load"
	"github.com/codeen15/pyro/pkg/engine/metrics"
	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/protocol"
	"github.com/codeen15/pyro/pkg/engine/runner"
)

const secret = "sk_live_TOPSECRET"

func sampleRun(loginPasses bool) *runner.Summary {
	return &runner.Summary{Iterations: 1, StartedAt: time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC), Duration: 1500 * time.Millisecond, Results: []runner.ItemResult{
		{Path: "shop/login", Name: "Log in", Result: &engine.Result{
			Sent: &model.Request{Method: "POST", URL: "https://api.test/login?key=" + secret,
				Headers: []model.KeyValue{{Key: "Authorization", Value: "Bearer abc.def.ghi"}, {Key: "X-Trace", Value: "t1"}},
				Body:    &model.Body{Type: "json", Content: `{"password":"` + secret + `"}`}},
			Response: &protocol.Response{Status: 200, Timings: protocol.Timings{Total: 120 * time.Millisecond},
				Headers: []model.KeyValue{{Key: "Set-Cookie", Value: "sid=xyz"}}, Body: []byte(`{"echo":"` + secret + `"}`)},
			Tests: []check.Result{{Name: "status 200", Passed: true}, {Name: "has token", Passed: loginPasses, Error: "no token"}},
		}},
		{Path: "shop/items", Name: "=SUM(A1)", Result: &engine.Result{
			Sent:     &model.Request{Method: "GET", URL: "https://api.test/items"},
			Response: &protocol.Response{Status: 200, Body: bytes.Repeat([]byte("x"), MaxBody+500)},
			Tests:    []check.Result{{Name: "status 200", Passed: true}},
		}},
		{Path: "shop/socket", Name: "Socket", Note: "WebSocket requests aren't run by the runner"},
	}}
}

func TestRunReportMasksAndCounts(t *testing.T) {
	sum := sampleRun(false)
	r := NewRunReport(Meta{Title: "Shop", Target: "shop", Environment: "staging", RunBy: "Ada"}, sum, []string{secret, "ab"})
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), secret) || strings.Contains(string(b), "abc.def.ghi") || strings.Contains(string(b), "sid=xyz") {
		t.Fatalf("secret leaked into the report: %s", b)
	}
	if !strings.Contains(string(b), "Bearer "+Masked) {
		t.Fatal("the auth scheme should stay readable")
	}
	if !strings.Contains(string(b), `"t1"`) {
		t.Fatal("ordinary headers were masked")
	}
	// The live summary isn't touched.
	if sum.Results[0].Result.Sent.Headers[0].Value != "Bearer abc.def.ghi" {
		t.Fatal("masking changed the original results")
	}
	if got := len(r.Summary.Results[1].Result.Response.Body); got != MaxBody || !r.Summary.Results[1].Result.Response.BodyTruncated {
		t.Fatalf("body not trimmed: %d", got)
	}
	want := Totals{Iterations: 1, Requests: 3, RequestsFailed: 1, Skipped: 1, Tests: 3, TestsFailed: 1}
	if r.Totals != want || r.Status != "failed" {
		t.Fatalf("totals %+v status %s", r.Totals, r.Status)
	}
	if pr := r.Totals.PassRate(); pr != 50 {
		t.Fatalf("pass rate %v", pr)
	}
}

func TestCompare(t *testing.T) {
	old := NewRunReport(Meta{}, sampleRun(true), nil)
	old.ID = "old"
	cur := NewRunReport(Meta{}, sampleRun(false), nil)
	cur.Summary.Results = append(cur.Summary.Results, runner.ItemResult{Path: "shop/new", Name: "New", Result: &engine.Result{}})
	cur.Summary.Results = cur.Summary.Results[1:] // shop/login no longer ran… then add it back failing
	cur.Summary.Results = append(cur.Summary.Results, sampleRun(false).Results[0])
	c := Compare(old, cur)
	if c.Against != "old" || len(c.NewlyFailing) != 2 || c.NewlyFailing[0].Name != "Log in" || c.NewlyFailing[1].Name != "Log in › has token" {
		t.Fatalf("newly failing %+v", c.NewlyFailing)
	}
	if len(c.Added) != 1 || c.Added[0] != "shop/new" || len(c.Removed) != 0 || len(c.NewlyPassing) != 0 {
		t.Fatalf("compare %+v", c)
	}
	back := Compare(cur, old)
	if len(back.NewlyPassing) != 2 || len(back.Removed) != 1 {
		t.Fatalf("reverse %+v", back)
	}
}

func TestRunCSVAndXLSX(t *testing.T) {
	r := NewRunReport(Meta{Title: "Shop", Target: "shop", RunBy: "Ada"}, sampleRun(false), []string{secret})
	data, err := RunCSV(r)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))).ReadAll()
	if err != nil || len(rows) != 4 || rows[0][1] != "Request" {
		t.Fatalf("csv: %v %v", rows, err)
	}
	if rows[2][1] != "'=SUM(A1)" {
		t.Fatalf("formula not neutralised: %q", rows[2][1])
	}
	if rows[1][9] != "failed" || !strings.Contains(rows[1][10], "has token: no token") || rows[3][9] != "skipped" {
		t.Fatalf("rows: %v", rows[1:])
	}

	x, err := RunXLSX(r)
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(x))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.GetSheetList(); strings.Join(got, ",") != "Summary,Results,Failures" {
		t.Fatalf("sheets %v", got)
	}
	res, _ := f.GetRows("Results")
	if len(res) != 4 || res[1][1] != "Log in" || res[2][1] != "'=SUM(A1)" {
		t.Fatalf("results sheet %v", res)
	}
	fails, _ := f.GetRows("Failures")
	if len(fails) != 2 || fails[1][4] != "has token" {
		t.Fatalf("failures sheet %v", fails)
	}
	sum, _ := f.GetRows("Summary")
	if !strings.Contains(toText(sum), "Run by") || !strings.Contains(toText(sum), "failed") {
		t.Fatalf("summary %v", sum)
	}
}

func toText(rows [][]string) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(strings.Join(r, "|") + "\n")
	}
	return b.String()
}

func TestRunHTML(t *testing.T) {
	r := NewRunReport(Meta{Title: "Shop <script>", Target: "shop", RunBy: "Ada"}, sampleRun(false), []string{secret})
	old := NewRunReport(Meta{}, sampleRun(true), nil)
	html, err := RunHTML(r, Compare(old, r))
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	for _, want := range []string{"✗ Failed", "Failures (1)", "has token: no token", "Now failing", "Run by", "Save as PDF", "@media print"} {
		if !strings.Contains(s, want) {
			t.Errorf("HTML lacks %q", want)
		}
	}
	if strings.Contains(s, "Shop <script>") || strings.Contains(s, secret) {
		t.Fatal("unescaped title or secret in HTML")
	}
}

func TestLoadXLSXAndCSV(t *testing.T) {
	res := &load.Result{Name: "Checkout", Status: "passed", Duration: 60, MaxVUs: 50,
		Metrics:    map[string]metrics.Stat{"http_reqs": {Value: 1000, PerSecond: 16.7}, "http_req_duration": {Med: 40, P95: 120, P99: 200}, "http_req_failed": {Value: 0.01}},
		Requests:   []metrics.RequestStat{{Scenario: "buy", Request: "shop/items", Method: "GET", Count: 1000, Failed: 10, PerSec: 16.7, Duration: metrics.Stat{Avg: 50, Med: 40, P90: 100, P95: 120, P99: 200, Max: 400}}},
		Thresholds: []load.ThresholdResult{{Metric: "http_req_duration", Condition: "p(95)<500", Actual: 120, Passed: true}},
		Ticks:      []load.Tick{{Elapsed: 1, VUs: 10, RPS: 15, ErrorRate: 0.02}},
	}
	x, err := LoadXLSX(res, LoadMeta{Test: "checkout", RunBy: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	f, _ := excelize.OpenReader(bytes.NewReader(x))
	if got := strings.Join(f.GetSheetList(), ","); got != "Summary,Requests,Thresholds,Timeline" {
		t.Fatalf("sheets %s", got)
	}
	rq, _ := f.GetRows("Requests")
	if len(rq) != 2 || rq[1][1] != "shop/items" || rq[1][5] != "1" {
		t.Fatalf("requests sheet %v", rq)
	}
	tl, _ := f.GetRows("Timeline")
	if tl[1][3] != "2" {
		t.Fatalf("timeline error rate should be a percentage: %v", tl[1])
	}
	c, err := LoadCSV(res)
	if err != nil || !strings.Contains(string(c), "buy,shop/items,GET,1000,10,1.00") {
		t.Fatalf("load csv: %s %v", c, err)
	}
}
