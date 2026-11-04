package report

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/codeen15/pyro/pkg/engine"
	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/runner"
	"github.com/codeen15/pyro/pkg/engine/script"
)

// RunReport is a collection run kept for later: what ran, where, by whom,
// and every result, with secrets masked and bodies trimmed.
type RunReport struct {
	ID          string    `json:"id,omitempty"`
	Title       string    `json:"title"`  // collection or folder name
	Target      string    `json:"target"` // its path
	Project     string    `json:"project,omitempty"`
	Environment string    `json:"environment,omitempty"`
	RunBy       string    `json:"runBy,omitempty"`
	DataFile    string    `json:"dataFile,omitempty"`
	Version     string    `json:"version,omitempty"` // Pyro version
	StartedAt   time.Time `json:"startedAt"`
	Duration    float64   `json:"duration"` // seconds
	Status      string    `json:"status"`   // passed | failed | stopped
	Totals      Totals    `json:"totals"`

	Summary *runner.Summary `json:"summary"`
}

// Totals counts a run's outcome.
type Totals struct {
	Iterations     int `json:"iterations"`
	Requests       int `json:"requests"`
	RequestsFailed int `json:"requestsFailed"`
	Skipped        int `json:"skipped"`
	Tests          int `json:"tests"`
	TestsFailed    int `json:"testsFailed"`
}

// PassRate is the share of requests that passed, 0–100.
func (t Totals) PassRate() float64 {
	ran := t.Requests - t.Skipped
	if ran <= 0 {
		return 100
	}
	return 100 * float64(ran-t.RequestsFailed) / float64(ran)
}

// Meta is what a report knows besides the results.
type Meta struct {
	Title, Target, Project, Environment, RunBy, DataFile, Version string
}

// MaxBody caps each response body kept in a report.
const MaxBody = 64 << 10

// Masked replaces secret values in a report.
const Masked = "••••••"

// sensitiveHeaders always have their values masked.
var sensitiveHeaders = map[string]bool{
	"authorization": true, "proxy-authorization": true, "cookie": true, "set-cookie": true,
	"x-api-key": true, "api-key": true, "x-auth-token": true,
}

// NewRunReport builds a report from a finished run. secrets are values to
// mask wherever they appear (variables marked secret); sensitive headers
// are masked too.
func NewRunReport(m Meta, s *runner.Summary, secrets []string) *RunReport {
	r := &RunReport{
		Title: m.Title, Target: m.Target, Project: m.Project, Environment: m.Environment, RunBy: m.RunBy,
		DataFile: m.DataFile, Version: m.Version, StartedAt: s.StartedAt, Duration: s.Duration.Seconds(),
	}
	mask := newMasker(secrets)
	clean := *s
	clean.Results = make([]runner.ItemResult, len(s.Results))
	for i, ir := range s.Results {
		clean.Results[i] = maskItem(ir, mask)
	}
	r.Summary = &clean
	r.Totals = totals(&clean)
	switch {
	case s.Stopped != "" && s.Stopped != "bail":
		r.Status = "stopped"
	case r.Totals.RequestsFailed > 0:
		r.Status = "failed"
	default:
		r.Status = "passed"
	}
	return r
}

func totals(s *runner.Summary) Totals {
	t := Totals{Iterations: s.Iterations}
	for _, ir := range s.Results {
		t.Requests++
		if ir.Note != "" || ir.Result == nil || ir.Result.Skipped {
			t.Skipped++
			continue
		}
		if !ir.Passed() {
			t.RequestsFailed++
		}
		for _, c := range ir.Result.Tests {
			t.Tests++
			if !c.Passed {
				t.TestsFailed++
			}
		}
	}
	return t
}

type masker struct{ r *strings.Replacer }

func newMasker(secrets []string) masker {
	// Longest first, so a secret containing another is masked whole.
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	var pairs []string
	seen := map[string]bool{}
	for _, v := range secrets {
		if len(v) < 4 || seen[v] { // very short values would mask ordinary text
			continue
		}
		seen[v] = true
		pairs = append(pairs, v, Masked)
	}
	if len(pairs) == 0 {
		return masker{}
	}
	return masker{strings.NewReplacer(pairs...)}
}

