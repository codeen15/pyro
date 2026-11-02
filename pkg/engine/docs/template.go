package docs

import (
	"html/template"
	"strings"
)

var tmpl = template.Must(template.New("docs").Funcs(template.FuncMap{
	"lower": strings.ToLower,
	"hl":    highlight,
	"statusClass": func(code int) string {
		switch {
		case code >= 500:
			return "s5"
		case code >= 400:
			return "s4"
		case code >= 300:
			return "s3"
		}
		return "s2"
	},
}).Parse(page_html))

// The page follows Pyro's Pyrometry design: cold iron content, warm brick
// sidebar, flame accent; light or dark from the reader's system setting.
const page_html = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="dark light">
<meta name="generator" content="Pyro">
<title>{{.Name}} · API reference</title>
<style>
:root{--bg:#0a0d10;--raised:#11151a;--raised2:#171c22;--rule:#272c33;--text:#e7e9ec;--muted:#a3a9b1;--faint:#7d838b;
--brick:#1d1512;--brick-rule:#3a2a24;--brick-text:#eadfd6;--accent:#f2662a;--var:#e6c35a;
--ok:#4fc38a;--warn:#e6c35a;--err:#ef5361;--get:#5fcf98;--post:#e6c35a;--put:#7aa7e6;--patch:#b394e8;--delete:#ef6f5f;--other:#a3a9b1;
--mono:"JetBrains Mono",ui-monospace,"SF Mono",Menlo,monospace;--ui:"Archivo",system-ui,-apple-system,"Segoe UI",sans-serif}
@media (prefers-color-scheme: light){:root{--bg:#fbfbfc;--raised:#f3f4f6;--raised2:#eceef1;--rule:#dfe2e6;--text:#1c1f24;--muted:#5b616a;--faint:#7c828a;
--brick:#f4ebe4;--brick-rule:#e2d3c8;--brick-text:#3a2a24;--accent:#d24c14;--var:#9a6b00;
--ok:#1f8a55;--warn:#9a6b00;--err:#c4323f;--get:#1f8a55;--post:#9a6b00;--put:#2f62b0;--patch:#7a4fc2;--delete:#c4323f;--other:#5b616a}}
*{box-sizing:border-box}html{scroll-padding-top:16px}
body{margin:0;background:var(--bg);color:var(--text);font:14px/1.55 var(--ui);display:grid;grid-template-columns:280px minmax(0,1fr);min-height:100vh}
nav{position:sticky;top:0;height:100vh;overflow:auto;background:var(--brick);border-right:1px solid var(--brick-rule);color:var(--brick-text);padding:16px 12px}
nav .brand{font-weight:700;margin:0 6px 12px}nav input{width:100%;padding:6px 8px;border:1px solid var(--brick-rule);border-radius:4px;background:transparent;color:inherit;font:inherit;margin-bottom:10px}
nav h4{margin:14px 6px 4px;font-size:11px;letter-spacing:.04em;text-transform:uppercase;opacity:.7}
nav a{display:flex;gap:8px;align-items:baseline;padding:3px 6px;border-radius:3px;color:inherit;text-decoration:none;font-size:13px}
nav a:hover{background:var(--brick-rule)}
.m{font:600 11px var(--mono);min-width:44px;color:var(--other)}.m.get{color:var(--get)}.m.post{color:var(--post)}.m.put{color:var(--put)}.m.patch{color:var(--patch)}.m.delete{color:var(--delete)}
main{padding:32px 40px 80px;max-width:1040px}
h1{font-size:26px;margin:0 0 4px}h2{font-size:18px;margin:40px 0 8px;padding-top:8px;border-top:1px solid var(--rule)}h3{font-size:16px;margin:0}
.meta{color:var(--muted);font-size:13px}.md p{margin:.5em 0}.md code,code{font-family:var(--mono);font-size:12.5px;background:var(--raised2);padding:1px 4px;border-radius:3px}
.md a,a{color:var(--accent)}
.req{margin:22px 0;padding:18px 20px;border:1px solid var(--rule);border-radius:6px;background:var(--raised)}
.url{display:flex;gap:10px;align-items:baseline;margin:6px 0 10px;font:13px var(--mono);overflow-wrap:anywhere}
.var{color:var(--var)}
table{border-collapse:collapse;width:100%;margin:6px 0 12px;font-size:13px}th,td{text-align:left;padding:5px 8px;border-bottom:1px solid var(--rule);vertical-align:top}
th{color:var(--muted);font-weight:500;font-size:12px}td.k{font-family:var(--mono);white-space:nowrap}td.v{font-family:var(--mono);overflow-wrap:anywhere}
.opt{color:var(--faint);font-size:12px}
h5{margin:14px 0 4px;font-size:12px;color:var(--muted);font-weight:600;text-transform:uppercase;letter-spacing:.04em}
pre{margin:4px 0 10px;padding:10px 12px;background:var(--bg);border:1px solid var(--rule);border-radius:4px;overflow:auto;font:12.5px/1.5 var(--mono);white-space:pre}
details{margin:6px 0}summary{cursor:pointer;color:var(--muted)}
.st{font:600 12px var(--mono)}.st.s2{color:var(--ok)}.st.s3{color:var(--muted)}.st.s4{color:var(--warn)}.st.s5{color:var(--err)}
.copy{float:right;font:12px var(--ui);background:none;border:1px solid var(--rule);color:var(--muted);border-radius:4px;padding:2px 8px;cursor:pointer}
.auth{font-size:13px;color:var(--muted);margin:4px 0}
footer{margin-top:60px;color:var(--faint);font-size:12px}
@media (max-width:820px){body{grid-template-columns:minmax(0,1fr)}nav{position:static;height:auto;max-height:45vh}main{padding:20px 16px 60px}}
</style>
</head>
<body>
<nav aria-label="Requests">
<div class="brand">{{.Name}}</div>
<input type="search" placeholder="Filter requests" aria-label="Filter requests" oninput="filter(this.value)">
{{range .Sections}}<h4 style="padding-left:{{.Depth}}em">{{.Name}}</h4>
{{range .Requests}}<a href="#{{.ID}}" data-name="{{lower .Name}} {{lower .Method}}"><span class="m {{lower .Method}}">{{.Method}}</span><span>{{.Name}}</span></a>
{{end}}{{end}}
</nav>
<main>
<h1>{{.Title}}</h1>
<p class="meta">{{.Count}} request{{if ne .Count 1}}s{{end}} · generated by Pyro on {{.Generated}}</p>
{{if .Description}}<div class="md">{{.Description}}</div>{{end}}
{{if .Auth}}<p class="auth"><strong>Auth:</strong> {{.Auth}}</p>{{end}}
{{if .Variables}}<h5>Variables</h5>
<table><thead><tr><th>Name</th><th>Value</th></tr></thead><tbody>
{{range .Variables}}<tr><td class="k">{{.Key}}</td><td class="v">{{.Value}}</td></tr>{{end}}
</tbody></table>{{end}}
{{range .Sections}}
<h2 id="{{.ID}}">{{.Name}}</h2>
{{if .Description}}<div class="md">{{.Description}}</div>{{end}}
{{range .Requests}}
<section class="req" id="{{.ID}}">
<h3>{{.Name}}</h3>
<div class="url"><span class="m {{lower .Method}}">{{.Method}}</span><span>{{.URL}}</span></div>
{{if .Description}}<div class="md">{{.Description}}</div>{{end}}
{{if .Auth}}<p class="auth"><strong>Auth:</strong> {{.Auth}}</p>{{end}}
{{if .Query}}<h5>Query parameters</h5>
<table><tbody>{{range .Query}}<tr><td class="k">{{.Key}}</td><td class="v">{{hl .Value}}</td><td class="opt">{{if .Disabled}}optional{{end}}</td></tr>{{end}}</tbody></table>{{end}}
{{if .Headers}}<h5>Headers</h5>
<table><tbody>{{range .Headers}}<tr><td class="k">{{.Key}}</td><td class="v">{{hl .Value}}</td><td class="opt">{{if .Disabled}}optional{{end}}</td></tr>{{end}}</tbody></table>{{end}}
{{if .BodyText}}<h5>Body <span class="opt">{{.Body.Type}}</span></h5><pre>{{.BodyText}}</pre>{{end}}
{{if .Examples}}<h5>Examples</h5>
{{range .Examples}}<details{{if eq (statusClass .Status) "s2"}} open{{end}}><summary>{{if .Status}}<span class="st {{statusClass .Status}}">{{.Status}}</span> {{end}}{{.Name}}</summary>
{{if .Headers}}<table><tbody>{{range .Headers}}<tr><td class="k">{{.Key}}</td><td class="v">{{.Value}}</td></tr>{{end}}</tbody></table>{{end}}
{{if .Body}}<pre>{{.Body}}</pre>{{end}}
</details>{{end}}{{end}}
{{if .Curl}}<h5>cURL <button class="copy" onclick="copyNext(this)">Copy</button></h5><pre>{{.Curl}}</pre>{{end}}
</section>
{{end}}{{end}}
<footer>Variables appear as <span class="var">{{"{{name}}"}}</span>; secret values and credentials are left out.</footer>
</main>
<script>
function filter(q){q=q.toLowerCase();document.querySelectorAll("nav a").forEach(function(a){a.style.display=a.dataset.name.indexOf(q)>=0?"":"none"})}
function copyNext(b){var t=b.parentElement.nextElementSibling.textContent;navigator.clipboard&&navigator.clipboard.writeText(t).then(function(){b.textContent="Copied";setTimeout(function(){b.textContent="Copy"},1200)})}
</script>
</body>
</html>`
