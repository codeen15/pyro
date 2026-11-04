package report

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
	"unicode/utf8"
)

// RunHTML renders a run report as one self-contained, printable page (light,
// A4-friendly; "Save as PDF" from the browser's print dialog). Opened with
// #print it prints straight away.
func RunHTML(r *RunReport, cmp *Comparison) ([]byte, error) {
	type failure struct {
		Iteration int
		Name      string
		Path      string
		Problems  []string
	}
	var fails []failure
	for _, ir := range r.Summary.Results {
		if ir.Note != "" || ir.Result == nil || ir.Passed() {
			continue
		}
		fails = append(fails, failure{ir.Iteration + 1, ir.Name, ir.Path, problems(ir.Result)})
	}
	view := map[string]any{
		"R":        r,
		"Started":  r.StartedAt.Local().Format("2 January 2006, 15:04:05"),
		"Duration": fmtDuration(r.Duration),
		"PassRate": fmt.Sprintf("%.1f%%", r.Totals.PassRate()),
		"Passed":   r.Totals.Requests - r.Totals.Skipped - r.Totals.RequestsFailed,
		"Rows":     Rows(r),
		"Fails":    fails,
		"Cmp":      cmp,
		"Multi":    r.Totals.Iterations > 1,
	}
	var buf bytes.Buffer
	if err := runTmpl.Execute(&buf, view); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func shorten(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

var runTmpl = template.Must(template.New("run").Funcs(template.FuncMap{
	"ms":    func(v float64) string { return fmtMS(v) },
	"short": shorten,
	"join":  strings.Join,
	"add":   func(a, b int) int { return a + b },
	"statusText": func(s string) string {
		switch s {
		case "passed":
			return "✓ Passed"
		case "failed":
			return "✗ Failed"
		}
		return "■ Stopped"
	},
}).Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.R.Title}} – test report</title>
<style>
:root{color-scheme:light;--text:#1f2329;--muted:#5c636d;--rule:#e3e5e8;--ok:#1a7f45;--error:#c4231b;--warn:#8a6100;--head:#f6f7f8}
*{box-sizing:border-box}body{margin:0;background:#fff;color:var(--text);font:13px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:1000px;margin:0 auto;padding:32px 24px 48px}
h1{margin:0;font-size:22px}h2{font-size:15px;margin:28px 0 8px;page-break-after:avoid}
.status{display:inline-block;margin-left:10px;font-size:15px;font-weight:600;vertical-align:2px}
.status.passed{color:var(--ok)}.status.failed{color:var(--error)}.status.stopped{color:var(--warn)}
dl.meta{display:grid;grid-template-columns:max-content 1fr;gap:3px 16px;margin:14px 0 0}dl.meta dt{color:var(--muted)}dl.meta dd{margin:0}
.kpis{display:grid;grid-template-columns:repeat(auto-fit,minmax(140px,1fr));border:1px solid var(--rule);border-radius:6px;margin-top:20px;overflow:hidden}
.kpi{padding:10px 14px;border-right:1px solid var(--rule)}.kpi:last-child{border-right:0}.kpi span{display:block;color:var(--muted);font-size:12px}.kpi b{font-size:20px;font-weight:600}
table{width:100%;border-collapse:collapse;font-variant-numeric:tabular-nums}
th{text-align:left;font-weight:600;font-size:12px;background:var(--head);border-bottom:1px solid var(--rule);padding:6px 8px}
td{border-bottom:1px solid var(--rule);padding:5px 8px;vertical-align:top}td.n,th.n{text-align:right}tr{page-break-inside:avoid}
.mono{font-family:ui-monospace,Consolas,monospace;font-size:12px;word-break:break-all}.pass{color:var(--ok);font-weight:600}.fail{color:var(--error);font-weight:600}.skip,.muted{color:var(--muted)}
ul.problems{margin:2px 0 0;padding-left:18px}.cmp li{margin:2px 0}
.actions{position:fixed;top:12px;right:12px}.actions button{font:inherit;padding:6px 12px;border:1px solid var(--rule);border-radius:6px;background:#fff;cursor:pointer}
footer{margin-top:36px;color:var(--muted);font-size:11px}
@media print{.actions{display:none}main{padding:0;max-width:none}body{font-size:11px}@page{margin:14mm}}
</style></head><body>
<div class="actions"><button onclick="print()">Print / Save as PDF</button></div>
<main>
<h1>{{.R.Title}}<span class="status {{.R.Status}}">{{statusText .R.Status}}</span></h1>
<dl class="meta">
{{if .R.Project}}<dt>Project</dt><dd>{{.R.Project}}</dd>{{end}}
<dt>Ran</dt><dd class="mono">{{.R.Target}}</dd>
<dt>Environment</dt><dd>{{if .R.Environment}}{{.R.Environment}}{{else}}<span class="muted">none</span>{{end}}</dd>
{{if .R.RunBy}}<dt>Run by</dt><dd>{{.R.RunBy}}</dd>{{end}}
<dt>Started</dt><dd>{{.Started}}</dd>
<dt>Duration</dt><dd>{{.Duration}}</dd>
<dt>Iterations</dt><dd>{{.R.Totals.Iterations}}{{if .R.DataFile}} (data: {{.R.DataFile}}){{end}}</dd>
{{if .R.Summary.Stopped}}<dt>Stopped</dt><dd>{{.R.Summary.Stopped}}</dd>{{end}}
</dl>
<div class="kpis">
<div class="kpi"><span>Requests passed</span><b>{{.Passed}}</b></div>
<div class="kpi"><span>Requests failed</span><b class="{{if .R.Totals.RequestsFailed}}fail{{end}}">{{.R.Totals.RequestsFailed}}</b></div>
<div class="kpi"><span>Tests</span><b>{{.R.Totals.Tests}}</b></div>
<div class="kpi"><span>Tests failed</span><b class="{{if .R.Totals.TestsFailed}}fail{{end}}">{{.R.Totals.TestsFailed}}</b></div>
<div class="kpi"><span>Pass rate</span><b>{{.PassRate}}</b></div>
</div>
{{with .Cmp}}<h2>Compared with the previous run</h2>
<p class="muted">Run on {{.AgainstStart.Local.Format "2 January 2006, 15:04"}}.</p>
{{if or .NewlyFailing .NewlyPassing .Added .Removed}}<ul class="cmp">
{{range .NewlyFailing}}<li><span class="fail">✗ Now failing:</span> {{.Name}}</li>{{end}}
{{range .NewlyPassing}}<li><span class="pass">✓ Now passing:</span> {{.Name}}</li>{{end}}
{{range .Added}}<li><span class="muted">+ New request:</span> <span class="mono">{{.}}</span></li>{{end}}
{{range .Removed}}<li><span class="muted">− No longer run:</span> <span class="mono">{{.}}</span></li>{{end}}
</ul>{{else}}<p>No changes: the same tests passed and failed.</p>{{end}}{{end}}
{{if .Fails}}<h2>Failures ({{len .Fails}})</h2>
<table><thead><tr>{{if .Multi}}<th class="n">#</th>{{end}}<th>Request</th><th>What failed</th></tr></thead><tbody>
{{range .Fails}}<tr>{{if $.Multi}}<td class="n">{{.Iteration}}</td>{{end}}<td>{{.Name}}<div class="mono muted">{{.Path}}</div></td><td><ul class="problems">{{range .Problems}}<li>{{short . 400}}</li>{{end}}</ul></td></tr>{{end}}
</tbody></table>{{end}}
<h2>All requests</h2>
<table><thead><tr>{{if .Multi}}<th class="n">#</th>{{end}}<th>Request</th><th>Method</th><th class="n">Status</th><th class="n">Time</th><th class="n">Tests</th><th>Result</th></tr></thead><tbody>
{{range .Rows}}<tr>{{if $.Multi}}<td class="n">{{.Iteration}}</td>{{end}}<td>{{.Name}}<div class="mono muted">{{short .URL 120}}</div></td><td class="mono">{{.Method}}</td><td class="n mono">{{if .Status}}{{.Status}}{{end}}</td><td class="n mono">{{if .TimeMS}}{{ms .TimeMS}}{{end}}</td><td class="n">{{if or .Passed .Failed}}{{.Passed}}/{{add .Passed .Failed}}{{end}}</td><td class="{{if eq .Result "passed"}}pass{{else if eq .Result "failed"}}fail{{else}}skip{{end}}">{{if eq .Result "passed"}}✓ Passed{{else if eq .Result "failed"}}✗ Failed{{else}}Skipped{{end}}</td></tr>{{end}}
</tbody></table>
<footer>Generated by Pyro{{if .R.Version}} {{.R.Version}}{{end}}. Secret values and credentials are masked; response bodies are not included in this printout.</footer>
</main>
<script>if(location.hash==="#print")addEventListener("load",function(){setTimeout(print,200)})</script>
</body></html>`))
