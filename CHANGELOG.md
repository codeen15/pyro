# Changelog

## v0.2.1 (2026-11-25)

- Team server: someone with no projects left (after deleting their last one, or being removed
  from every workspace) now gets **You don't have any projects** with **Create project** and
  **Sign out**, instead of a page with no way forward.

## v0.2.0 (2026-11-25)

- Run a load agent from environment variables alone: `PYRO_MODE=agent` makes the image's
  default command (`serve --team`) start `pyro agent`, and `PYRO_AGENT_CONNECT` sets
  `--connect`. Platforms such as Dokploy can run an agent as an Application with no command
  override (the image has no shell). The **Connects in…** dialog shows these settings ready to
  paste, next to the `docker run` and `pyro agent` commands.

- Manage load agents from the team server's UI: **Administration → Agents** lists every agent
  with its status, version, CPUs and max VUs, and administrators add, test, edit, rotate and
  remove them without flags or a restart. An agent is either direct (the server dials its
  address) or connects in (`pyro agent --connect <server> --token <token>` on the server's own
  address and port, so it works through Dokploy's Traefik or by IP on an internal network; the
  app shows the `docker run` and `pyro agent` commands once). Every agent has its own token:
  direct agents' tokens are stored encrypted (key derived from the server's JWT secret),
  connecting agents' tokens only as a hash. Failed dial-in attempts are rate limited per
  address (`429` after 5 in a minute). `--agent` flags on `serve --team` still work and show
  as read-only entries. See "Load agents on other machines" in the guide.
- API change: `GET /api/v1/agents` now returns one status object per agent,
  `[{id, name, mode, address, status, detail, version, cpus, maxVUs, encrypted, fromFlags, lastSeen}]`
  (`mode` is `direct` or `connect`, `status` is `available`, `busy`, `offline` or `error`),
  instead of `[{mode, address, info?, error?}]`, and needs the editor role (viewers get `403`).
  `POST /api/v1/loadtests/{name}/start` accepts `agents: [id]` to run on just those agents.
- Agents that get no share of a small load test (fewer VUs than agents) are handed back when
  the test starts instead of staying busy.
- Pick agents per load test run: **Run on: Agents (n available)** shows a checklist with every
  available agent ticked; busy, offline and error agents are greyed out with the reason, and
  what you untick is remembered per project and load test. The start request takes
  `agents: [id]`. Agents are now claimed only after the other start checks pass (a refused
  start used to leave dial-in agents claimed), and results name agents by their registry names.
- Deploying the team server anywhere: a Docker image (`ghcr.io/codeen15/pyro`, amd64 and
  arm64, non-root, data on `/data`), a Compose file that pulls it or builds it from source, a
  systemd unit for machines without Docker, and the same for load agents (`pyro agent`) in
  `deploy/`. All settings come from `PYRO_*` environment variables, including an existing
  PostgreSQL through `PYRO_DATABASE_URL`. See "Deploying the team server" in the guide.
- Plugin building: `pyro plugin new --lang go|js` creates a plugin from a template;
  `pyro plugin dev <dir>` rebuilds (Go) or reinstalls (JavaScript) on every save, approves the
  manifest's permissions for you and prints them; `pyro plugin test <dir|name>` runs one call
  from the terminal and prints the response, the call time and the plugin's log lines.
  `pyro serve`, `serve --team` and `pyro agent` watch the plugins folder, so a plugin installed
  from the terminal shows up in the open app (the request Type menu, the Plugins tab) within
  about a second.
- JavaScript plugins: set `runtime: js` and `module: plugin.js` in the manifest and write the
  plugin as one script with the `pyro` object (`registerProtocol`, `registerAuth`,
  `registerProcessor`, `dial`, `log`, `encoding`, `crypto`); there is no build step. They run on
  an embedded JavaScript engine under the same sandbox, permissions and approval as Go
  plugins, but about 17 times slower, so prefer Go for heavy load tests. The first one makes
  Pyro compile the engine once (several seconds). Release archives include an example,
  `plugin-redis-js`.
- Plugin limits are enforced: `limits.memoryMB` (default 128 MB, 256 MB for JavaScript, at most
  1024; a changed limit needs approval again) and `limits.maxInstances` (calls beyond it wait,
  then fail with `plugin <name> is busy`). Load tests warn when a plugin's cap is below the
  test's peak VUs. Plugins get the real wall clock, a real monotonic clock and cryptographic
  random numbers, but no real sleep. Writes to `state.json` are locked, so the CLI and a
  running server no longer overwrite each other's changes. The Go plugin SDK gains `HandleOp`.
  A plugin past its memory limit fails with
  `plugin <name> ran out of memory (limit <n> MB; raise limits.memoryMB)`.
- **Upgrade note:** all plugins used to share one 512 MB memory ceiling; now each plugin is
  held to its own `limits.memoryMB` (default 128 MB for Go/WebAssembly plugins, 256 MB for
  JavaScript). A plugin that needs more must declare it in its manifest (and be approved again).

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
- The Socket.IO log has a **Frames** view showing the raw packets as sent and received
  (`42["event",{...}]`), including the handshake and its `sid`. Packets the client used to drop
  silently, such as replies on a namespace you didn't join, now show in the log.
- Socket.IO takes the namespace from the URL's path when none is set, as `socket.io-client`
  does: `https://host/chat` joins `/chat` (it used to join `/` and ignore the path).

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
