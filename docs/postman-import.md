# Postman Import (implemented in Phase 1; scripts run from Phase 2)

`pyro import <file.json> [-o collections/<slug>]`, or **Import** in the UI.
The code will live in `pkg/engine/importer/postman`.

## Supported inputs
- Collection **v2.0** and **v2.1**, detected from `info.schema`.
- Environment exports, which become `environments/<name>.env.yaml`.
- Globals exports, which become workspace globals (planned).

## Concept mapping
| Postman | Pyro |
|---|---|
| Collection | `collections/<slug>/collection.yaml` (slug from the name, kebab-case, made unique) |
| Folder (`item` with `item[]`) | Directory, plus `folder.yaml` when it has auth, scripts or a description |
| Request item | `<slug>.req.yaml`, `kind: http` |
| `request.url` (string or object) | `url` (raw form) + `query` (from `url.query`, keeping `disabled`) |
| `request.header[]` | `headers` (order and `disabled` kept) |
| `body.mode: raw` + `options.raw.language` | `body.type`: `json` / `xml` / `text` |
| `body.mode: urlencoded` | `body.type: form`, `fields` |
| `body.mode: formdata` | `body.type: multipart`, `fields` (file fields get `file: true` with the source path as `value`; reported) |
| `body.mode: file` | `body.type: file` |
| `body.mode: graphql` | `body.type: graphql`, `content` = query, `variables` |
| `auth` (collection, folder or request) | `auth` at the same level; `type: noauth` → `none`; `inherit` → omitted |
| Auth types basic, bearer, apikey, oauth2, digest | Same `type`; Postman's key/value list → `params` |
| Auth types awsv4, hawk, ntlm, akamai, edgegrid | Reported. Expected to come through `auth` plugins. |
| `variable[]` | `collection.yaml` `variables` |
| `event[]` `prerequest` / `test` | `scripts.pre` / `scripts.post` |
| `{{var}}` and `{{$guid}}`-style dynamic variables | Kept as written (same syntax) |
| Saved examples (`response[]`) | Skipped for now (reported); planned as mock fixtures |
| `protocolProfileBehavior` | Mapped where there is an equivalent (e.g. redirects), otherwise reported |
| WebSocket and Socket.IO requests (where exported) | `kind: websocket` / `kind: socketio` |
| Postman Flows, monitors, mocks | Not imported (reported) |

## `pm.*` script compatibility (Phase 2)
Scripts run in goja with a compatibility shim.

**Supported first:**
- Variables: `pm.environment`, `pm.collectionVariables`, `pm.variables`, `pm.globals`, `pm.iterationData` (`get`, `set`, `unset`, `has`, `toObject`).
- Request: `pm.request` (url, method, headers with `add`, `upsert` and `remove`, body).
- Response: `pm.response` (`code`, `status`, `headers`, `json()`, `text()`, `responseTime`, `responseSize`, `to.have.status`, `to.have.header`, `to.have.jsonBody`).
- Tests: `pm.test(name, fn)`, `pm.expect` (bundled chai), `pm.expect.fail`.
- Requests: `pm.sendRequest` (callback form and Promise form).
- Execution control: `pm.execution.skipRequest()`, `postman.setNextRequest` (runner only).
- Modules: `require()` for an allow-list: `lodash`, `moment`, `crypto-js`, `uuid`, `chai`, `tv4`/`ajv` (JSON Schema), `xml2js`. Legacy globals `responseBody`, `responseCode` and `tests[]`.

**Not supported (reported):** `pm.vault`, `pm.visualizer` (ignored with a warning), `pm.cookies.jar()` write APIs (read-only at first), and packages from Postman's Package Library.

The importer scans scripts for API usage it doesn't know (a simple AST walk over goja's parser output) and lists it in the report.

## Import report
Every import produces a report, which is shown in the UI and printed by the CLI (`--report report.json` for machine-readable output):

```json
{
  "collection": "Payments API",
  "requests": 48,
  "folders": 7,
  "environments": 2,
  "warnings": [
    {"path": "payments/auth/sign", "code": "auth.unsupported", "detail": "awsv4 auth: install an AWS SigV4 auth plugin"},
    {"path": "payments/orders/create", "code": "script.api", "detail": "pm.vault.get is not supported"},
    {"path": "payments/files/upload", "code": "body.file", "detail": "form-data file 'avatar' refers to /Users/x/a.png"}
  ]
}
```
The import still succeeds when there are warnings. Fatal errors (invalid JSON, an unknown schema) stop it without writing anything.

## Testing
Golden-file tests: real Postman exports under `testdata/postman/` are converted and compared with the expected YAML trees and reports.

## Later
Postman **export** (so you can move back), plus OpenAPI 3, HAR and cURL importers.
