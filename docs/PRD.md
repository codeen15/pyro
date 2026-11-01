# Pyro — Product Requirements

## Problem
Teams use one tool to design and debug APIs (Postman, Insomnia) and a different
tool to load test them (JMeter, k6, Gatling). The API definitions get re-created
in each tool, drift apart, and performance testing happens late or never.

## Vision
One application where an API is defined once and then:
1. sent by hand while developing and debugging,
2. run as an automated functional suite (locally or in CI), and
3. executed by thousands of virtual users for stress, spike, soak and concurrency tests.

It is extensible through installable plugins, as JMeter is.

## Target users
| User | Needs |
|---|---|
| **API developers** | Fast request editing, environments, auth, readable responses, realtime (WS/Socket.IO) consoles, Postman migration. |
| **QA engineers** | Collection runs with assertions, data-driven iterations, CI-friendly reports (JUnit). |
| **Performance engineers** | Realistic load profiles, accurate percentiles, thresholds that fail builds, distributed load, extensibility. |

## Goals
- **API client:** REST/HTTP, WebSocket and Socket.IO with environments, variables, auth, scripts, assertions, history.
- **Postman import:** Collection v2.0/v2.1 and environment exports, with a report of anything that could not be converted.
- **Load testing:** closed (VU-based) and open (arrival-rate) models; ready-made Smoke/Load/Stress/Spike/Soak/Breakpoint profiles; live dashboard; thresholds.
- **Plugins:** sandboxed WASM plugins that add protocols, auth, assertions, data sources, reporters and importers.
- **Cross-platform:** one binary for Linux, macOS and Windows, using the browser as the UI. It also runs headless in CI.
- **Git-friendly:** workspaces are plain YAML files, one request per file.

## Non-goals (for now)
- Hosted team sync, cloud accounts or a SaaS backend. Collaboration happens through git.
- API mocking servers and API documentation publishing.
- GraphQL subscriptions, gRPC, MQTT and raw TCP/UDP as **built-ins**. They are expected to arrive as plugins.
- Full Postman script parity. The most-used `pm.*` APIs are supported, and the rest are reported on import.
- A native desktop shell. A Tauri wrapper is optional in Phase 5.

## Key requirements
| Area | Requirement |
|---|---|
| Protocols | HTTP/1.1 + HTTP/2, TLS/mTLS, proxies, cookies; WebSocket (text/binary, subprotocols); Socket.IO v4/v5 (namespaces, events, acks). |
| Scripting | Pre/post JS scripts with a `pm.*` compatibility layer and a native `pyro.*` API. |
| Assertions | Declarative status, header, JSONPath, body and duration checks; extractors into variables. |
| Load | 5k+ HTTP VUs and 2k+ concurrent WebSocket connections on a laptop; HDR-histogram percentiles; no coordinated omission in arrival-rate mode. |
| Reports | JSON, HTML, JUnit, CSV; non-zero exit code when thresholds fail. |
| Security | The local server binds to loopback with a per-launch token; plugins run sandboxed with capabilities the user approves; tests that target non-local hosts need confirmation. |

## Success measures
- A real Postman collection imports and its requests send successfully with no manual edits in the common case.
- Imported `pm.test` scripts pass in `pyro run`.
- Load test percentiles stay within tolerance of a reference tool (k6) against the same fixed-latency target.
- A third-party protocol plugin works in both manual and load modes.

## Roadmap
See [roadmap.md](roadmap.md).
