# Architecture

## Overview

```
┌───────────────────────────── Browser (any OS) ──────────────────────────────┐
│  React + TS SPA: Workspace tree · Request editor · WS/SIO console ·         │
│  Collection runner · Load test designer · Live dashboard · Plugin manager   │
└──────────────▲───────────────────────────────▲──────────────────────────────┘
               │ REST/JSON (commands, CRUD)    │ WebSocket (live events/metrics)
┌──────────────┴───────────────────────────────┴──────────────────────────────┐
│                         pyro (single Go binary)                             │
│  ┌────────────┐  ┌───────────────────────────────────────────────────────┐  │
│  │ API Server │→ │                     Engine Core                       │  │
│  │ (chi + ws) │  │  Executor ─ Protocol Drivers (HTTP │ WS │ SIO │ wasm) │  │
│  │ embeds UI  │  │  Script Runtime (goja, pm.* compat)                   │  │
│  └────────────┘  │  Variable Resolver · Auth · Assertions · Extractors   │  │
│  ┌────────────┐  │  Collection Runner (functional)                       │  │
│  │    CLI     │→ │  Load Engine (scheduler, VUs, executors, thresholds)  │  │
│  │ run / load │  │  Metrics Pipeline (HDR histograms, time buckets)      │  │
│  └────────────┘  │  Plugin Host (wazero) · Importers · Reporters         │  │
│                  └───────────────────────────────────────────────────────┘  │
│  Storage: workspace files (YAML, git-friendly) + SQLite (history, results)  │
└─────────────────────────────────────────────────────────────────────────────┘
           controller ⇄ remote `pyro agent` nodes over WebSocket (ADR 0006)
```

## Principles
1. **The engine is a library.** `pkg/engine/...` has no knowledge of HTTP handlers, the CLI or the UI. `internal/server` and `cmd/pyro` are thin adapters. The engine never imports `internal/`.
2. **One data model, three modes.** The same `model.Request` is sent interactively, run in a collection, or executed by load-test VUs.
3. **Drivers share one interface.** HTTP, WebSocket, Socket.IO and plugin protocols plug into the same pipeline, so assertions, metrics and scripts work the same way everywhere.
4. **The hot path stays lean.** Load mode records samples into per-VU buffers and aggregates them off the hot path. Declarative assertions are pure Go, and scripts are optional.
5. **Pure Go, no CGO.** Cross-compiling to every OS and architecture is a plain `go build`.

## Components

| Component | Package | Status | Responsibility |
|---|---|---|---|
| Data model | `pkg/engine/model` | ✅ Phase 0 | Workspace, Collection, Request, Environment, Auth, Body, Assertion, Extractor, Message. |
| Workspace store | `internal/store` | ✅ Phase 0 | YAML persistence, tree listing, path safety, atomic writes. |
| API server | `internal/server` | ✅ Phase 2 | REST under `/api/v1` (CRUD, send, history, import, sessions, runs, session variables), event WebSocket, token auth, embedded SPA. |
| History | `internal/history` | ✅ Phase 1 | Sent requests in SQLite (`~/.pyro/pyro.db`), per workspace. |
| Send pipeline | `pkg/engine` (`engine.Send`, `engine.Connect`) | ✅ Phase 2 | Pre scripts → resolve → auth → execute → post scripts → assertions → extractors; `Connect` opens sessions with the same resolution and auth. |
| CLI | `cmd/pyro` | ✅ `serve`, `init`, `import`, `run`, `load`, `plugin`, `version` | `agent` comes in Phase 5. |
| Web UI | `web/` | ✅ Phase 1 | React SPA embedded with `go:embed`. Visual system in `DESIGN.md`. |
| Protocol drivers | `pkg/engine/protocol/{httpdriver,wsdriver,sioclient}` | ✅ Phase 1–2 | Execute requests; WebSocket and Socket.IO sessions (`protocol.Session`). |
| Variables, auth | `pkg/engine/vars`, `pkg/engine/auth` | ✅ Phase 1 | `{{var}}` resolution across scopes; auth inheritance and signing. |
| Script runtime | `pkg/engine/script` | ✅ Phase 2 | goja with a `pm.*` shim over vendored chai 4.5.0; `pyro.*` API comes with the load engine. |
| Assertions, extractors | `pkg/engine/check` | ✅ Phase 2 | Declarative checks; capture response values into variables. |
| Collection runner | `pkg/engine/runner` | ✅ Phase 2 | Iterations, data files, setNextRequest, bail. |
| Load engine | `pkg/engine/load` | ✅ Phase 3 | Plans, executors, VUs, flow steps, data feeders, thresholds. See [load-engine.md](load-engine.md). |
| Metrics | `pkg/engine/metrics` | ✅ Phase 3 | Per-VU buffers → HDR histograms (cumulative per series, per-interval per metric); deltas for merging across agents. |
| Distributed agents | `pkg/engine/agent` | ✅ Phase 5 | `pyro agent` server and the controller's client (`load.Worker`); see [load-engine.md](load-engine.md#distributed-tests) and [ADR 0006](adr/0006-agents-over-websocket.md). |
| Plugin host | `pkg/engine/plugin` | ✅ Phase 4 | wazero runtime, ABI v1 (JSON), capabilities, install/approve, index. See [plugins.md](plugins.md). |
| Importers | `pkg/engine/importer/{postman,openapi,har,curl,auto}` | ✅ Phase 1, 5 | Postman (Phase 1); OpenAPI/Swagger, HAR, cURL and format detection (Phase 5). See [postman-import.md](postman-import.md), [import-export.md](import-export.md). |
| Exporters | `pkg/engine/exporter/postman` | ✅ Phase 5 | Postman v2.1 collections and environments. See [import-export.md](import-export.md). |
| Live outputs | `pkg/engine/output` | ✅ Phase 5 | InfluxDB line protocol, Prometheus remote write, per-tick NDJSON/CSV (`--out`). |
| Reporters | `pkg/engine/report` | ✅ JUnit, JSON (runs); HTML, JSON (load) | Reports for CI; CSV planned. |

