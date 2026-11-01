# Changelog

## v0.1.0 (unreleased)

First public release.

**API client**
- HTTP requests with variables and environments, auth (basic, bearer, API key, digest,
  OAuth 2.0) inherited from collections and folders, a response viewer with
  timings, cookies and history.
- WebSocket and Socket.IO consoles (Engine.IO v4 / Socket.IO v5: namespaces, events, acks,
  binary attachments; WebSocket or HTTP long-polling with automatic fallback).
- `pm.*`-compatible pre-request and test scripts (bundled chai), declarative assertions and
  extractors.
- Collection runner in the UI and `pyro run` for CI (data files, JUnit and JSON reports).
- Import Postman collections and environments (v2.0, v2.1), OpenAPI 3 and Swagger 2,
  HAR recordings and cURL commands, into a new or an existing collection. Export
  collections and environments to Postman v2.1.
- Workspaces are plain YAML files, one per request, made for git.

- Environments and globals editor showing initial (file) and current (session) values;
  `globals.yaml` for workspace-wide variables.
- OAuth 2.0 token fetching: client credentials, password, authorization code with PKCE
  (browser sign-in), refresh tokens. Digest auth (MD5, SHA-256).
- Cookie manager (cookies survive restarts); saved response examples (also imported from Postman and OpenAPI and
  exported to Postman); a light theme.

**Load testing**
- Five executors (constant and ramping VUs, per-VU iterations, constant and ramping arrival
  rate), a flow language (think time, groups, loops, conditions, weighted choices, once,
  WebSocket holds), data feeders and thresholds with abort-on-fail.
- Profiles (smoke, load, stress, spike, soak, breakpoint), a visual designer, a live
  dashboard, saved runs, run comparison and self-contained HTML reports; `pyro load` for CI.
- Distributed tests across `pyro agent` machines with exactly merged percentiles; agents can
  listen or connect out to the controller (behind NAT).
- A requests-per-second cap (`maxRPS`, `--max-rps`, `loadMaxRPS` in `pyro.yaml`).
- Live outputs to InfluxDB, Prometheus remote write, NDJSON and CSV.
- The load generator's own CPU and memory, with a warning when it saturates.

**Plugins**
- WebAssembly plugins (wazero) adding request types and auth types, with capability
  approval, a Go SDK, `pyro plugin` commands, archives and index installs. Examples: raw
  TCP and HMAC signing.

**Security**
- Loopback-only server with a session token and DNS-rebinding protection; request bodies
  can't read files outside the workspace by default; see [SECURITY.md](SECURITY.md).
