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

### Implemented (Phase 0)
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

### Planned
| Method | Path | Phase | Description |
|---|---|---|---|
| POST | `/api/v1/send` | 1 | Send one request (`{path? , request?, environment}`), returning the response, timings and test results. |
| GET | `/api/v1/history` | 1 | Recent sends (SQLite). |
| POST | `/api/v1/import` | 1 | Upload a Postman collection or environment; returns the import report. |
| POST | `/api/v1/sessions` | 2 | Open an interactive WebSocket or Socket.IO session; returns `{id}`. |
| POST | `/api/v1/sessions/{id}/send` · DELETE `/api/v1/sessions/{id}` | 2 | Send a message, close the session. |
| POST | `/api/v1/runs` | 2 | Start a collection run. |
| GET/PUT | `/api/v1/loadtests/{name}` | 3 | Load plan CRUD. |
| POST | `/api/v1/loadtests/{name}/start` · `/api/v1/loadruns/{id}/stop` | 3 | Control a load test. |
| GET | `/api/v1/results/{id}` | 3 | Summary, time series, thresholds. |
| GET/POST/DELETE | `/api/v1/plugins…` | 4 | List, install, configure, remove. |

## Event WebSocket (Phase 2+)
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
| `session:<id>` | `open`, `message` (direction, data, event, ack), `error`, `close` |
| `run:<id>` | `started`, `request` (result + tests), `iteration`, `finished` |
| `load:<id>` | `started`, `metrics` (1s tick), `threshold`, `log`, `finished` |
| `workspace` | `changed` (files edited on disk, through a file watcher) |
