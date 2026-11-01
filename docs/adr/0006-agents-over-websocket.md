# 0006 — Distributed agents: controller dials agents over WebSocket + JSON

**Status:** Accepted (2026-11-02).

## Context
The plan for Phase 5 had `pyro agent` nodes register with a controller over gRPC. When
building it we weighed:
- gRPC needs `google.golang.org/grpc` plus protobuf code generation (`protoc` and plugins)
  in the build. Pyro builds with the Go toolchain alone today.
- Pyro already depends on `coder/websocket` and speaks JSON everywhere else.
- The controller is often a short-lived `pyro load` in CI, not a server that agents could
  register with.
- The bulk of the traffic is one report per agent per second: per-series counters plus
  the non-empty HDR buckets of each trend. That's kilobytes, not megabytes.

## Decision
- An agent **listens** (`pyro agent`, default port 7979) and requires a bearer token.
  The controller (`pyro load --agent …`, or `pyro serve --agent …`) **dials** each agent.
- One WebSocket per agent per test, carrying JSON messages: `job` (the plan segment with
  requests, environment, data rows and body files), `ready`, `begin`, `report` once per
  tick, `stop`, `done`. Closing the connection aborts the job.
- Agents ship **metric deltas**, not summaries: counts, sums and HDR bucket counts per
  series (metric and tags). The controller merges them into its own aggregator, so
  percentiles and thresholds are computed over every sample, exactly as if the test ran
  in one process.
- The agent protocol is versioned (`Pyro-Agent-Protocol: 1`).

## Consequences
- No codegen step and no new dependency; agents use the same transport as the UI.
- Agents need an open inbound port. Agents behind NAT that can only dial out aren't
  supported yet (a reverse "agent dials controller" mode could be added later on the same
  messages).
- JSON reports are larger than protobuf would be. With one report per agent per second
  this is negligible next to the load itself.
- If an agent fails mid-test, the controller stops the others and fails the test: results
  with a missing share of the load would be misleading.
