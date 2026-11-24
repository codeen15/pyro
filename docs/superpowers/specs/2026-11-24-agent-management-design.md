# Managing load agents from the team server UI

Status: draft for approval · 2026-11-24 · size: project

## Intent

On a team server, an administrator manages load agents entirely from the web UI: add, test,
see status, remove. No `--agent` flags, no restart. The team server runs on a VPS under
Dokploy (or any host, including an internal network reached by IP only), so changing
command-line flags is awkward. Agents run in two kinds of places: VPSes the server can
reach, and machines behind NAT or a firewall that can only dial out.

## Goals
1. An **Agents** tab on the admin page: list, add, test, remove agents; live status.
2. Two connection directions, both managed in the UI:
   - **Direct**: the server dials the agent (`10.0.0.5:7979`, `https://agent1.example.com`).
   - **Connects in**: the agent dials the server on the same address and port as the UI
     (`pyro agent --connect https://pyro.example.com --token …` or `http://10.0.0.1:7878`).
3. A per-agent token for each agent; removing an agent revokes its access at once.
4. Load tests pick agents per run: all available agents ticked by default, busy/offline
   ones shown greyed out with the reason.
5. Works with an IP-only, plain-HTTP internal network (with a visible "not encrypted"
   note), and behind Dokploy's Traefik with HTTPS.

## Non-goals
- Local `pyro serve` keeps its flags (`--agent`, `--agent-listen`); no UI management there.
- Per-workspace agent assignment or quotas (everyone with load-test rights sees all agents).
- Pushing plugins to agents (plugins are still installed on each agent).
- Agent auto-update, remote shell, logs.
- Changing the agent program or its protocol (`pkg/engine/agent` Protocol 1 stays).

## Anti-goals (would make this a failure even if it works)
- A stored agent token readable from the database in plain text.
- Removing one agent disturbs others or leaves the removed agent able to take jobs.
- Breaking existing deployments that use `--agent` / `--agent-token` with `serve --team`.
- The dial-in endpoint opening a way in for anything other than a registered agent token.

