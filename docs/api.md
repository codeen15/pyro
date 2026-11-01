# Engine API

The engine exposes a local HTTP API used by the web UI. The source of truth is
`internal/server/server.go`. From Phase 1 an OpenAPI spec will generate the
TypeScript client in `web/src/api.ts`.

## Auth
- `pyro serve` binds to `127.0.0.1:7878` by default and prints `http://127.0.0.1:7878/?token=<hex>`.
- Every `/api/v1/*` request must send `X-Pyro-Token: <token>`. A missing or wrong token gets `401`.
- The UI reads `?token=` once, stores it in `sessionStorage` for the tab, and removes it from the address bar.
- `--no-token` turns the check off, and is refused unless the bind address is loopback. It exists for the Vite dev server.
- Static UI assets (`/`, `/assets/*`) are served without a token.

## Conventions
- JSON in and out. Unknown request fields are rejected (`400`). Request bodies are limited to 10 MB.
- Errors: `{"error": "<message>"}` with `400` (invalid input or path), `404` (not found), `409` (already exists), `500` (unexpected; details logged, not returned).
- Request paths in URLs are the workspace request path (see [data-model.md](data-model.md)). Each segment is URL-encoded, e.g. `/api/v1/requests/demo/get%20user`.

## Endpoints

### Implemented (Phases 0–4)
| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/health` | `{status, version}` |
| GET | `/api/v1/workspace` | `{name, root, collections: Node[]}`, where Node = `{type, name, path, kind?, method?, children?}` |
| POST | `/api/v1/collections` | Body `{slug, name?, description?, variables?, auth?, scripts?}` → `201` |
| GET | `/api/v1/collections/{slug}` | Collection settings |
| GET | `/api/v1/requests/{path…}` | A request |
| PUT | `/api/v1/requests/{path…}` | Create or replace; folders are created as needed; `kind` defaults to `http` |
| DELETE | `/api/v1/requests/{path…}` | `204` |
| GET | `/api/v1/environments` | Environment names |
| GET | `/api/v1/environments/{name}` | An environment |
| PUT | `/api/v1/environments/{name}` | Create or replace |
| DELETE | `/api/v1/environments/{name}` | `204` |
| PUT | `/api/v1/collections/{slug}` | Replace collection settings (variables, auth, scripts) |
| POST | `/api/v1/send` | Body `{path?, request?, environment?, runtime?}`. `path` loads the saved request and supplies collection/folder variables and auth; `request` sends unsaved edits instead. Returns `{sent, response?, unresolved?, error?, startedAt, historyId?}`. A transport failure is still `200` with `error` set. Response bodies are base64 (`response.body`). |
| GET | `/api/v1/history?limit=N` | Newest first: `{id, at, path?, name?, method, url, status?, durationMs, error?}` |
| GET | `/api/v1/history/{id}` | Full stored send result (bodies capped at 256 KB) |
| DELETE | `/api/v1/history` | Clear this workspace's history |
| POST | `/api/v1/import?filename=&into=` | The file as the body (≤ 50 MB): Postman collection or environment, OpenAPI/Swagger, HAR or cURL text; the format is detected. `filename` names cURL/HAR collections; `into` adds the requests to an existing collection or folder. `201` with the import report (`into: true` when added); `422` if the format isn't recognised. |
| GET | `/api/v1/collections/{slug}/export` | `{filename, content, report}`: the collection as Postman v2.1 JSON (`content`) and what didn't convert |
| GET | `/api/v1/environments/{name}/export` | `{filename, content}`: the environment as Postman environment JSON |
| POST | `/api/v1/oauth2/token` | Body `{path?, request?, environment?, fetch?, clear?}`: the OAuth 2.0 token for the request's effective auth: `{oauth, grant, token?, expired?, interactive, inherited, redirectUri, unresolved?}` (`oauth: false` when its auth isn't OAuth 2.0). `fetch` gets a new one (non-interactive grants), `clear` forgets it |
| POST | `/api/v1/oauth2/authorize` | Same body; starts an authorization-code sign-in and returns `{url, redirectUri}` to open in a browser |
| GET | `/oauth2/callback` | The provider's redirect target (no session token; the random `state` ties it to a sign-in Pyro started) |
| GET | `/api/v1/cookies` | The interactive cookie jar: `[{name, value, domain, path, hostOnly?, secure?, httpOnly?, sameSite?, expires?, created}]` |
| PUT | `/api/v1/cookies` | Add or replace a cookie (`domain` and `name` required, `path` defaults to `/`) |
| DELETE | `/api/v1/cookies?domain=&path=&name=` | One cookie, a domain's cookies (domain only) or all (no parameters) |
| GET, PUT | `/api/v1/globals` | `globals.yaml` as an environment `{name: "globals", variables}` |
| POST | `/api/v1/sessions` | Body `{path?, request?, environment?}`. Opens a WebSocket or Socket.IO session. Returns `{sent, unresolved?, error?, session?}`; on failure `error` is set and there is no `session`. |
| GET | `/api/v1/sessions/{id}` | `{id, kind, info, openedAt, closed, error?, messages[]}` (last 2000 messages) |
| POST | `/api/v1/sessions/{id}/send` | Body `{data, binary?, event?, ack?}` (variables resolved). Returns `{message, unresolved?}` |
| DELETE | `/api/v1/sessions/{id}` | Close |
| POST | `/api/v1/runs` | Body `{path, environment?, iterations?, data?, delayMs?, bail?}`. Starts a background collection run; `202` with the run view |
| GET | `/api/v1/runs/{id}` | `{id, path, status, done, total, results[], summary?}` |
| DELETE | `/api/v1/runs/{id}` | Cancel |
| GET | `/api/v1/loadtests` | `[{name, title?, peakVUs?, error?}]` (`error` = the plan doesn't validate) |
| GET / PUT / DELETE | `/api/v1/loadtests/{name}` | GET returns `{name, title, yaml, plan, peakVUs, error?}`. PUT `{yaml}` validates (400 with every problem) and saves the raw text, so comments survive. |
| POST | `/api/v1/loadtests/{name}/start` | Body `{environment?, confirmHosts?, distributed?}` (`distributed` runs on the agents given to `pyro serve --agent`; `400` if none). `409 {needsConfirmation, hosts}` when it would target hosts that aren't loopback or in `loadAllowHosts`; `409 {running}` when another test runs; `202 {id}` when started. |
| GET | `/api/v1/agents` | Configured agents: `[{address, info?: {name, version, protocol, cpus, os, arch, busy, maxVUs, plugins}, error?}]` |
| GET | `/api/v1/loadruns?test=` | Saved runs newest first (a running one first): `{id, test, name, startedAt, status, duration, requests, p95, errorRate, maxVUs}` |
| GET | `/api/v1/loadruns/{id}` | Running: `{status: "running", ticks, live: {tick, requests, errors}, plan}`. Finished: `{status, result}` |
| GET | `/api/v1/loadruns/{id}/report` | Self-contained HTML report (`?download=1` sets Content-Disposition) |
| POST | `/api/v1/loadruns/{id}/stop` | Graceful stop; `?abort=1` cancels in-flight requests |
| DELETE | `/api/v1/loadruns/{id}` | Delete a saved run |
| GET | `/api/v1/plugins` | `{plugins: [Info], available, indexConfigured, dir}`; Info = manifest + `{enabled, approved, ready, settings?, error?}` |
| POST | `/api/v1/plugins` | Body: a plugin archive (zip). Installs it unapproved (`201` Info; `422` if invalid) |
| POST | `/api/v1/plugins/{name}/approve` | Approve the current capabilities |
| POST | `/api/v1/plugins/{name}/enabled` | Body `{enabled}` |
| PUT | `/api/v1/plugins/{name}/config` | Body `{config: {key: value}}` (saved as `settings`) |
| DELETE | `/api/v1/plugins/{name}` | Uninstall |
| GET | `/api/v1/plugins/index?q=` | Search the configured index (`404` when none) |
| POST | `/api/v1/plugins/index/install` | Body `{name, version?}`: download (sha256-verified) and install |
| GET / DELETE | `/api/v1/session/variables` | Values set by scripts and extractors during this `pyro serve` (runtime, globals, per environment, per collection). Never written to files. DELETE resets them. |
| PATCH | `/api/v1/session/variables` | Body `{scope: globals\|environment\|collection\|runtime, name?, key, value?, reset?}`: set one current value (`value: null` unsets it for the session) or `reset` it to the file's initial value |

### Planned
| Method | Path | Phase | Description |
|---|---|---|---|

## Event WebSocket (implemented)
`GET /api/v1/events?token=<token>` (browsers can't set headers on WebSocket, so the token
goes in the query string; it is checked the same way).

Client → server:
```json
{"op":"subscribe","topic":"load:01HXYZ"}
{"op":"unsubscribe","topic":"load:01HXYZ"}
```
Server → client:
```json
{"topic":"load:01HXYZ","type":"metrics","data":{"t":1730450000,"vus":200,"rps":1834,"p95":212}}
```

| Topic | Events |
|---|---|
| `session:<id>` | `message` (`{seq, at, direction, kind, data, event?, ackId?, rttMs?, size}`), `closed` (`{error}`) |
| `run:<id>` | `result` (`{done, total, item}`, bodies trimmed to 64 KB), `finished` (the run view) |
| `load:<id>` | `tick` (`{tick, requests, errors}` once a second), `finished` (`{id, test, status, result}`) |
| `workspace` (planned) | `changed` (files edited on disk, through a file watcher) |

Events published before a client subscribes are not replayed. Clients subscribe first,
then fetch `GET /sessions/{id}` or `GET /runs/{id}` once and merge (by `seq` for
session messages, by position for run results). Slow subscribers drop events rather
than slowing the engine.
