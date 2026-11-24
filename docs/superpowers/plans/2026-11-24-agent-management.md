# Agent Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Team-server admins manage load agents from the UI (direct agents the server dials, and agents that connect in over the server's own address), each with its own token, and load tests pick agents per run.

**Architecture:** A registry in the team database (encrypted direct tokens, hashed connect tokens) feeds an `AgentSource` interface that project servers use for listing, picking and claiming workers. The agent hub gains per-agent authentication and id-based claiming and is mounted on the team server's main router at `/agent/v1/connect`. Local `pyro serve` wraps its flags in the same interface.

**Tech Stack:** Go (Bun ORM, chi, coder/websocket, crypto/hkdf, crypto/aes), React + TS + antd + react-query, Playwright.

**Spec:** `docs/superpowers/specs/2026-11-24-agent-management-design.md`

## Global Constraints
- Pure Go, CGO off, no new third-party dependencies. `pkg/engine` never imports `internal/...`.
- Database: new `db.Migration` step appended to `internal/team/models.go` `migrations`; never edit old steps; Bun query builder; Unix-integer times; tests with `dbtest.Open` (SQLite, and PostgreSQL when `PYRO_TEST_POSTGRES_URL` is set).
- Agent protocol unchanged (`agent.Protocol = 1`); existing agents work.
- Every route has a minimum role (`TestEveryRouteHasARole`). Admin agent routes: admin + CSRF. `GET /api/v1/agents`: editor.
- Key: HKDF-SHA256 from the JWT secret, info `"pyro agent tokens v1"`; AES-256-GCM, nonce-prefixed, base64.
- Connect tokens: `pyro_agent_` + base64url(32 random bytes); stored as SHA-256 hex.
- Statuses: `available`, `busy`, `offline`, `error`. Direct probe every 15 s, 5 s timeout.
- Wrong-secret error text: `token can't be read (the server's secret changed); edit the agent and enter its token again`.
- Event topic `agents`, type `changed`.
- UI: antd components, `App.useApp()` for messages, icons via `web/src/icons.tsx`, status as neutral text with a status dot (no coloured text), new colours need light and dark tokens in `web/src/styles.css`.
- Docs updated with every user-visible change.

