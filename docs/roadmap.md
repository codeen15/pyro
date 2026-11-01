# Roadmap

| Phase | Scope | Exit criteria | Status |
|---|---|---|---|
| **0. Foundations** | Monorepo, data model, workspace store, `pyro serve` with embedded UI shell, token auth, `init --sample` | `pyro serve` serves the UI and workspace API from one binary | ✅ Done |
| **1. API client MVP** | HTTP driver (timings, cookies, TLS, proxy), variables and environments, auth inheritance, editable request editor, response viewer, history (SQLite), Postman importer without scripts, product/design pass (PRODUCT.md, DESIGN.md) | A real Postman collection imports and its requests send | ✅ Done (deferred: OpenAPI-generated TS client, digest auth, OAuth 2.0 token fetching, environment editor UI) |
| **2. Realtime + scripting** | WebSocket and Socket.IO drivers and consoles, event WebSocket, goja + `pm.*` shim, assertions and extractors, collection runner, CLI `run`, JUnit/JSON reporters | Imported `pm.test` scripts pass; `pyro run` works in CI | ✅ Done (deferred: Socket.IO binary attachments and long-polling, most `require()` modules) |
| **3. Load engine** | Scheduler, executors, VU pool, flow DSL, data feeders, metrics pipeline, thresholds, profiles, load designer and live dashboard, CLI `load`, HTML report | 5k HTTP VUs and 2k concurrent WebSockets on a laptop with stable metrics; percentiles within tolerance of k6 | ✅ Done: 5000 VUs at ~4.7k req/s (1 core, 170 MB), 2000 WebSockets; p95 within 2 ms of a fixed 50 ms target. Not yet compared with k6 (not installed here). Deferred: load-generator CPU/memory gauges, CSV/NDJSON outputs, RPS cap |
| **4. Plugins** | wazero host, ABI v1, manifest and capabilities, Go/Rust SDKs, registry and `pyro plugin`, raw-TCP example, Plugin Manager UI | A third-party protocol plugin works in manual and load modes | ✅ Done: raw TCP plugin in manual send, collection runs and load tests; HMAC auth plugin. Deferred: Rust SDK, streaming plugins, other plugin kinds, public index, signed entries |
| **5. Scale & polish** | Distributed agents (gRPC), Prometheus/InfluxDB sinks, run comparison, OpenAPI/HAR/cURL import, Postman export, optional Tauri wrapper | A multi-node test produces correctly merged results | Next |

## Risks and mitigations
| Risk | Mitigation |
|---|---|
| `pm.*` compatibility is a deep rabbit hole | Support the most-used APIs first and report the rest in the import report. Measure coverage against real collections. |
| Script cost under load | One goja runtime per VU, pooled. Push declarative assertions and extractors. Benchmark in Phase 2 before the load engine depends on it. |
| Coordinated omission skews latency | Arrival-rate executors; measure from the intended start time. |
| The load generator becomes the bottleneck | Expose Pyro's own CPU and memory and warn when they saturate. Offer distributed agents in Phase 5. |
| Plugin security | Capability-gated host functions, approval on install, signed index entries, memory and fuel limits. |
| UI performance with high-frequency metrics | 1s aggregation ticks, uPlot for charts, virtualized tables. |
| Socket.IO protocol edge cases | Conformance tests against the official Node server, versions 4.x, in CI. |
| Scope creep from Postman feature parity | Non-goals in the [PRD](PRD.md). New protocols ship as plugins. |