func (m masker) str(s string) string {
	if m.r == nil {
		return s
	}
	return m.r.Replace(s)
}

func (m masker) headers(h []model.KeyValue) []model.KeyValue {
	if h == nil {
		return nil
	}
	out := make([]model.KeyValue, len(h))
	for i, kv := range h {
		kv.Value = m.str(kv.Value)
		if sensitiveHeaders[strings.ToLower(kv.Key)] {
			// Keep the scheme ("Bearer ") so the report still says how it signed in.
			if sp := strings.IndexByte(kv.Value, ' '); sp > 0 && sp < 16 && strings.ToLower(kv.Key) != "cookie" && strings.ToLower(kv.Key) != "set-cookie" {
				kv.Value = kv.Value[:sp+1] + Masked
			} else {
				kv.Value = Masked
			}
		}
		out[i] = kv
	}
	return out
}

func maskItem(ir runner.ItemResult, m masker) runner.ItemResult {
	if ir.Result == nil {
		return ir
	}
	res := *ir.Result
	if res.Sent != nil {
		sent := *res.Sent
		sent.URL = m.str(sent.URL)
		sent.Headers = m.headers(sent.Headers)
		sent.Query = m.headers(sent.Query)
		if sent.Body != nil {
			b := *sent.Body
			b.Content, b.Variables = m.str(b.Content), m.str(b.Variables)
			if b.Fields != nil {
				b.Fields = m.headers(b.Fields)
			}
			sent.Body = &b
		}
		sent.Auth = nil // the resolved credentials, never needed in a report
		res.Sent = &sent
	}
	if res.Response != nil {
		resp := *res.Response
		resp.Headers = m.headers(resp.Headers)
		resp.Cookies = nil
		body := resp.Body
		if len(body) > MaxBody {
			body = body[:MaxBody]
			resp.BodyTruncated = true
		}
		resp.Body = []byte(m.str(string(body)))
		res.Response = &resp
	}
	if res.Extracted != nil {
		ex := make(map[string]string, len(res.Extracted))
		for k, v := range res.Extracted {
			ex[k] = m.str(v)
		}
		res.Extracted = ex
	}
	if res.Logs != nil {
		logs := make([]script.Log, len(res.Logs))
		for i, l := range res.Logs {
			l.Message = m.str(l.Message)
			logs[i] = l
		}
		res.Logs = logs
	}
	ir.Result = &res
	return ir
}

// Change is a test (or a whole request) whose outcome changed between runs.
type Change struct {
	Path string `json:"path"`
	Name string `json:"name"` // request name, or "request name › test name"
	Now  string `json:"now"`  // passed | failed
}

// Comparison lists what changed from an earlier run of the same target.
type Comparison struct {
	Against      string    `json:"against"` // the earlier report's ID
	AgainstStart time.Time `json:"againstStartedAt"`
	NewlyFailing []Change  `json:"newlyFailing"`
	NewlyPassing []Change  `json:"newlyPassing"`
	Added        []string  `json:"added"`   // requests only in the new run
	Removed      []string  `json:"removed"` // requests only in the earlier run
}

// outcomes maps "path\x00test" (test "" = the request itself) to whether
// it passed in every iteration.
func outcomes(r *RunReport) (map[string]bool, map[string]string) {
	pass, names := map[string]bool{}, map[string]string{}
	set := func(key, name string, ok bool) {
		if prev, seen := pass[key]; seen {
			ok = prev && ok
		}
		pass[key], names[key] = ok, name
	}
	for _, ir := range r.Summary.Results {
		if ir.Note != "" || ir.Result == nil || ir.Result.Skipped {
			continue
		}
		set(ir.Path+"\x00", ir.Name, ir.Passed())
		for _, c := range ir.Result.Tests {
			set(ir.Path+"\x00"+c.Name, ir.Name+" › "+c.Name, c.Passed)
		}
	}
	return pass, names
}

