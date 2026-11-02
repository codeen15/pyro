# Import and export

Pyro imports Postman, OpenAPI/Swagger, HAR, JMeter and cURL, and exports collections and
environments back to Postman. Postman import has its own page,
[postman-import.md](postman-import.md); this page covers the other formats and export.

```sh
pyro import shop.postman_collection.json openapi.yaml recording.har   # format detected per file
pbpaste | pyro import - --into shop/orders                           # a copied cURL into a folder
pyro export shop -o shop.postman_collection.json
pyro export -e staging -o staging.postman_environment.json
```
In the UI, **Import** (top bar) takes files (drop or choose) or pasted text, into a new
collection or an existing collection or folder. The download button on a collection's row
exports it to Postman.

Every import produces a report: what was created, and a list of things to check
(`pyro import --report report.json` saves it).

## Adding to an existing collection
`--into <collection or folder>` (UI: **Add to**) writes the requests under that path instead
of creating a collection. Names that clash get a `-2` suffix. Variables the collection
doesn't have yet are added to it; existing ones are kept. Imported requests that relied on
the imported collection's auth get that auth set on the request itself, so they keep
working under the target's settings. The imported collection's scripts are not copied
(reported).

## OpenAPI 3.x and Swagger 2.0
JSON or YAML. Local `$ref`s are followed (`#/components/...`, `#/definitions/...`);
external ones are reported.

