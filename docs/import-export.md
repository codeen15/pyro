# Import and export

Pyro imports Postman, OpenAPI/Swagger, HAR and cURL, and exports collections and
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
the first repeat. Not imported: callbacks, webhooks, response examples.

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
- Left out and reported: WebSocket, Socket.IO and plugin requests (Collection v2.1 can't
  hold them), plugin auth types, and folder variables (Postman folders have none).
- Pyro's own tests import such an export back and run it: the converted checks pass and the
  extractors set the same variables.

Environments: `pyro export -e <name>` or `GET /api/v1/environments/{name}/export` writes
Postman's environment format (`secret` and disabled variables are kept).
