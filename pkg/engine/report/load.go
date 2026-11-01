package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/codeen15/pyro/pkg/engine/load"
)

// LoadJSON renders a load test result.
func LoadJSON(r *load.Result) ([]byte, error) { return json.MarshalIndent(r, "", "  ") }

// Chart colours, validated with the dataviz palette checks against the
// iron surface (#0a0d10): one series hue, plus a one-hue ordinal ramp for
// latency percentiles (p50 → p99 gets brighter).
const (
	colSeries = "#00a0a6"
	colP50    = "#3e6c9b"
	colP95    = "#5b9ddf"
	colP99    = "#98d0ff"
)

type series struct {
	Name   string    `json:"name"`
	Color  string    `json:"color"`
	Values []float64 `json:"values"`
}

type chart struct {
	ID     string    `json:"id"`
	Title  string    `json:"-"`
	Unit   string    `json:"unit"`
	X      []float64 `json:"x"`
	Series []series  `json:"series"`
	// Refs are threshold lines drawn dashed.
	Refs []ref `json:"refs"`
	SVG  template.HTML
}

type ref struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
}

const (
	cw, ch           = 640.0, 200.0
	padL, padR       = 52.0, 12.0
	padT, padB       = 10.0, 26.0
	plotW, plotH     = cw - padL - padR, ch - padT - padB
	maxPointsInChart = 1200
)

func niceMax(v float64) float64 {
	if v <= 0 {
		return 1
	}
	exp := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if m*exp >= v {
			return m * exp
		}
	}
	return 10 * exp
}

func compact(v float64, unit string) string {
	switch unit {
	case "%":
		return trimZero(fmt.Sprintf("%.2f", v*100)) + "%"
	case "ms":
		if v >= 1000 {
			return trimZero(fmt.Sprintf("%.1f", v/1000)) + " s"
		}
		return trimZero(fmt.Sprintf("%.0f", v)) + " ms"
	}
	if v >= 1000 {
		return trimZero(fmt.Sprintf("%.1f", v/1000)) + "K"
	}
	return trimZero(fmt.Sprintf("%.1f", v))
}

