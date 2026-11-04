# Changelog

## Unreleased

- WebSocket and Socket.IO requests have an action, chosen when created: **Connect** (the
  console, now with a connection name), **Send & receive** (send messages, then wait for a
  matching message, an ack or a set time), **Ping** and **Close**. Steps reach a connection by
  name, so HTTP requests can run in between and feed their results into messages. They run by
  hand, in the collection runner (which no longer skips socket requests) and in load tests
  (`ws_step_duration`, `ws_step_failed`, `ws_ping`). Consoles stay connected when you open
  other requests; `GET /api/v1/connections` lists open connections.
- The WebSocket and Socket.IO console saves the message you're composing with the request
  (`compose`). Clearing or half-typing a JSON argument no longer drops the argument list and
  switches to raw JSON.

- Response bodies: the Pretty view is a collapsible JSON tree, with expand all and collapse
  all.
- Collection and folder settings: `Ctrl` + `S` saves; variables show initial and current
  values.
- Current values show what requests set (extractors, set-variables processors, scripts),
  marked *from a request*, on the settings and environments pages.
- The `{{variable}}` card stays open while you use it, and appears in assertion and extractor
  fields too; those fields now fill in `{{variables}}` when they run.
- Sidebar names use the whole row, with the buttons over the end on hover and a tooltip for
  cut-off names; more room at the bottom of the request editor.
- Local builds stamp the app version, not the plugin SDK tag.

## v0.1.0 (2026-11-04)

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
  HAR recordings, JMeter test plans (as requests plus a load test) and cURL commands, into a new or an existing collection. Export
  collections and environments to Postman v2.1.
- Workspaces are plain YAML files, one per request, made for git.

- Environments and globals editor showing initial (file) and current (session) values;
  `globals.yaml` for workspace-wide variables.
- OAuth 2.0 token fetching: client credentials, password, authorization code with PKCE
  (browser sign-in), refresh tokens. Digest auth (MD5, SHA-256).
- Cookie manager (cookies survive restarts); saved response examples (also imported from Postman and OpenAPI and
  exported to Postman); a light theme.

- Collection and folder settings (variables, auth, scripts, description) in the UI.
- Folders inside collections; deleting requests, folders and collections; a request's type
  (HTTP, WebSocket, Socket.IO, plugin) is chosen once, when it's created.
- `{{variables}}` are coloured by whether they're defined, with a hover card to see and edit
  the value where it lives.
- JMeter-style think time (constant, uniform, Gaussian, Poisson timers) and ordered pre- and
  post-processors (scripts, set variables, counters, extractors incl. boundary and XPath,
  assertions) on requests, folders and collections; a `processor` plugin kind.
- Socket.IO messages with several typed arguments (text, JSON, number, boolean, binary) and
  WebSocket payloads as text, JSON or binary, as in Postman.
- An Ant Design interface with the Pyro brand, light and dark, and Hugeicons.
- A complete user guide, in `docs/guide/` and in the app under **Docs**.
- Generated HTML API docs (`pyro docs`) and a mock server answering from saved examples
  (`pyro mock`), both also in the UI.

**Load testing**
- Five executors (constant and ramping VUs, per-VU iterations, constant and ramping arrival
  rate), a flow language (think time, groups, loops, conditions, weighted choices, once,
  WebSocket holds), data feeders and thresholds with abort-on-fail.
- Profiles (smoke, load, stress, spike, soak, breakpoint), a visual designer, a live
  dashboard, saved runs, run comparison and self-contained HTML reports; `pyro load` for CI.
- Distributed tests across `pyro agent` machines with exactly merged percentiles; agents can
  listen or connect out to the controller (behind NAT).
- Script steps: JavaScript with a `pyro.*` API (requests, checks, groups, custom metrics).
- A requests-per-second cap (`maxRPS`, `--max-rps`, `loadMaxRPS` in `pyro.yaml`).
- Live outputs to InfluxDB, Prometheus remote write, NDJSON and CSV.
- Test data from CSV files (header row or JMeter-style variable names, any delimiter),
  mapped to variables; Socket.IO and WebSocket steps that send messages.
- Load tests can be renamed, duplicated and deleted.
- The load generator's own CPU and memory, with a warning when it saturates.

**Test reports**
- Every collection run is saved as a report (secrets masked) and listed with load test runs:
  pass rate, failures, comparison with the previous run, shareable links.
- Save as PDF (printable HTML), Excel workbooks, CSV, JUnit and JSON, from the app and from
  `pyro run` / `pyro load` (`--html`, `--xlsx`, `--csv`); `--report-to` uploads CI runs to a
  team server.

**Team server** (`pyro serve --team`)
- Accounts with email and password, LDAP / Active Directory, or single sign-on with OpenID
  Connect (Entra ID, Google, Keycloak…); first account is the administrator.
- Sign-in with JWTs: short-lived access tokens and refresh tokens in HttpOnly cookies for
  browsers, Bearer tokens and personal access tokens (`pyro_pat_…`) for scripts and CI.
- Personal and team workspaces holding projects (each one an ordinary workspace folder),
  with Owner, Editor and Viewer roles enforced on every route.
- Invites by link or email, members and roles, an activity log, moving and copying
  collections between projects, moving projects, project zip download and upload.
- Safe concurrent editing: saves that would overwrite someone's newer change ask first;
  everyone's sidebar follows changes live.
- An administration page (users, administrators, disabling accounts, password reset links),
  "forgot password" emails, `pyro admin` commands.
- SQLite by default or PostgreSQL (`PYRO_DATABASE_URL`) through the Bun ORM; settings from
  `server.yaml`, environment variables or a `.env` file.

**Plugins**
- WebAssembly plugins (wazero) adding request types, auth types and processors, with capability
  approval, a Go SDK, `pyro plugin` commands, archives and index installs. Examples: raw
  TCP and HMAC signing.

**Security**
- Loopback-only server with a session token and DNS-rebinding protection; request bodies
  can't read files outside the workspace by default; see [SECURITY.md](SECURITY.md).
- Team server: argon2id passwords, CSRF protection, rate-limited sign-in, hashed tokens and
  invite links, per-route roles with a test that every route has one.