## Request pipeline
Every mode runs the same steps:

```
resolve variables → apply auth → pre-request script → driver.Execute / Connect
  → record metrics → post-response script & tests → assertions → extractors → update VU variables
```

## Driver interface (planned)
```go
type Driver interface {
    Kind() string
    Execute(ctx context.Context, req *ResolvedRequest, vu *VUContext) (*Response, error)
}
type StreamDriver interface {        // WebSocket, Socket.IO, plugin streams
    Driver
    Connect(ctx context.Context, req *ResolvedRequest, vu *VUContext) (Session, error)
}
type Session interface {
    Send(ctx context.Context, msg Message) error
    Messages() <-chan Message
    Close(code int, reason string) error
}
```

## Storage
- **Workspace files:** human-edited YAML in a directory you can commit. See [data-model.md](data-model.md).
- **SQLite** (`~/.pyro/pyro.db`, `modernc.org/sqlite`, from Phase 1): request history, run summaries and downsampled time series, plugin index and settings. Raw load-test samples go to NDJSON files, never written to SQLite row by row.

## Technology choices
| Choice | Why |
|---|---|
| **Go engine** | Cheap goroutines run 10k+ VUs, it ships as one static binary per OS, and it has strong network libraries. k6 uses the same approach. See [ADR 0001](adr/0001-go-engine.md). |
| **Local server + browser UI** | Works on every OS with no per-OS GUI code. The same binary runs headless on servers and in CI. |
| **React + Vite + TanStack Query + Ant Design** | Mature ecosystem. antd (MIT) supplies the components, themed with the brand red; charts are hand-drawn SVG (`LineChart.tsx`) under the dataviz rules. |
| **goja** | Pure-Go JS engine. One runtime per VU keeps scripts isolated. |
| **wazero** | Pure-Go WASM runtime: sandboxed, cross-platform plugins with no CGO. See [ADR 0002](adr/0002-wasm-plugins.md) and [ADR 0005](adr/0005-json-abi-and-go-sdk.md). |
| **In-house Socket.IO client** | Go's third-party clients are weak, and owning the protocol keeps load mode efficient. See [ADR 0003](adr/0003-socketio-in-house.md). |
| **One YAML file per request** | Small diffs and easy merges, and it works with any editor. See [ADR 0004](adr/0004-yaml-file-per-request.md). |
| **HDR histograms** | Accurate percentiles that can be merged across VUs and agents. |
| **SQLite without CGO** | Nothing to install, and cross-compiling stays simple. |

## Security model
- `pyro serve` binds to `127.0.0.1` and prints a URL carrying a random per-launch token. Every `/api` call must send `X-Pyro-Token`. `--no-token` only works on loopback.
- The store rejects path traversal (`..`, empty or `.` segments, backslashes, drive letters).
- Plugins get only the capabilities they declare and the user approves on install.
- Load tests against non-allow-listed hosts need confirmation.