## Approaches
- **Chosen (A): one registry in the team database; dial-in agents use the server's normal
  HTTPS address** (`/agent/v1/connect` on the team server's own listener). Works through
  Dokploy's Traefik with no extra port or domain; per-agent tokens.
- (B) Same registry, dial-in agents on a separate port (`--agent-listen :7980`): reuses the
  hub as is, but needs a second port/route and TLS through the proxy — the ops work this
  feature removes. Dropped.
- Environment-variable configuration with a read-only UI: doesn't meet "manage from the UI".
  Dropped.

## Design

### Data
New team-store migration (append-only, Bun models, SQLite + PostgreSQL, Unix-integer times):

`agents` table: `id` (text, random), `name` (unique, 1–64 chars), `mode` (`direct` |
`connect`), `address` (direct only: `host:port` or `http(s)://…`), `token_enc` (direct only:
AES-256-GCM ciphertext of the agent's token, nonce-prefixed, base64), `token_hash`
(connect only: SHA-256 of the generated token, like API tokens), `insecure_tls` (direct:
skip TLS verification for self-signed agents), `created_by` (user id), `created_at`,
`last_seen_at` (connect: last connection; direct: last successful Info).

Encryption key for `token_enc`: HKDF-SHA256 from the server's JWT signing secret
(`jwt.secret` / `PYRO_JWT_SECRET`) with info `"pyro agent tokens v1"`. If the secret
changes, decryption fails: the agent shows status **error: "token can't be read (the
server's secret changed); edit the agent and enter its token again"**.

Connect tokens: 32 random bytes, shown once as `pyro_agent_<base64url>`, never stored in
clear.

### Server side (team)
- **Registry service** in `internal/team` (`agents.go`): CRUD on the table, encrypt/decrypt,
  token generation, and `Workers(ids []string) ([]load.Worker, error)` /
  `Status() []AgentStatus` used by project servers.
- **Dial-in endpoint**: the team server mounts `GET /agent/v1/connect` on its main router,
  outside session auth and CSRF (it is a GET + WebSocket upgrade, authenticated by the
  agent's bearer token). The token is hashed and looked up; unknown → 401; the agent's
  protocol header is checked as today. Accepted connections are held by the hub keyed by
  agent id.
- **Hub changes** (`pkg/engine/agent/hub.go`, no protocol change): an optional
  `Authenticate func(token string) (id string, ok bool)` replaces the single `Token` when
  set; idle connections are tracked per id (a new connection from the same id replaces the
  old one); new `ClaimIDs(ids []string) ([]load.Worker, []string /*unavailable*/)` and
  `Disconnect(id string)` (used on remove). Existing `Claim`/`Token` behaviour stays for
  local `--agent-listen`.
- **Project servers** (`internal/server`): `Config` gets an `Agents AgentSource` interface
  (`List(ctx) []agentView`, `Workers(ctx, ids) ([]load.Worker, error)`); the team server
  provides the registry; local `serve` wraps today's flags + hub in the same interface, so
  `internal/server` has one code path. `GET /api/v1/agents` returns id, name, mode, status
  (`available` | `busy` | `offline` | `error`), detail (error text), version, CPUs, maxVUs,
  `encrypted` (false for `http://`/`ws://`), `fromFlags`.
- **Status of direct agents**: the registry runs a background prober (started with the
  team server, stopped on close) that calls each direct agent's `Info` every 15 s (5 s
  timeout) and caches status, version, CPUs and maxVUs; `GET /api/v1/agents` and the admin
  list read the cache (an admin **Test** probes immediately and updates it). Busy means the
  agent reported `busy` or is in a run started by this server. Status changes, hub
  connect/disconnect/claim/release and admin edits publish an `agents` event.
  `AgentSource` has `Subscribe(func()) (cancel func())`; each project `Server` subscribes in
  `New` and republishes `agents`/`changed` on its own event hub, cancelling in `Close`
  (the same pattern as the `plugins` event).
- **One server process**: agents are tracked in memory by the one team server process, as
  the guide already requires ("Run one Pyro server per data directory"). Several processes
  sharing a database are not supported for dial-in agents; the guide says so.
- **Names in results**: in team mode a worker's name is the registry name (the hub/client
  worker is wrapped to report it), so run reports and the Agents table show the admin's
  names. Local `pyro serve` keeps today's names; its dial-in agents get the id
  `hub:<name>@<remote address>` and appear with `fromFlags: true`.
- **Claim after checks (fixes an existing bug)**: today `startLoadTest` claims hub agents
  (`Claim(0)`) before the "already running", run-slot and `load.Start` checks, so a rejected
  start leaks the claimed dial-in agents. Workers are now obtained after those checks, and a
  failure between claim and a successful start releases them (`Release(workers)` on the
  source).
- **Start a distributed test**: request body gains `agents: [ids]`. Empty/omitted with
  `distributed: true` means "all available" (keeps today's API behaviour). Unavailable ids
  at start → 409 with which and why. Each agent runs one test at a time (as today).
- **Flags stay**: agents from `--agent` on `serve --team` appear in the list as read-only
  entries (`fromFlags: true`, id `flag:<address>`), using `--agent-token`. `--agent-listen`
  remains rejected in team mode (dial-in now goes through the main port).
- **Admin API** (team, admin only, CSRF): `GET /api/v1/admin/agents`,
  `POST /api/v1/admin/agents` (`{name, mode, address?, token?, insecureTLS?}` → for
  `connect` the response includes `token` and `command` once), `PATCH
  /api/v1/admin/agents/{id}` (rename; direct: address/token/insecureTLS), `DELETE
  /api/v1/admin/agents/{id}` (deletes, revokes, disconnects a connected agent), `POST
  /api/v1/admin/agents/{id}/test` (direct: Info call; connect: whether it's connected now),
  `POST /api/v1/admin/agents/{id}/token` (connect: rotate; shows the new command once, drops
  the current connection). Add/remove/rotate are recorded in the server activity log.
- **The generated command** uses `PYRO_PUBLIC_URL` when set, else the request's origin
  (scheme + host the admin's browser used); the UI lets the admin edit the address before
  copying. Two forms: `docker run -d --restart unless-stopped ghcr.io/codeen15/pyro:<server
  version tag> agent --connect <url> --token <token>` and the plain `pyro agent …` line.

### UI
- **Admin → Agents tab** (antd `Table`, `Tag` for mode/status, `Modal` forms, `App.useApp`
  messages; DESIGN.md rules; status shown as neutral text with a small status dot, not
  coloured text):

```
Agents                                              [ Add agent ▾ ]  (Direct… | Connects in…)
Name        How           Address / from          Status        Version  CPUs  Max VUs
vps-sg      Direct        10.0.0.5:7979  ⚠ http    ● available   0.2.0    8     50000   [Test][Edit][Remove]
office-1    Connects in   203.0.113.7             ● busy        0.2.0    4     50000   [Rotate][Remove]
lab-2       Connects in   —                       ○ offline (last seen 3 h ago)        [Rotate][Remove]
flag:…      Direct        from server flags       ● available                           (read-only)
```

  - *Add direct*: name, address, token, "skip certificate check" → **Test** then **Save**
    (save is allowed when the test fails, with a warning).
  - *Add connecting*: name → shows the command once (copy buttons), a "waiting for the agent
    to connect…" state that turns into **connected** live.
  - Remove asks for confirmation.
- **Load test page**: "Run on" → **Agents** opens a checklist under Start:

```
Run on: ( ) This server  (•) Agents     3 of 4 selected
  [x] vps-sg     8 CPUs   available
  [x] office-1   4 CPUs   available
  [ ] lab-2               offline (last seen 3 h ago)      ← greyed, not selectable
  [x] flag:…     8 CPUs   available
```

  Default: all available ticked; the selection is remembered per load test in the browser.
- Live status: the agents list refreshes on a new event topic `agents` (team server
  publishes on connect/disconnect/busy/idle and on admin changes) plus a 15 s refetch.

### Security
- Admin-only management; viewing/selecting agents needs the load-test role that starting a
  test already needs (editor).
- Tokens: direct ones encrypted at rest; connect ones hashed; generated tokens shown once.
- `/agent/v1/connect` rate-limits failed token attempts per remote address (reuse whatever
  sign-in throttling exists; otherwise a simple per-IP backoff).
- Plain `http://` addresses and a plain-HTTP public URL are allowed with a visible "not
  encrypted" note.
- SECURITY.md gets the agent boundary: who can add agents, what an agent token allows, and
  that a direct agent's token is recoverable by the server (it must present it).

### Docs
`docs/guide/team-server.md` ("Load agents on other machines" becomes UI-first; flags stay
as an alternative), `docs/guide/distributed.md`, `docs/api.md` (admin agent endpoints,
`agents` field on start, `agents` event topic, `/agent/v1/connect` on the team server),
`SECURITY.md`, `CHANGELOG.md`, `docs/roadmap.md` (phase entry).

## Testing
- Store: CRUD, uniqueness, encryption round-trip, wrong-secret → readable error, hash
  lookup; runs on SQLite and PostgreSQL (`dbtest.Open`).
- Hub: per-id auth, replacing a connection from the same id, `ClaimIDs` with
  available/busy/missing ids, `Disconnect`; existing single-token tests unchanged.
- Team server (httptest): add direct agent (in-process `httptest` agent) → list shows
  available; add connect agent → a real `agent.Server.Connect` to the team server's
  `/agent/v1/connect` with the generated token → list shows connected; wrong token → 401;
  remove → the connected agent is dropped and its token no longer works; rotate → old
  token refused.
- Distributed start with selected ids runs on exactly those agents; unavailable id → 409;
  omitted ids → all available.
- `--agent` flags on `serve --team` still work and show as read-only.
- UI: e2e — admin adds a connecting agent (the spec starts `pyro agent --connect` with the
  shown token), sees it come online, runs a load test on it with the picker.

## Constraints
- Pure Go, no CGO, no new dependencies beyond the standard library (`crypto/hkdf` is in the
  standard library in this Go version; otherwise implement HKDF with `crypto/hmac`).
- `pkg/engine` never imports `internal/...`; the hub stays generic (callback for auth).
- Database changes are new migration steps only.
- Every route gets a minimum role (`TestEveryRouteHasARole`).
- Agent protocol unchanged; existing agents (any version speaking Protocol 1) work.

## Calls made without asking (check these)
- Team server only; local `pyro serve` keeps flags.
- Flag-configured agents appear read-only in the list.
- Encryption key derived from the JWT secret (changing that secret means re-entering direct
  agents' tokens).
- Editors (anyone who can start load tests) can see and pick agents; only admins manage them.
- Selection remembered per load test in the browser, not on the server.
- Plain HTTP allowed with a warning rather than blocked.
- Direct agents are probed every 15 s in the background; the UI reads cached status.
- One team server process (already the documented rule); no cross-instance agent presence.
- Reports show the registry name for agents in team mode.
- Fix the existing claim-before-checks leak as part of this work.

## Left to the builder
- Exact table/column names and Bun model shapes; the event payloads.
- Rate-limit mechanics for failed agent tokens.
- Status-dot styling within DESIGN.md.