## Review Focus
1. **Agent removed while connected or mid-run**: it must stop taking new jobs at once; a run already using it ends (fails naming the agent) rather than hanging. Test in Task 5 (remove while connected → connection dropped; remove during a run → run ends within 30 s).
2. **Same connect token used by two machines** (copied command run twice): the newer connection replaces the older; only one appears. Test in Task 3.
3. **Server restarted**: direct agents reload from the database and are probed; connect agents reconnect by themselves (agent backoff) and show online again. Test in Task 5 (new registry on the same DB lists agents; a connected agent re-dials after the server's listener restarts).
4. **Rejected start does not leak agents** (existing bug): starting while a run is already going, or with an unavailable selected agent, leaves every agent available. Test in Task 4.
5. **Agent offline at start**: a selected agent that went offline since the page loaded gives a 409 naming it, not a hang. Test in Task 4.

---

### Task 1: Token crypto helpers

**Files:**
- Create: `internal/team/agentcrypto.go`, `internal/team/agentcrypto_test.go`

**Interfaces:**
- Produces:
  - `type tokenBox struct{ aead cipher.AEAD }`; `func newTokenBox(jwtSecret []byte) (*tokenBox, error)`
  - `func (b *tokenBox) seal(token string) (string, error)`; `func (b *tokenBox) open(enc string) (string, error)` (returns `errTokenUnreadable` on auth failure)
  - `var errTokenUnreadable = errors.New("token can't be read (the server's secret changed); edit the agent and enter its token again")`
  - `func newConnectToken() (token, hash string, err error)`; `func hashAgentToken(token string) string`

- [ ] **Step 1: Failing tests**: `TestTokenBoxRoundTrip` (seal → open equals input; two seals of the same token differ); `TestTokenBoxWrongSecret` (open with a box from a different secret → `errors.Is(err, errTokenUnreadable)`); `TestConnectToken` (prefix `pyro_agent_`, length 11+43, `hashAgentToken(token) == hash`, two tokens differ).
- [ ] **Step 2: Run** `go test ./internal/team -run 'TokenBox|ConnectToken'` — FAIL (undefined).
- [ ] **Step 3: Implement** with `crypto/hkdf` (or HMAC-based HKDF if the package is unavailable in go.mod's Go version), `crypto/aes`, `cipher.NewGCM`, `crypto/rand`, `encoding/base64` (StdEncoding for ciphertext, RawURLEncoding for tokens), `crypto/sha256` hex.
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `Team: agent token encryption and generation`

### Task 2: Agents table and store methods

**Files:**
- Modify: `internal/team/models.go` (`agentRow`, migration step `"N-agents"` appended)
- Create: `internal/team/agents_store.go`, `internal/team/agents_store_test.go`

**Interfaces:**
- Consumes: Task 1 helpers.
- Produces:
  - `type agentRow struct` → table `agents`: `id` pk, `name` unique notnull, `mode` notnull (`direct`|`connect`), `address`, `token_enc`, `token_hash` (indexed), `insecure_tls` bool, `created_by`, `created_at`, `last_seen_at` (all ints are Unix seconds).
  - `type Agent struct{ ID, Name, Mode, Address string; InsecureTLS bool; CreatedBy string; Created, LastSeen time.Time }` (exported, no tokens)
  - `func (s *Store) AddDirectAgent(ctx, name, address, token string, insecure bool, by string) (Agent, error)` — needs the store's `*tokenBox` (add field `box` set by `team.New` from `Config.JWTSecret`).
  - `func (s *Store) AddConnectAgent(ctx, name, by string) (Agent, string /*token*/, error)`
  - `func (s *Store) Agents(ctx) ([]Agent, error)` (sorted by name)
  - `func (s *Store) AgentToken(ctx, id string) (string, error)` (direct; decrypts; may return `errTokenUnreadable`)
  - `func (s *Store) AgentByToken(ctx, token string) (Agent, bool, error)` (connect; by hash)
  - `func (s *Store) UpdateAgent(ctx, id string, name, address, token *string, insecure *bool) (Agent, error)` (nil = unchanged; token only for direct)
  - `func (s *Store) RotateAgentToken(ctx, id string) (string, error)` (connect only)
  - `func (s *Store) DeleteAgent(ctx, id string) error`; `func (s *Store) TouchAgent(ctx, id string, at time.Time) error`
  - Errors: duplicate name → `ErrExists`-style error the server maps to 409 (use the team package's existing conflict error); unknown id → its not-found error; name 1–64 chars after trim, address required for direct (`host:port` or `http(s)://`), else 400.

- [ ] **Step 1: Failing tests** (`dbtest.Open`): add direct + list (no token in `Agent`), `AgentToken` returns the token, raw row's `token_enc` != token; add connect returns token, `AgentByToken(token)` finds it, wrong token not found; duplicate name → conflict; update name/address/token; rotate → old token not found, new found; delete → gone; `TestAgentTokenAfterSecretChange` (store reopened with another secret → `errTokenUnreadable`); migration runs on an existing database (open, close, reopen).
- [ ] **Step 2: Run** `go test ./internal/team -run Agent` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS; also with `PYRO_TEST_POSTGRES_URL` if available (report).
- [ ] **Step 5: Commit** `Team: agents table and store`

### Task 3: Hub with per-agent auth and id-based claims

**Files:**
- Modify: `pkg/engine/agent/hub.go`
- Test: `pkg/engine/agent/hub_test.go` (or the existing hub test file)

**Interfaces:**
- Produces (backward compatible; existing `Token`, `Claim`, `Connected` unchanged when `Authenticate` is nil):
  - field `Authenticate func(token string) (id string, ok bool)`; when set, the hub keys idle connections by id; a new connection with the same id closes and replaces the old one.
  - `func (h *Hub) ConnectedIDs() map[string]Info` (id → info, idle and busy alike, busy shown with `Busy: true`)
  - `func (h *Hub) ClaimIDs(ids []string) (workers []load.Worker, unavailable map[string]string)` — reason `"offline"` or `"busy"`; claims nothing if any id is unavailable.
  - `func (h *Hub) Release(workers []load.Worker)` — returns unused claimed workers to idle (no job sent).
  - `func (h *Hub) Disconnect(id string)` — closes that agent's connection(s).
  - `OnChange func()` — called after connect, disconnect, claim, release.
  - `connWorker.Name()` returns `NameFor(id)` when `NameFor func(id string) string` is set, else `Info.Name`.

- [ ] **Step 1: Failing tests** with in-process `httptest.Server` running `h.Handler()` and real `agent.Server.Connect` clients: per-id auth (bad token 401); same id twice → one in `ConnectedIDs`, the first client's connection closed; `ClaimIDs` with an offline id → nil workers, reason `offline`, the other agent still idle; claim then `Release` → idle again; `Disconnect` drops it; `NameFor` names the worker; existing single-token tests still pass.
- [ ] **Step 2: Run** `go test ./pkg/engine/agent -race -run Hub` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./pkg/engine/agent -race` — PASS.
- [ ] **Step 5: Commit** `Agent hub: per-agent tokens, claims by id, release`

### Task 4: AgentSource in the project server; picking agents; claim-after-checks fix

**Files:**
- Create: `internal/server/agents.go` (interface, local implementation, handler), `internal/server/agents_test.go`
- Modify: `internal/server/server.go` (`Config.AgentSource AgentSource` replaces direct use of `Agents/AgentToken/AgentInsecure/AgentHub` inside handlers; keep those fields and build a local source from them in `New` when `AgentSource` is nil), `internal/server/loadtests.go` (`startLoadTest`, `listAgents`), `internal/server/roles.go`, `cmd/pyro/main.go` (unchanged flags; still `SetAgentHub`), `web/src/api.ts` (types only)

**Interfaces:**
- Produces:
  ```go
  type AgentStatus struct {
      ID, Name, Mode, Address, Status, Detail, Version string // Mode: "direct" | "connect"
      CPUs, MaxVUs int
      Encrypted, FromFlags bool
      LastSeen int64 // unix, 0 = never
  } // JSON lowerCamel: id,name,mode,address,status,detail,version,cpus,maxVUs,encrypted,fromFlags,lastSeen
  type AgentSource interface {
      List(ctx context.Context) []AgentStatus
      Claim(ctx context.Context, ids []string) ([]load.Worker, error) // nil/empty ids = all available; *UnavailableError if any unavailable
      Release(workers []load.Worker)
      Subscribe(f func()) (cancel func())
  }
  type UnavailableError struct{ Agents map[string]string } // id -> reason; Error(): "agents not available: <name> (offline), …"
  ```
  - Local source: ids `flag:<address>` (direct flags, `FromFlags: true`) and `hub:<name>@<remote>` (hub), status from an `Info` call (direct, 5 s) / hub state.
  - `GET /api/v1/agents` → `[]AgentStatus` (role editor). Server publishes `agents`/`changed` on its event hub from `Subscribe`.
  - Start body: `agents []string`. With `distributed: true`: claim after the "already running", run-slot and plan/env checks; release on any error before `load.Start` succeeds; `*UnavailableError` → 409 with its message; no agents at all → 400 (message mentions the Agents tab on a team server and the flags otherwise).
  - `web/src/api.ts`: `AgentStatus` type, `agents: () => call<AgentStatus[]>("GET", "/agents")`, `startLoadTest` body gains `agents?: string[]`.

- [ ] **Step 1: Failing tests**: list shows a flag agent (`httptest` agent) as `available`, an unreachable one as `offline` with detail; start with `agents: [flag:A]` runs on A only (worker names in the result); `TestRejectedStartReleasesAgents` (start while a run is going → error, hub agent still claimable afterwards); `TestStartWithOfflineAgent` → 409 naming it; omitted `agents` = all available (today's behaviour).
- [ ] **Step 2: Run** `go test ./internal/server -run 'Agent|Start'` — FAIL.
- [ ] **Step 3: Implement**; `cd web && npm run typecheck` for the type changes.
- [ ] **Step 4: Run** `go test ./internal/server ./cmd/...` — PASS.
- [ ] **Step 5: Commit** `Pick agents per run; claim agents only after start checks`

### Task 5: Team registry, prober and the dial-in endpoint

**Files:**
- Create: `internal/team/agents.go` (registry implementing `server.AgentSource`), `internal/team/agents_test.go`
- Modify: `internal/team/server.go` (create the registry in `New`, mount `GET /agent/v1/connect` → `registry.hub.Handler()` before session auth/CSRF, close in `Close`; pass the registry as `AgentSource` to project configs), `cmd/pyro/team.go` (flag agents passed to the registry as read-only entries), `internal/server/roles.go` if routes change

**Interfaces:**
- Consumes: Task 2 store, Task 3 hub, Task 4 `AgentSource`, `AgentStatus`, `UnavailableError`.
- Produces: `func newAgentRegistry(store *Store, flagAgents []string, flagToken string, insecure bool, log *slog.Logger) *agentRegistry`; `(*agentRegistry).Start(ctx)` (prober, every 15 s), `Close()`, `Probe(ctx, id) AgentStatus` (immediate), `Forget(id)` (on delete: `hub.Disconnect(id)`, drop cache), plus the `AgentSource` methods. Hub `Authenticate` = `store.AgentByToken` (+ `TouchAgent`); `NameFor` = registry name. Failed connect attempts: after 5 failures from one remote IP within a minute, refuse that IP for a minute (429).
  - `team.Config` gains `FlagAgents []string`, `FlagAgentToken string`, `AgentInsecure bool` (from `serveTeam`).

- [ ] **Step 1: Failing tests** (team `httptest` server + in-process agents): add a direct agent via store → after `Probe`, `List` shows `available` with version/CPUs; connect agent: real `agent.Server.Connect(ctx, ts.URL, false)` with the store token → `List` shows it `available` within 2 s, `encrypted: false` for `http://`; wrong token → 401 and, after 5 tries, 429; `Forget` → agent disconnected and its token refused; flag agent listed with `fromFlags: true`; Review Focus #1 (delete during a run → run ends within 30 s naming the agent) and #3 (new registry on the same DB lists the direct agent; the connect agent re-dials after the server restarts); worker names in a run equal registry names.
- [ ] **Step 2: Run** `go test ./internal/team -run Registry -race` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./internal/team ./internal/server ./cmd/... -race` — PASS.
- [ ] **Step 5: Commit** `Team server: agent registry, status prober and /agent/v1/connect`

### Task 6: Admin agent API

**Files:**
- Modify: `internal/team/admin.go` (handlers), `internal/team/server.go` (routes under the existing admin group), roles/route-role table if the team server has one, `docs/api.md`
- Test: `internal/team/admin_agents_test.go`

**Interfaces:**
- Consumes: Task 2 store, Task 5 registry.
- Produces routes (admin, CSRF):
  - `GET /api/v1/admin/agents` → `[]AgentStatus` plus `createdBy`, `created`
  - `POST /api/v1/admin/agents` `{name, mode, address?, token?, insecureTLS?}` → 201 `{agent, token?, command?}` (`token`/`command` only for `connect`)
  - `PATCH /api/v1/admin/agents/{id}` `{name?, address?, token?, insecureTLS?}` → agent
  - `DELETE /api/v1/admin/agents/{id}` → 204 (store delete + `registry.Forget`)
  - `POST /api/v1/admin/agents/{id}/test` → `AgentStatus` (registry `Probe`)
  - `POST /api/v1/admin/agents/{id}/token` → `{token, command}` (connect only; rotates, `registry.Forget` then keeps the row)
  - `command` = `docker run -d --name pyro-agent --restart unless-stopped ghcr.io/codeen15/pyro:<tag> agent --connect <base> --token <token>` where `<tag>` is the server version without the leading `v` (or `latest` for `dev`/describe-style versions) and `<base>` is `PublicURL` or the request's scheme+host; also return `base` so the UI can edit it.
  - Each add/update/delete/rotate records server activity (`Store.Record(ctx, user, "", "", "agent.add"|…, name)`).
  - Errors: 400 validation, 404 unknown, 409 duplicate name, 400 rotate on a direct agent.

- [ ] **Step 1: Failing tests**: non-admin → 403; missing CSRF → 403; full lifecycle for both modes; command contains `--connect` with `PublicURL` (and the request host when PublicURL is empty) and the token; activity rows written; `TestEveryRouteHasARole` (or team equivalent) still passes.
- [ ] **Step 2: Run** `go test ./internal/team -run AdminAgents` — FAIL.
- [ ] **Step 3: Implement**; update `docs/api.md` (admin endpoints, `agents` on start, `agents` event, `/agent/v1/connect` on the team server).
- [ ] **Step 4: Run** `go test ./internal/team ./internal/server` — PASS.
- [ ] **Step 5: Commit** `Team admin API for agents`

### Task 7: Admin → Agents tab

**Files:**
- Create: `web/src/components/AdminAgents.tsx`
- Modify: `web/src/components/Admin.tsx` (tab `agents`, label `Agents (n)`), `web/src/api.ts` (admin agent calls), `web/src/App.tsx` or the agents hook (subscribe `agents` → invalidate `["agents"]` and `["admin-agents"]`), `web/src/styles.css` (status dot tokens, light + dark)

**Interfaces:**
- Consumes: Task 6 routes; Task 4 `AgentStatus`.
- Produces: `AdminAgents` component; react-query keys `["admin-agents"]`.

- [ ] **Step 1: Write the e2e spec first** `web/e2e/agents.spec.ts` (team-mode fixture as `team.spec.ts` uses): admin opens Admin → Agents; **Add agent → Connects in…**, name `e2e-agent`, sees the command; the spec starts `bin/pyro agent --connect <server url> --token <token shown>` as a child process; the row turns `available` within 10 s; **Remove** (confirm) → row gone and the child process logs a disconnect/refusal.
- [ ] **Step 2: Run** `cd web && npm run e2e -- agents` — FAIL (no tab).
- [ ] **Step 3: Implement** per spec §UI: table columns Name, How (Direct/Connects in tag), Address (with "not encrypted" note for http), Status (dot + text; offline shows "last seen …"), Version, CPUs, Max VUs, actions (Test/Edit/Remove for direct; Rotate/Remove for connect; none for `fromFlags`); Add menu with two modals (direct: name, address, token (password input), skip certificate check, Test then Save — Save allowed after a failed test with a warning; connect: name → command shown once with copy buttons, editable base URL, "waiting for the agent…" turning into connected live). `npm run typecheck`.
- [ ] **Step 4: Run** the e2e spec — PASS; `npm run typecheck` clean.
- [ ] **Step 5: Commit** `Admin: manage load agents`

### Task 8: Agent picker on the load test page

**Files:**
- Modify: `web/src/components/LoadTest.tsx`, `web/src/styles.css` if needed
- Test: extend `web/e2e/agents.spec.ts`

**Interfaces:**
- Consumes: `api.agents`, `startLoadTest({..., agents})`.

- [ ] **Step 1: Extend the e2e spec**: with the connected `e2e-agent` from Task 7's flow (before removing it), open a load test, choose **Run on: Agents**, see `e2e-agent` ticked and `available`; start → the run's Agents table lists `e2e-agent`.
- [ ] **Step 2: Run** — FAIL (no checklist).
- [ ] **Step 3: Implement**: radio "This server" / "Agents (n available)"; under it a checklist (antd `Checkbox` list) of all agents; available ones ticked by default; busy/offline/error disabled with the reason; "x of y selected"; selection remembered per load test in `localStorage` (try/catch), dropping ids that no longer exist; Start sends `distributed: true, agents: [...]`; a 409 shows the server's message via `App.useApp().message`.
- [ ] **Step 4: Run** `cd web && npm run e2e` — PASS (whole suite).
- [ ] **Step 5: Commit** `Pick agents per run in the load test page`

### Task 9: Docs and security

**Files:**
- Modify: `docs/guide/team-server.md` ("Load agents on other machines": UI-first — add direct / connecting agents, internal network by IP, Dokploy domain; flags as an alternative), `docs/guide/distributed.md` (team server: Admin → Agents; local: flags), `docs/guide/load-testing.md` (agent picker), `SECURITY.md` (agent boundary: admins add agents; what a token allows; direct tokens encrypted with a key from the JWT secret and recoverable by the server; connect tokens hashed; plain HTTP note; rate limit), `CHANGELOG.md`, `docs/roadmap.md` (new phase row: agent management ✅), `CLAUDE.md` (Layout line for `internal/team` agents; `pkg/engine/agent` hub note)

- [ ] **Step 1: Write the docs.**
- [ ] **Step 2: Check** that every endpoint, flag and UI label named in the docs exists (grep the code for each) and the guide's commands match the generated `command`.
- [ ] **Step 3: Run** `make test` and `cd web && npm run e2e` — PASS.
- [ ] **Step 4: Commit** `Docs: managing agents from the UI`