func trimZero(s string) string {
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// render draws a line chart as SVG: hairline grid, 2px lines, a light area
// wash for single-series charts, dashed threshold references.
func (c *chart) render() {
	n := len(c.X)
	if n > maxPointsInChart {
		// Keep every k-th point; the table view keeps all of them.
		k := int(math.Ceil(float64(n) / maxPointsInChart))
		x := []float64{}
		for i := 0; i < n; i += k {
			x = append(x, c.X[i])
		}
		for si := range c.Series {
			vals := []float64{}
			for i := 0; i < n; i += k {
				vals = append(vals, c.Series[si].Values[i])
			}
			c.Series[si].Values = vals
		}
		c.X, n = x, len(x)
	}
	var b strings.Builder
	maxY := 0.0
	for _, s := range c.Series {
		for _, v := range s.Values {
			maxY = math.Max(maxY, v)
		}
	}
	for _, r := range c.Refs {
		maxY = math.Max(maxY, r.Value)
	}
	if c.Unit == "%" {
		maxY = math.Max(maxY, 0.01)
	}
	maxY = niceMax(maxY * 1.05)
	maxX := 1.0
	if n > 0 {
		maxX = math.Max(c.X[n-1], 1)
	}
	px := func(x float64) float64 { return padL + x/maxX*plotW }
	py := func(y float64) float64 { return padT + plotH - y/maxY*plotH }

	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" class="chart" data-chart="%s" role="img" aria-label="%s">`, cw, ch, c.ID, template.HTMLEscapeString(c.Title))
	for i := 0; i <= 4; i++ {
		y := maxY * float64(i) / 4
		fmt.Fprintf(&b, `<line class="grid" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, padL, padL+plotW, py(y), py(y))
		fmt.Fprintf(&b, `<text class="tick" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, padL-6, py(y)+3.5, compact(y, c.Unit))
	}
	for i := 0; i <= 4; i++ {
		x := maxX * float64(i) / 4
		fmt.Fprintf(&b, `<text class="tick" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, px(x), ch-8, clock(x))
	}
	for _, r := range c.Refs {
		fmt.Fprintf(&b, `<line class="ref" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/><text class="ref-label" x="%.1f" y="%.1f" text-anchor="end">%s</text>`,
			padL, padL+plotW, py(r.Value), py(r.Value), padL+plotW, py(r.Value)-4, template.HTMLEscapeString(r.Label))
	}
	for _, s := range c.Series {
		if n == 0 {
			break
		}
		var path strings.Builder
		for i, v := range s.Values {
			cmd := "L"
			if i == 0 {
				cmd = "M"
			}
			fmt.Fprintf(&path, "%s%.1f %.1f ", cmd, px(c.X[i]), py(v))
		}
		if len(c.Series) == 1 {
			fmt.Fprintf(&b, `<path d="%sL%.1f %.1f L%.1f %.1f Z" fill="%s" fill-opacity="0.1"/>`, path.String(), px(c.X[n-1]), py(0), px(c.X[0]), py(0), s.Color)
		}
		fmt.Fprintf(&b, `<path d="%s" fill="none" stroke="%s" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/>`, path.String(), s.Color)
	}
	fmt.Fprintf(&b, `<line class="cross" x1="0" x2="0" y1="%.1f" y2="%.1f" visibility="hidden"/>`, padT, padT+plotH)
	fmt.Fprintf(&b, `<rect class="hit" x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="transparent" data-maxx="%g"/>`, padL, padT, plotW, plotH, maxX)
	b.WriteString(`</svg>`)
	c.SVG = template.HTML(b.String())
}

func clock(secs float64) string {
	d := time.Duration(secs * float64(time.Second)).Round(time.Second)
	m, s := int(d.Minutes()), int(d.Seconds())%60
	if m >= 60 {
		return fmt.Sprintf("%d:%02d:%02d", m/60, m%60, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// HeatStep mirrors web/src/heat.ts so the report and the UI agree.
func HeatStep(ms float64) string {
	switch {
	case ms < 100:
		return "cold"
	case ms < 300:
		return "ember"
	case ms < 600:
		return "cherry"
	case ms < 1000:
		return "orange"
	case ms < 2000:
		return "yellow"
	}
	return "white"
}

// FormatMS formats milliseconds the way the UI does.
func FormatMS(v float64) string { return fmtMS(v) }

func fmtMS(v float64) string {
	switch {
	case v >= 10000:
		return fmt.Sprintf("%.1f s", v/1000)
	case v >= 1000:
		return fmt.Sprintf("%.2f s", v/1000)
	case v >= 10:
		return fmt.Sprintf("%.0f ms", v)
	}
	return fmt.Sprintf("%.1f ms", v)
}

// LoadHTML renders a self-contained HTML report.
func LoadHTML(r *load.Result) ([]byte, error) {
	x := make([]float64, len(r.Ticks))
	rps, vus, errs, p50, p95, p99 := make([]float64, len(r.Ticks)), make([]float64, len(r.Ticks)), make([]float64, len(r.Ticks)),
		make([]float64, len(r.Ticks)), make([]float64, len(r.Ticks)), make([]float64, len(r.Ticks))
	for i, t := range r.Ticks {
		x[i], rps[i], vus[i], errs[i] = t.Elapsed, t.RPS, float64(t.VUs), t.ErrorRate
		p50[i], p95[i], p99[i] = t.Latency.P50, t.Latency.P95, t.Latency.P99
	}
	var refs []ref
	for _, th := range r.Thresholds {
		if th.Metric == "http_req_duration" {
			if v, ok := percentileLimit(th.Condition); ok {
				refs = append(refs, ref{Label: th.Condition, Value: v})
			}
		}
	}
	charts := []*chart{
		{ID: "rps", Title: "Requests per second", Unit: "", X: x, Series: []series{{"req/s", colSeries, rps}}},
		{ID: "latency", Title: "Response time percentiles", Unit: "ms", X: x, Refs: refs,
			Series: []series{{"p50", colP50, p50}, {"p95", colP95, p95}, {"p99", colP99, p99}}},
		{ID: "vus", Title: "Active virtual users", Unit: "", X: x, Series: []series{{"VUs", colSeries, vus}}},
		{ID: "errors", Title: "Failed requests", Unit: "%", X: x, Series: []series{{"failed", colSeries, errs}}},
	}
	data := map[string]*chart{}
	for _, c := range charts {
		c.render()
		data[c.ID] = c
	}
	chartJSON, _ := json.Marshal(data)

	m := r.Metrics
	d := m["http_req_duration"]
	view := map[string]any{
		"R":        r,
		"Started":  r.StartedAt.Local().Format("2 Jan 2006 15:04:05"),
		"Duration": clock(r.Duration),
		"Requests": int64(m["http_reqs"].Value),
		"RPS":      fmt.Sprintf("%.1f", m["http_reqs"].PerSecond),
		"P95":      fmtMS(d.P95),
		"P95Heat":  HeatStep(d.P95),
		"ErrRate":  fmt.Sprintf("%.2f%%", m["http_req_failed"].Value*100),
		"Iter":     int64(m["iterations"].Value),
		"Dropped":  int64(m["dropped_iterations"].Value),
		"WS":       m["ws_sessions"].Value > 0,
		"WSMsgs":   int64(m["ws_msgs_received"].Value),
		"Charts":   charts,
		"Data":     template.JS(chartJSON),
		"Ticks":    r.Ticks,
	}
	var buf bytes.Buffer
	if err := loadTmpl.Execute(&buf, view); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var pLimit = regexp.MustCompile(`^p\(([0-9.]+)\)(<=|<)([0-9.]+)$`)

// percentileLimit extracts the limit from an upper-bound percentile
// threshold such as "p(95)<500", to draw it on the latency chart.
func percentileLimit(cond string) (float64, bool) {
	m := pLimit.FindStringSubmatch(strings.ReplaceAll(cond, " ", ""))
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[3], 64)
	return v, err == nil
}

var loadTmpl = template.Must(template.New("load").Funcs(template.FuncMap{
	"ms":    fmtMS,
	"heat":  HeatStep,
	"clock": clock,
	"pct":   func(f float64) string { return fmt.Sprintf("%.2f%%", f*100) },
	"f1":    func(f float64) string { return fmt.Sprintf("%.1f", f) },
	"inc":   func(i int) int { return i + 1 },
	"actual": func(t load.ThresholdResult) string {
		if t.NoData {
			return "no data"
		}
		if strings.HasPrefix(t.Condition, "rate") {
			return fmt.Sprintf("%.4g", t.Actual)
		}
		if strings.Contains(t.Metric, "duration") || strings.Contains(t.Metric, "rtt") || strings.Contains(t.Metric, "connecting") {
			return fmtMS(t.Actual)
		}
		return fmt.Sprintf("%.4g", t.Actual)
	},
}).Parse(loadHTML))

const loadHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.R.Name}} · Pyro load report</title>
<style>
:root{color-scheme:dark;--bg:#0a0d10;--raised:#11151a;--rule:#272c33;--text:#e7e9ec;--muted:#a3a9b1;--faint:#7d838b;
--ok:#4fc38a;--error:#ef5361;--warn:#e6c35a;
--heat-cold:#7d93a8;--heat-ember:#8e3a26;--heat-cherry:#c4392f;--heat-orange:#ef9a3d;--heat-yellow:#f3d25c;--heat-white:#fbf3dc}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:14px/1.5 system-ui,sans-serif}
main{max-width:1100px;margin:0 auto;padding:32px 20px 56px}
h1{margin:0;font-size:22px}h2{font-size:14px;margin:36px 0 10px;color:var(--muted)}
.sub{color:var(--muted);margin:4px 0 0}
.warn{margin:10px 0 0;padding:8px 12px;border:1px solid var(--rule);border-radius:4px;color:var(--text)}
.status{display:inline-flex;gap:6px;align-items:center;font-weight:600;margin-left:10px;font-size:14px}
.status.passed{color:var(--ok)}.status.failed,.status.aborted{color:var(--error)}.status.stopped{color:var(--warn)}
.kpis{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:1px;background:var(--rule);border:1px solid var(--rule);border-radius:4px;overflow:hidden;margin-top:24px}
.kpi{background:var(--raised);padding:12px 14px}.kpi>span{display:block;color:var(--muted);font-size:12px}.kpi b{font-size:22px;font-weight:600}
.chip{display:inline-block;padding:1px 7px;border-radius:3px;font:600 13px ui-monospace,monospace;color:#1d1410}
.chip.cold{color:var(--bg)}.chip.ember,.chip.cherry{color:#fbf3ee}
.cold{background:var(--heat-cold)}.ember{background:var(--heat-ember)}.cherry{background:var(--heat-cherry)}.orange{background:var(--heat-orange)}.yellow{background:var(--heat-yellow)}.white{background:var(--heat-white)}
.sw{display:inline-block;width:9px;height:9px;border-radius:2px;margin-right:6px;vertical-align:0}
.grid2{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,460px),1fr));gap:16px}
figure{margin:0;padding:12px 12px 6px;background:var(--raised);border:1px solid var(--rule);border-radius:4px;position:relative}
figcaption{display:flex;gap:14px;align-items:center;font-size:13px;font-weight:600;margin-bottom:4px}
.legend{display:flex;gap:12px;font-weight:400;color:var(--muted);font-size:12px}.legend i{display:inline-block;width:14px;height:2px;margin-right:5px;vertical-align:3px}
svg.chart{width:100%;height:auto;display:block}
.grid{stroke:var(--rule);stroke-width:1}.tick{fill:var(--faint);font-size:10px;font-family:ui-monospace,monospace}
.ref{stroke:var(--muted);stroke-width:1;stroke-dasharray:4 4}.ref-label{fill:var(--muted);font-size:10px}
.cross{stroke:var(--muted);stroke-width:1}
.tip{position:absolute;pointer-events:none;background:#1b2027;border:1px solid var(--rule);border-radius:4px;padding:6px 8px;font-size:12px;display:none;min-width:110px}
.tip b{display:block;font-weight:400;color:var(--muted);margin-bottom:2px}.tip div{display:flex;gap:6px;align-items:center}.tip strong{font-weight:600}
.tip i{display:inline-block;width:12px;height:2px}
.scroll{overflow-x:auto}table{width:100%;border-collapse:collapse;font-variant-numeric:tabular-nums}
th{text-align:left;font-weight:500;color:var(--muted);font-size:12px;border-bottom:1px solid #3a4048;padding:6px 8px}
td{border-bottom:1px solid var(--rule);padding:6px 8px;vertical-align:top}td.n,th.n{text-align:right}
.mono{font-family:ui-monospace,monospace;font-size:12.5px}.pass{color:var(--ok)}.fail{color:var(--error)}.muted{color:var(--muted)}
details{margin-top:12px}summary{cursor:pointer;color:var(--muted)}
footer{margin-top:48px;color:var(--faint);font-size:12px}
</style></head><body><main>
<h1>{{.R.Name}}<span class="status {{.R.Status}}">{{if eq .R.Status "passed"}}✓ Passed{{else if eq .R.Status "failed"}}✗ Failed{{else if eq .R.Status "aborted"}}✗ Aborted{{else}}■ Stopped{{end}}</span></h1>
<p class="sub">Started {{.Started}} · ran {{.Duration}} · up to {{.R.MaxVUs}} virtual users{{if .R.MaxRPS}} · capped at {{.R.MaxRPS}} req/s{{end}}{{if .R.Reason}} · {{.R.Reason}}{{end}}</p>
{{range .R.Warnings}}<p class="warn">⚠ {{.}}</p>{{end}}

<div class="kpis">
<div class="kpi"><span>Requests</span><b>{{.Requests}}</b></div>
<div class="kpi"><span>Average throughput</span><b>{{.RPS}}/s</b></div>
<div class="kpi"><span>p95 response time</span><b><span class="chip {{.P95Heat}}">{{.P95}}</span></b></div>
<div class="kpi"><span>Failed requests</span><b>{{.ErrRate}}</b></div>
<div class="kpi"><span>Iterations</span><b>{{.Iter}}</b></div>
{{if .Dropped}}<div class="kpi"><span>Dropped iterations</span><b>{{.Dropped}}</b></div>{{end}}
{{if .WS}}<div class="kpi"><span>Stream messages received</span><b>{{.WSMsgs}}</b></div>{{end}}
</div>

{{if .R.Thresholds}}<h2>Thresholds</h2>
<div class="scroll"><table><thead><tr><th>Result</th><th>Metric</th><th>Condition</th><th class="n">Actual</th></tr></thead><tbody>
{{range .R.Thresholds}}<tr><td class="{{if .Passed}}pass{{else}}fail{{end}}">{{if .NoData}}– no data{{else if .Passed}}✓ passed{{if .Breached}} <span class="muted">(failed earlier in the run)</span>{{end}}{{else}}✗ failed{{end}}</td><td class="mono">{{.Metric}}</td><td class="mono">{{.Condition}}{{if .AbortOnFail}} <span class="muted">(abort on fail)</span>{{end}}</td><td class="n mono">{{actual .}}</td></tr>{{end}}
</tbody></table></div>{{end}}

<h2>Over time</h2>
<div class="grid2">
{{range .Charts}}<figure><figcaption>{{.Title}}{{if gt (len .Series) 1}}<span class="legend">{{range .Series}}<span><i style="background:{{.Color}}"></i>{{.Name}}</span>{{end}}</span>{{end}}</figcaption>{{.SVG}}<div class="tip"></div></figure>{{end}}
</div>

<h2>Requests</h2>
<div class="scroll"><table><thead><tr><th>Request</th><th>Scenario</th><th class="n">Count</th><th class="n">Failed</th><th class="n">req/s</th><th class="n">Avg</th><th class="n">p50</th><th class="n">p95</th><th class="n">p99</th><th class="n">Max</th></tr></thead><tbody>
{{range .R.Requests}}<tr><td class="mono">{{.Method}} {{.Request}}</td><td>{{.Scenario}}</td><td class="n">{{.Count}}</td><td class="n {{if .Failed}}fail{{end}}">{{.Failed}}</td><td class="n">{{f1 .PerSec}}</td><td class="n">{{ms .Duration.Avg}}</td><td class="n">{{ms .Duration.Med}}</td><td class="n"><span class="sw {{heat .Duration.P95}}"></span>{{ms .Duration.P95}}</td><td class="n"><span class="sw {{heat .Duration.P99}}"></span>{{ms .Duration.P99}}</td><td class="n">{{ms .Duration.Max}}</td></tr>{{end}}
</tbody></table></div>
<p class="muted" style="font-size:12px">Swatches show the heat scale: cold steel &lt; 100 ms · ember 100–300 · cherry 300–600 · orange 600 ms–1 s · yellow 1–2 s · white &gt; 2 s.</p>

{{if .R.Agents}}<h2>Agents</h2>
<div class="scroll"><table><thead><tr><th>Agent</th><th class="n">Segment</th><th class="n">Max VUs</th><th class="n">Requests</th><th class="n">Peak CPU</th><th>State</th></tr></thead><tbody>
{{$n := len .R.Agents}}{{range .R.Agents}}<tr><td class="mono">{{.Name}}</td><td class="n">{{inc .Segment}} of {{$n}}</td><td class="n">{{.VUs}}</td><td class="n">{{.Requests}}</td><td class="n">{{pct .PeakCPU}}</td><td class="{{if eq .State "failed"}}fail{{end}}">{{if eq .State "failed"}}✗ failed{{if .Error}}: {{.Error}}{{end}}{{else}}✓ {{.State}}{{end}}</td></tr>{{end}}
</tbody></table></div>
<p class="muted" style="font-size:12px">Distributed test: each agent ran its segment of the plan; percentiles and thresholds above are computed from all agents' samples merged.</p>{{end}}

{{if .R.Errors}}<h2>Errors</h2>
<div class="scroll"><table><thead><tr><th>Message</th><th class="n">Count</th></tr></thead><tbody>
{{range .R.Errors}}<tr><td class="mono">{{.Message}}</td><td class="n">{{.Count}}</td></tr>{{end}}
</tbody></table></div>{{end}}

<details><summary>Data table (every interval)</summary>
<div class="scroll"><table><thead><tr><th>Time</th><th class="n">VUs</th><th class="n">req/s</th><th class="n">Failed</th><th class="n">p50</th><th class="n">p95</th><th class="n">p99</th></tr></thead><tbody>
{{range .Ticks}}<tr><td class="mono">{{clock .Elapsed}}</td><td class="n">{{.VUs}}</td><td class="n">{{f1 .RPS}}</td><td class="n">{{pct .ErrorRate}}</td><td class="n">{{ms .Latency.P50}}</td><td class="n">{{ms .Latency.P95}}</td><td class="n">{{ms .Latency.P99}}</td></tr>{{end}}
</tbody></table></div></details>
<footer>Generated by Pyro.</footer>
</main>
<script>
const DATA = {{.Data}};
const fmt = (v, u) => u === "%" ? (v * 100).toFixed(2) + "%" : u === "ms" ? (v >= 1000 ? (v / 1000).toFixed(2) + " s" : v.toFixed(v < 10 ? 1 : 0) + " ms") : v.toFixed(1);
const clock = (s) => { s = Math.round(s); return Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0"); };
document.querySelectorAll("svg.chart").forEach((svg) => {
  const c = DATA[svg.dataset.chart]; if (!c || !c.x.length) return;
  const hit = svg.querySelector(".hit"), cross = svg.querySelector(".cross"), tip = svg.parentElement.querySelector(".tip");
  const maxX = Number(hit.dataset.maxx), x0 = Number(hit.getAttribute("x")), w = Number(hit.getAttribute("width"));
  const show = (clientX) => {
    const r = svg.getBoundingClientRect(), vx = (clientX - r.left) / r.width * 640;
    const t = Math.max(0, Math.min(1, (vx - x0) / w)) * maxX;
    let i = 0; for (let j = 1; j < c.x.length; j++) if (Math.abs(c.x[j] - t) < Math.abs(c.x[i] - t)) i = j;
    const px = x0 + c.x[i] / maxX * w;
    cross.setAttribute("x1", px); cross.setAttribute("x2", px); cross.setAttribute("visibility", "visible");
    tip.replaceChildren();
    const head = document.createElement("b"); head.textContent = clock(c.x[i]); tip.append(head);
    c.series.forEach((s) => { const row = document.createElement("div"), key = document.createElement("i"), v = document.createElement("strong"), n = document.createElement("span");
      key.style.background = s.color; v.textContent = fmt(s.values[i], c.unit); n.textContent = s.name; n.className = "muted"; row.append(key, v, n); tip.append(row); });
    tip.style.display = "block";
    const left = (px / 640) * r.width + 12; tip.style.left = Math.min(left, r.width - tip.offsetWidth) + "px"; tip.style.top = "36px";
  };
  hit.addEventListener("pointermove", (e) => show(e.clientX));
  hit.addEventListener("pointerleave", () => { tip.style.display = "none"; cross.setAttribute("visibility", "hidden"); });
});
</script></body></html>`