// Compare reports what changed from old to cur.
func Compare(old, cur *RunReport) *Comparison {
	c := &Comparison{Against: old.ID, AgainstStart: old.StartedAt, NewlyFailing: []Change{}, NewlyPassing: []Change{}, Added: []string{}, Removed: []string{}}
	was, _ := outcomes(old)
	now, names := outcomes(cur)
	keys := make([]string, 0, len(now))
	for k := range now {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		path := k[:strings.IndexByte(k, 0)]
		before, existed := was[k]
		switch {
		case !existed:
			if strings.HasSuffix(k, "\x00") {
				c.Added = append(c.Added, path)
			}
		case before && !now[k]:
			c.NewlyFailing = append(c.NewlyFailing, Change{path, names[k], "failed"})
		case !before && now[k]:
			c.NewlyPassing = append(c.NewlyPassing, Change{path, names[k], "passed"})
		}
	}
	for k := range was {
		if _, still := now[k]; !still && strings.HasSuffix(k, "\x00") {
			c.Removed = append(c.Removed, k[:len(k)-1])
		}
	}
	sort.Strings(c.Removed)
	return c
}

// Row is one request in one iteration, flattened for CSV and Excel.
type Row struct {
	Iteration         int
	Name, Path        string
	Method, URL       string
	Status            int
	TimeMS            float64
	Passed, Failed    int    // tests
	Result            string // passed | failed | skipped
	Problems          string // failed tests and errors
	ResponseSizeBytes int64
}

// Rows flattens a run report, one row per request per iteration.
func Rows(r *RunReport) []Row {
	out := make([]Row, 0, len(r.Summary.Results))
	for _, ir := range r.Summary.Results {
		row := Row{Iteration: ir.Iteration + 1, Name: ir.Name, Path: ir.Path, Result: "passed"}
		if ir.Note != "" || ir.Result == nil || ir.Result.Skipped {
			row.Result, row.Problems = "skipped", ir.Note
			out = append(out, row)
			continue
		}
		res := ir.Result
		if res.Sent != nil {
			row.Method, row.URL = res.Sent.Method, res.Sent.URL
		}
		if res.Response != nil {
			row.Status, row.ResponseSizeBytes = res.Response.Status, res.Response.Size
			row.TimeMS = float64(res.Response.Timings.Total.Microseconds()) / 1000
		}
		row.Problems = strings.Join(problems(res), "; ")
		for _, c := range res.Tests {
			if c.Passed {
				row.Passed++
			} else {
				row.Failed++
			}
		}
		if !ir.Passed() {
			row.Result = "failed"
		}
		out = append(out, row)
	}
	return out
}

func problems(res *engine.Result) []string {
	var p []string
	if res.Error != "" {
		p = append(p, res.Error)
	}
	p = append(p, res.ScriptErrors...)
	for _, c := range res.Tests {
		if !c.Passed {
			msg := c.Name
			if c.Error != "" {
				msg += ": " + c.Error
			}
			p = append(p, msg)
		}
	}
	return p
}

var csvHeader = []string{"Iteration", "Request", "Path", "Method", "URL", "Status", "Time (ms)", "Tests passed", "Tests failed", "Result", "Problems"}

// RunCSV is the results table, one row per request per iteration.
func RunCSV(r *RunReport) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("\xef\xbb\xbf") // so Excel opens it as UTF-8
	w := csv.NewWriter(&buf)
	_ = w.Write(csvHeader)
	for _, row := range Rows(r) {
		status := ""
		if row.Status > 0 {
			status = strconv.Itoa(row.Status)
		}
		_ = w.Write([]string{strconv.Itoa(row.Iteration), csvSafe(row.Name), csvSafe(row.Path), row.Method, csvSafe(row.URL), status,
			strconv.FormatFloat(row.TimeMS, 'f', 1, 64), strconv.Itoa(row.Passed), strconv.Itoa(row.Failed), row.Result, csvSafe(row.Problems)})
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// csvSafe stops spreadsheet apps from running a cell as a formula.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func fmtDuration(secs float64) string {
	d := time.Duration(secs * float64(time.Second))
	if secs < 1 {
		return fmtMS(secs * 1000)
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1f s", secs)
	}
	return d.Round(time.Second).String()
}
