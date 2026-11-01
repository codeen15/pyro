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
           (Phase 5) controller ⇄ remote `pyro agent` nodes over gRPC
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
| API server | `internal/server` | ✅ Phase 1 | REST under `/api/v1` (CRUD, send, history, import), token auth, embedded SPA. Event WebSocket comes in Phase 2. |
| History | `internal/history` | ✅ Phase 1 | Sent requests in SQLite (`~/.pyro/pyro.db`), per workspace. |
| Send pipeline | `pkg/engine` (`engine.Send`) | ✅ Phase 1 | Resolve → auth → execute; scripts/assertions join in Phase 2. |
| CLI | `cmd/pyro` | ✅ `serve`, `init`, `import`, `version` | `run`, `load`, `plugin`, `agent` come later. |
| Web UI | `web/` | ✅ Phase 1 | React SPA embedded with `go:embed`. Visual system in `DESIGN.md`. |
| Protocol drivers | `pkg/engine/protocol/httpdriver` ✅, `websocket`, `socketio` (Phase 2) | Phase 1–2 | Execute requests and stream sessions. |
| Variables, auth | `pkg/engine/vars`, `pkg/engine/auth` | ✅ Phase 1 | `{{var}}` resolution across scopes; auth inheritance and signing. |
| Script runtime | `pkg/engine/script` | Phase 2 | goja with a `pm.*` shim and `pyro.*` API. |
| Assertions, extractors | `pkg/engine/assert`, `pkg/engine/extract` | Phase 2 | Declarative checks; capture response values into variables. |
| Collection runner | `pkg/engine/runner` | Phase 2 | Iterations, data files, functional reports. |
| Load engine | `pkg/engine/load` | Phase 3 | Scheduler, executors, VU pool, flow DSL, thresholds. See [load-engine.md](load-engine.md). |
| Metrics | `pkg/engine/metrics` | Phase 3 | Samples → HDR histograms → sinks. |
| Plugin host | `pkg/engine/plugin` | Phase 4 | wazero runtime, ABI v1, registry. See [plugins.md](plugins.md). |
| Importers | `pkg/engine/importer/postman` | ✅ Phase 1 | See [postman-import.md](postman-import.md). |
| Reporters | `pkg/engine/report` | Phase 2–3 | JSON, HTML, JUnit, CSV. |

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
| **React + Vite + TanStack Query** | Mature ecosystem. Monaco is available for editors, and uPlot handles high-frequency live charts. |
| **goja** | Pure-Go JS engine. One runtime per VU keeps scripts isolated. |
| **wazero** | Pure-Go WASM runtime: sandboxed, cross-platform plugins with no CGO. See [ADR 0002](adr/0002-wasm-plugins.md). |
| **In-house Socket.IO client** | Go's third-party clients are weak, and owning the protocol keeps load mode efficient. See [ADR 0003](adr/0003-socketio-in-house.md). |
| **One YAML file per request** | Small diffs and easy merges, and it works with any editor. See [ADR 0004](adr/0004-yaml-file-per-request.md). |
| **HDR histograms** | Accurate percentiles that can be merged across VUs and agents. |
| **SQLite without CGO** | Nothing to install, and cross-compiling stays simple. |

## Security model
- `pyro serve` binds to `127.0.0.1` and prints a URL carrying a random per-launch token. Every `/api` call must send `X-Pyro-Token`. `--no-token` only works on loopback.
- The store rejects path traversal (`..`, empty or `.` segments, backslashes, drive letters).
- Plugins get only the capabilities they declare and the user approves on install.
- Load tests against non-allow-listed hosts need confirmation.