| OpenAPI | Pyro |
|---|---|
| `info.title`, `info.description` | Collection name and description |
| `servers[0]` (with variable defaults); 2.0 `schemes[0]://host/basePath` | Collection variable `baseUrl`. Other servers are listed in the report. |
| Each operation | A request: name from `summary`, else `operationId`, else `METHOD /path`; file name from `operationId` or the summary |
| First tag | Folder (in the order of the top-level `tags` list); untagged operations stay at the top |
| `/orders/{id}` | URL `{{baseUrl}}/orders/{{id}}`, with a collection variable `id` set from the parameter's example, default or first enum value |
| Query and header parameters | Query and header lists; required ones enabled, optional ones disabled. `Accept`, `Content-Type` and `Authorization` headers are skipped (the spec ignores them too). |
| Cookie parameters | A `Cookie` header |
| `requestBody` | JSON (preferred), form, multipart, XML or text body. Uses the media example, else the first named example, else one generated from the schema. |
| 2.0 `in: body` / `in: formData` | JSON body / form or multipart fields |
| Global `security` | Collection auth; an operation's own `security` sets request auth, and `security: []` means no auth |
| `http` bearer / basic, `apiKey` (header, query, cookie) | Bearer `{{bearerToken}}`, basic `{{username}}`/`{{password}}`, API key `{{<scheme name>}}`, with secret collection variables to fill in |
| `oauth2`, `openIdConnect` | Bearer `{{accessToken}}` (Pyro doesn't fetch OAuth tokens yet; reported) |

Generated examples keep the schema's property order and use `example`, `default`, `enum`,
`const`, `allOf` (merged), the first `oneOf`/`anyOf`, and formats (`date-time`, `uuid`,
`email`, …). `readOnly` properties are left out of request bodies; recursive schemas stop at
the first repeat. Each documented JSON response with a numeric status becomes an example (its media example, else the first named one, else generated from the schema, `readOnly` fields included). Not imported: callbacks, webhooks.

## HAR (HTTP Archive 1.2)
Browser dev tools (Network → Save all as HAR) and proxies produce these.
- One folder per host; requests keep their recorded order. Repeats become `get-cart`,
  `get-cart-2`, ….
- Static assets (images, fonts, styles, scripts, media), preflights (`OPTIONS`) and
  pings are skipped (counted in the report).
- HTTP/2 pseudo-headers and `Host`, `Content-Length`, `Connection`, `Accept-Encoding` are
  dropped; other headers are kept, **including `Cookie`**: recorded session cookies may be
  secrets and will expire (reported).
- Bodies: JSON, XML, text, form and multipart (uploaded file contents aren't in a HAR;
  reported). WebSocket entries become WebSocket requests.
- The collection is named after the first page's host, else the file name.

## JMeter (.jmx)
A JMeter test plan becomes a collection (one folder per thread group, one request per HTTP
sampler) **and a load test** (`loadtests/<name>.load.yaml`) with one scenario per thread
group, ready to run with `pyro load` or from the Load tab.

| JMeter | Pyro |
|---|---|
| Thread Group: threads, ramp-up, duration (scheduler) | `ramping-vus` (ramp to the thread count, hold for the rest), or `constant-vus` without ramp-up |
| Thread Group with a loop count and no duration | `per-vu-iterations` (ramp-up ignored, reported) |
| Concurrency Thread Group / Arrivals Thread Group (BlazeMeter plugins) | `ramping-vus` / `ramping-arrival-rate` |
| setUp / tearDown Thread Group | Ordinary scenarios (reported) |
| HTTP Request (+ HTTP Request Defaults) | A request; a scheme and host shared by all requests becomes the `baseUrl` variable |
| Parameters / raw body / files | Query (GET, DELETE), form or multipart fields, raw JSON/XML/text body, file fields |
| HTTP Header Manager, HTTP Authorization Manager (basic, digest) | Headers and auth on every request in scope |
| User Defined Variables | Collection variables |
| CSV Data Set Config | The scenario's data file `data/<file>` (shared); column names from the plan become the CSV header |
| Transaction / Loop / While / If / Once Only / Random Controller | `group` / `loop` / `loop.while` / `if` / `once` / `weighted` steps; Simple Controller is inlined |
| Constant, Uniform Random, Gaussian Random, Poisson Random timers | The request's `think` timer, same distribution (timers in scope add up; several random ones become one uniform spread, reported) |
| User Parameters | A `vars` pre-processor (the first thread's values; reported when there are more) |
| Counter | A `counter` pre-processor (`000`-style formats become `%03d`; shared counters count per VU, reported) |
| Constant Throughput Timer | `maxRPS` for the whole test (reported) |
| JSON Extractor, Regular Expression Extractor, Boundary Extractor | `extract` post-processors (`jsonpath`, `regex`), run before assertions as in JMeter |
| Response Assertion (code, text, headers), JSON Assertion, Duration Assertion | `assert` post-processors (`status`, `body`, `header`, `jsonpath`, `duration`) |
| `${var}`, `${__UUID()}`, `${__Random(…)}`, `${__time()}`, `${__P(name,default)}` | `{{var}}`, `{{$guid}}`, `{{$randomInt}}`, `{{$timestamp}}`, `{{name}}` (numbers use the default) |
| If/While conditions (`${__jexl3(…)}` or JavaScript) | Converted to Pyro expressions where possible (`"${status}" == "200"` → `status == 200`); others become `true` and are reported |

Skipped and reported: JSR223/BeanShell elements (Groovy can't run in Pyro's JavaScript engine: rewrite them as
`script` processors or [script steps](load-engine.md#script-steps)),
non-HTTP samplers, Module/Include Controllers, Kerberos auth, negated text
assertions. Cookie, cache and listener elements are ignored (Pyro keeps cookies per VU and
has its own results). Disabled elements are left out.

**Data files:** `pyro import` copies a CSV Data Set's file into `data/` when it finds it (at
the path in the plan, or next to the `.jmx`), adding the header line if JMeter listed the
column names. Importing from the UI can't read local files: copy it yourself (reported).

## cURL
Paste one or more commands (Chrome/Firefox "Copy as cURL", API docs, shell history).
Bash quoting (`'…'`, `"…"`, `$'…'`), `\` line continuations and Windows `^` continuations
work. Each command becomes a request named `METHOD host/path`.

| Option | Becomes |
|---|---|
| `-X`, `--request` | Method (default GET, or POST with a body) |
| `-H`, `--header` | Header. `Authorization: Bearer …` / `Basic …` become bearer / basic auth. |
| `-d`, `--data`, `--data-raw`, `--data-binary`, `--data-urlencode` | Body: JSON if the content type says so or it parses as JSON, form fields if it looks like `a=1&b=2`, else text. Several are joined with `&`. |
| `--json` | JSON body plus `Content-Type` and `Accept: application/json` |
| `-F`, `--form` | Multipart fields; `@file` becomes a file field |
| `-G`, `--get` | Data goes into the query instead |
| `-u`, `--user`; credentials in the URL | Basic auth |
| `-b`, `--cookie` (`name=value`), `-A`, `-e` | `Cookie`, `User-Agent`, `Referer` headers |
| `-T`, `--upload-file` | Binary file body (PUT) |
| `--oauth2-bearer` | Bearer auth |
| `-k`, `-L`, `-s`, `-v`, `--compressed`, `-o`, `-m`, … | Ignored (Pyro has its own settings) |

The URL's query string moves into the query list. Data read from files (`-d @file`) and
cookie files aren't imported (reported).

## Export to Postman (Collection v2.1)
`pyro export <collection>`, `GET /api/v1/collections/{slug}/export`, or the download button
on a collection's row.

- Folders, order, descriptions, variables (secret ones keep `type: secret`), auth (`none`
  becomes `noauth`; inherited stays inherited), headers, query (enabled ones also in the raw
  URL), every body type and pre-request/test scripts are exported.
- **Assertions and extractors become test-script code** appended to the request's test
  script, with the same semantics: values compare as strings, JSON values render like Pyro
  renders them, and extractors set `pm.variables`. JSONPath converts when it is a simple
  path (`$.a.b[0]['c d']`); wildcards, filters and `..` are reported and left out.
- Examples export as Postman examples (`response[]`).
- Left out and reported: WebSocket, Socket.IO and plugin requests (Collection v2.1 can't
  hold them), plugin auth types, and folder variables (Postman folders have none).
- Pyro's own tests import such an export back and run it: the converted checks pass and the
  extractors set the same variables.

Environments: `pyro export -e <name>` or `GET /api/v1/environments/{name}/export` writes
Postman's environment format (`secret` and disabled variables are kept).
