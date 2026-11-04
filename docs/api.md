# Engine API

The engine exposes a local HTTP API used by the web UI. The source of truth is
`internal/server/server.go`. From Phase 1 an OpenAPI spec will generate the
TypeScript client in `web/src/api.ts`.

## Auth
- `pyro serve` binds to `127.0.0.1:7878` by default and prints `http://127.0.0.1:7878/?token=<hex>`.
- Every `/api/v1/*` request must send `X-Pyro-Token: <token>`. A missing or wrong token gets `401`.
- The UI reads `?token=` once, stores it in `sessionStorage` for the tab, and removes it from the address bar.
- `--no-token` turns the check off, and is refused unless the bind address is loopback. It exists for the Vite dev server.
- Static UI assets (`/`, `/assets/*`) are served without a token.
- `GET /api/v1/auth/config` returns `{"mode":"local"}`, so the UI can tell a local server from a team server.

## Team server (`pyro serve --team`)
The team server puts accounts and workspaces in front of the same project API. Source:
`internal/team/server.go`.

- **Sign-in uses JWTs.** An **access token** is a JWT (HS256, issuer `pyro`, 15 minutes; claims `sub`, `name`, `admin`, `iat`, `iat_ms`, `exp`, `jti`). A **refresh token** is an opaque random value. Only its SHA-256 hash is stored (the `sessions` table), and it lasts 14 days from its last use.
  - **Browsers** get both as HttpOnly cookies: `pyro_access` (the JWT) and `pyro_session` (the refresh token). Both are SameSite=Lax, and Secure behind https. When the access token has expired, the next request mints a new one from the refresh cookie, so the UI never handles tokens.
  - **API clients and the CLI** send `Authorization: Bearer <token>`, with either a personal access token (`pyro_pat_…`, made on the Account page, lasting until it expires or is revoked) or an access token from `POST /api/v1/auth/token`, renewed with `POST /api/v1/auth/refresh`.
  - **LDAP** accounts sign in through the same `/auth/login` and `/auth/token` with a username or email. **OpenID Connect** accounts sign in through `/auth/oidc/{id}/start` in the browser.
  - **Revocation:** changing a password or signing out everywhere deletes refresh tokens and revokes access tokens issued earlier (`users.tokens_after`, checked on every request). Signing out deletes that refresh token. Its access token stops being renewed and expires within 15 minutes.
  - **Signing key:** `$PYRO_JWT_SECRET` (at least 32 characters; set it when several servers share a database), otherwise `<data>/jwt.secret`, generated on first start with mode 0600.
- **CSRF:** every cookie-authenticated request other than GET/HEAD/OPTIONS must send `X-Pyro-CSRF: 1`. Requests with a Bearer token, and `/auth/token` and `/auth/refresh`, don't need it. When an
  `Origin` header is present it must match the host. Otherwise the server answers `403`.
- **Project API:** every endpoint below is available under `/api/v1/projects/{projectId}/…`,
  e.g. `GET /api/v1/projects/3f…/requests/shop/list-items`, and the events socket is at
  `/api/v1/projects/{projectId}/events`. Calls act as the signed-in user with their workspace
  role (see the role table in `internal/server/roles.go`):
  - `403` when the role is too low;
  - `404` when the user isn't a member (the project's existence isn't confirmed).
- The OAuth 2.0 redirect URI for a project is `<publicURL>/oauth2/p/{projectId}/callback`.
- Errors use the same `{"error": …}` shape.

| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/health` | `{status, version, mode: "team"}` |
| GET | `/api/v1/auth/config` | `{mode: "team", signedIn, firstUser, signup, signupPolicy, signupDomains}`. No sign-in needed |
| POST | `/api/v1/auth/signup` | Body `{email, name?, password}` (password ≥ 10 characters). Creates the account with a personal workspace and a first project, and signs in. `403` when the sign-up policy refuses, `409` for a taken email |
| POST | `/api/v1/auth/login` | Body `{email, password}`. `400` for wrong credentials; `429` after 10 failures per account (or 50 per IP) in 15 minutes |
| POST | `/api/v1/auth/logout` | Ends this session: the refresh cookie, or `{refreshToken}` in the body. `?everywhere=1` ends all of the user's sessions and revokes their access tokens |
| POST | `/api/v1/auth/token` | API clients: body `{email, password}` → `{accessToken, tokenType: "Bearer", expiresIn, refreshToken}`. Same rate limits as sign-in; no cookies |
| GET | `/auth/oidc/{id}/start` | Browser: start single sign-on with a configured provider (`?invite=<token>` joins a workspace after) |
| GET | `/auth/oidc/{id}/callback` | Browser: the provider's redirect. On failure it redirects to `/?signin_error=…` |
| POST | `/api/v1/auth/forgot` | Body `{email}`: email a one-hour reset link to a password account. Always `204`; `404` if the server can't send email |
| GET | `/api/v1/auth/reset/{token}` | `{email}` for a valid reset link; `410` once used or expired |
| POST | `/api/v1/auth/reset` | Body `{token, password}`: set the password, sign out everywhere else, and sign in here |
| GET / POST | `/api/v1/me/tokens` | Personal access tokens: list (`[{id, name, created, lastUsed?, expires?}]`), or create with `{name, days}` (`days: 0` never expires) → `{token, info}`. The `pyro_pat_…` token is shown once |
| DELETE | `/api/v1/me/tokens/{id}` | Revoke a token |
| GET | `/api/v1/admin/users` | Administrators: every account |
| PATCH | `/api/v1/admin/users/{id}` | Administrators: `{admin?, disabled?}`. Disabling signs the account out everywhere. The last active administrator can't be removed (`400`) |
| POST | `/api/v1/admin/users/{id}/reset-link` | Administrators: a 24-hour one-time password link `{url, expires, emailed}` (password accounts only) |
| GET | `/api/v1/admin/settings` | Administrators: how the server is set up (no secrets) |
| POST | `/api/v1/auth/refresh` | Body `{refreshToken}` → a new `{accessToken, tokenType, expiresIn}`; `401` once it's expired or signed out |
| GET | `/api/v1/me` | `{mode, user: {id, email, name, provider, admin}, workspaces}`. `401` when signed out |
| PATCH | `/api/v1/me` | Body `{name}` |
| POST | `/api/v1/me/password` | Body `{current, password}`. Signs out the user's other sessions |
| GET | `/api/v1/workspaces` | The user's workspaces (personal first) with `role` and `projects` |
| POST | `/api/v1/workspaces` | Body `{name}`: a team workspace owned by the caller |
| PATCH / DELETE | `/api/v1/workspaces/{id}` | Rename (`{name}`) or delete an empty team workspace (owner) |
| POST | `/api/v1/workspaces/{id}/projects` | Body `{name}`: new project (editor) |
| GET | `/api/v1/projects/{id}` | `{id, name, workspace, workspaceName, workspaceKind, role}` |
| PATCH / DELETE | `/api/v1/projects/{id}` | Rename (`{name}`) or delete a project and its files (owner) |
| POST | `/api/v1/projects/{id}/move` | Body `{workspace}`: move the project (owner of both workspaces) |
| POST | `/api/v1/projects/{id}/transfer` | Body `{collection, project, mode: copy\|move, environments?}`: copy or move a collection to `project` (editor there; a move also needs editor here). Returns `{slug, project}`; a clash gets `-2`, `-3`… |
| GET | `/api/v1/projects/{id}/archive` | The project folder as a zip |
| POST | `/api/v1/workspaces/{id}/projects/import?name=` | Body: a zipped workspace folder (≤ 200 MB). Creates a project (editor) |
| GET | `/api/v1/workspaces/{id}/members` | `[{userId, email, name, role, added}]` |
| PATCH / DELETE | `/api/v1/workspaces/{id}/members/{userId}` | Change a role (`{role}`, owner) or remove a member (owner, or yourself to leave). The last owner can't be demoted or removed (`400`) |
| GET / POST | `/api/v1/workspaces/{id}/invites` | Open invites, or create one (owner): body `{role, email?, days?, maxUses?}` → `{invite, url, emailed, emailError?}`. An email invite is single-use and only for that address. Personal workspaces can't invite (`400`) |
| DELETE | `/api/v1/workspaces/{id}/invites/{inviteId}` | Revoke |
| GET | `/api/v1/workspaces/{id}/activity` | `[{at, user, project?, action, target?}]`, newest first |
| GET | `/api/v1/invites/{token}` | No sign-in needed: `{workspace, workspaceId, role, email?, invitedBy, expires}`; `410` once expired, used up or revoked |
| POST | `/api/v1/invites/{token}/accept` | Join the workspace (keeps a higher existing role) |

`POST /api/v1/auth/signup` also takes `invite`, a token. It allows sign-up whatever the
policy (for an email invite, only with that address) and joins the workspace.

### Reports (local and team)
Collection runs are saved as reports when they finish (secret variable values and credential
headers masked; bodies capped at 64 KB). Load test runs are listed with them.

| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/reports?kind=run\|load&target=&limit=` | Newest first: `[{id, kind, title, target, environment, status, startedAt, duration, runBy, requests, failed, tests, testsFailed, p95, errorRate, maxVUs}]` |
| GET | `/api/v1/reports/{id}` | A collection run's report: `{id, title, target, project, environment, runBy, dataFile, version, startedAt, duration, status, totals, summary}` |
| GET | `/api/v1/reports/{id}/compare?with=` | What changed since `with` (default: the previous run of the same target): `{against, againstStartedAt, newlyFailing, newlyPassing, added, removed}`. `204` when there's no earlier run |
| GET | `/api/v1/reports/{id}/export?format=html\|xlsx\|csv\|junit\|json` | Download a collection run (any format) or a load test run (`html`, `xlsx`, `csv`, `json`). HTML is inline unless `&download=1` |
| POST | `/api/v1/reports` | Save a run made elsewhere (`pyro run/load --report-to`): `{kind: "run", report}` or `{kind: "load", test?, environment?, result}`. Run reports are masked again on arrival. `201 {id, kind}` |
| DELETE | `/api/v1/reports/{id}` | Delete a report or a load run (editor) |

`POST /api/v1/runs` takes `dataFile` (the data file's name, for the report), and `GET /api/v1/runs/{id}` returns `reportId` once the run has ended. Load run views include `runBy` and `environment`.

### Versions and change events (local and team)
- `GET` on a request, collection, folder, environment, load test or globals returns an `ETag`.
- `PUT` with `If-Match: <etag>` answers `409 {"error": "<name> changed this since you opened it", "conflict": true, "by"}` when the stored version has moved on. A successful `PUT` returns the new `ETag`. Without `If-Match` the save always goes through.
- Successful writes are published on the event topic `tree` as `{"type":"changed","data":{"by","name","action","target"}}`. The UI refreshes its lists, and on a team server they go to the workspace activity log.
- `GET /api/v1/collections/{slug}/usage` returns `{loadTests: [...]}`: the load tests whose steps use the collection.

## Conventions
- JSON in and out. Unknown request fields are rejected (`400`). Request bodies are limited to 10 MB.
- Errors: `{"error": "<message>"}` with `400` (invalid input or path), `404` (not found), `409` (already exists), `500` (unexpected; details logged, not returned).
- Request paths in URLs are the workspace request path (see [data-model.md](data-model.md)). Each segment is URL-encoded, e.g. `/api/v1/requests/demo/get%20user`.

## Endpoints

### Implemented (Phases 0–4)
| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/health` | `{status, version}` |
| GET | `/api/v1/workspace` | `{name, root, collections: Node[]}`, where Node = `{type, name, path, kind?, method?, children?}` |
| POST | `/api/v1/collections` | Body `{slug, name?, description?, variables?, auth?, scripts?}` → `201` |
| GET | `/api/v1/collections/{slug}` | Collection settings |
| DELETE | `/api/v1/collections/{slug}` | Delete the collection and everything in it (stops its mock server) → `204` |
| POST | `/api/v1/folders/{path}` | Create an empty folder inside a collection or folder; body is its settings (`{name?, …}`) → `201`, `409` if it exists |
| DELETE | `/api/v1/folders/{path}` | Delete the folder and everything in it → `204` |
| GET | `/api/v1/requests/{path…}` | A request |
| PUT | `/api/v1/requests/{path…}` | Create or replace; folders are created as needed; `kind` defaults to `http` |
| DELETE | `/api/v1/requests/{path…}` | `204` |
| GET | `/api/v1/environments` | Environment names |
| GET | `/api/v1/environments/{name}` | An environment |
| PUT | `/api/v1/environments/{name}` | Create or replace |
| DELETE | `/api/v1/environments/{name}` | `204` |
| PUT | `/api/v1/collections/{slug}` | Replace collection settings (variables, auth, scripts) |
| POST | `/api/v1/send` | Body `{path?, request?, environment?, runtime?}`. `path` loads the saved request and supplies collection/folder variables and auth; `request` sends unsaved edits instead. Returns `{sent, response?, unresolved?, error?, startedAt, historyId?}`. A transport failure is still `200` with `error` set. Response bodies are base64 (`response.body`). WebSocket and Socket.IO requests with `action` `send`, `ping` or `close` run as steps on the caller's connection of that name (see `GET /connections`), and the result adds `socket: {action, connection?, own?, info?, sent[], received[], matched?, waitMs?, rttMs?}`; the step's messages also appear in that connection's session log. Connect requests are rejected here with `400`: open them with `POST /sessions`. Socket steps aren't recorded in history. |
| GET | `/api/v1/history?limit=N` | Newest first: `{id, at, path?, name?, method, url, status?, durationMs, error?}` |
| GET | `/api/v1/history/{id}` | Full stored send result (bodies capped at 256 KB) |
| DELETE | `/api/v1/history` | Clear this workspace's history |
| POST | `/api/v1/import?filename=&into=` | The file as the body (≤ 50 MB): Postman collection or environment, OpenAPI/Swagger, HAR or cURL text; the format is detected. `filename` names cURL/HAR collections; `into` adds the requests to an existing collection or folder. `201` with the import report (`into: true` when added); `422` if the format isn't recognised. |
| PUT | `/api/v1/collections/{slug}` | Save collection settings (`collection.yaml`) |
| GET, PUT | `/api/v1/folders/{path}` | Folder settings (`folder.yaml`; a folder without one reads as `{name}`) |
| GET | `/api/v1/collections/{slug}/docs` | Generated HTML API reference (`?download=1`) |
| GET | `/api/v1/mocks` | Running mock servers `[{collection, running, url}]` |
| POST | `/api/v1/mocks` | Body `{collection, port?, delayMs?}`: start a mock on 127.0.0.1 (`409` if one runs) |
| GET | `/api/v1/mocks/{slug}` | `{running, url, routes, hits}` (`running: false` when none) |
| DELETE | `/api/v1/mocks/{slug}` | Stop it |
| GET | `/api/v1/collections/{slug}/export` | `{filename, content, report}`: the collection as Postman v2.1 JSON (`content`) and what didn't convert |
| GET | `/api/v1/environments/{name}/export` | `{filename, content}`: the environment as Postman environment JSON |
| POST | `/api/v1/oauth2/token` | Body `{path?, request?, environment?, fetch?, clear?}`: the OAuth 2.0 token for the request's effective auth: `{oauth, grant, token?, expired?, interactive, inherited, redirectUri, unresolved?}` (`oauth: false` when its auth isn't OAuth 2.0). `fetch` gets a new one (non-interactive grants), `clear` forgets it |
| POST | `/api/v1/oauth2/authorize` | Same body; starts an authorization-code sign-in and returns `{url, redirectUri}` to open in a browser |
| GET | `/oauth2/callback` | The provider's redirect target (no session token; the random `state` ties it to a sign-in Pyro started) |
| GET | `/api/v1/cookies` | The interactive cookie jar (saved per workspace in `~/.pyro/pyro.db`, so it survives restarts): `[{name, value, domain, path, hostOnly?, secure?, httpOnly?, sameSite?, expires?, created}]` |
| PUT | `/api/v1/cookies` | Add or replace a cookie (`domain` and `name` required, `path` defaults to `/`) |
| DELETE | `/api/v1/cookies?domain=&path=&name=` | One cookie, a domain's cookies (domain only) or all (no parameters) |
| GET, PUT | `/api/v1/globals` | `globals.yaml` as an environment `{name: "globals", variables}` |
| POST | `/api/v1/sessions` | Body `{path?, request?, environment?}`. Opens a WebSocket or Socket.IO session under the request's `connection` name (default `default`; opening a name again replaces the old connection). Returns `{sent, unresolved?, error?, session?}`; on failure `error` is set and there is no `session`. Sessions stay open until closed, whatever page the UI shows. |
| GET | `/api/v1/sessions/{id}` | `{id, connection, kind, path?, info, openedAt, closed, error?, messages[]}` (last 2000 messages) |
| GET | `/api/v1/connections` | The caller's open named connections: `[{name, kind?, sessionId?, path?, url?}]` (each user has their own) |
| POST | `/api/v1/sessions/{id}/send` | Body `{data, binary?, event?, ack?}` (variables resolved). Returns `{message, unresolved?}` |
| DELETE | `/api/v1/sessions/{id}` | Close |
| POST | `/api/v1/runs` | Body `{path, environment?, iterations?, data?, delayMs?, bail?}`. Starts a background collection run; `202` with the run view |
| GET | `/api/v1/runs/{id}` | `{id, path, status, done, total, results[], summary?}` |
| DELETE | `/api/v1/runs/{id}` | Cancel |
| GET | `/api/v1/data` | Data files under `data/`: `[{path, size}]` |
| PUT | `/api/v1/data/{name}` | Upload a data file (raw body, `.csv`/`.tsv`/`.txt`/`.json`, up to 64 MB) to `data/{name}` → `201 {path, size}` |
| POST | `/api/v1/data/preview` | Body `{file, limit?, header?, variables?, map?, delimiter?}` → `{columns, variables, rows, total}` (first `limit` rows, default 5); `422` when the file can't be read that way |
| GET | `/api/v1/loadtests` | `[{name, title?, peakVUs?, error?}]` (`error` = the plan doesn't validate) |
| GET / PUT / DELETE | `/api/v1/loadtests/{name}` | GET returns `{name, title, yaml, plan, peakVUs, error?}`. PUT `{yaml}` validates (400 with every problem) and saves the raw text, so comments survive. |
| POST | `/api/v1/loadtests/{name}/start` | Body `{environment?, confirmHosts?, distributed?}` (`distributed` runs on the agents given to `pyro serve --agent`; `400` if none). `409 {needsConfirmation, hosts}` when it would target hosts that aren't loopback or in `loadAllowHosts`; `409 {running}` when another test runs; `202 {id}` when started. |
| GET | `/api/v1/agents` | Agents: those given to `--agent` (`mode: dial`) and those connected to `--agent-listen` (`mode: connected`): `[{mode, address, info?: {name, version, protocol, cpus, os, arch, busy, maxVUs, plugins}, error?}]` |
| GET | `/api/v1/loadruns?test=` | Saved runs newest first (a running one first): `{id, test, name, startedAt, status, duration, requests, p95, errorRate, maxVUs}` |
| GET | `/api/v1/loadruns/{id}` | Running: `{status: "running", ticks, live: {tick, requests, errors}, plan}`. Finished: `{status, result}` |
| GET | `/api/v1/loadruns/{id}/report` | Self-contained HTML report (`?download=1` sets Content-Disposition) |
| POST | `/api/v1/loadruns/{id}/stop` | Graceful stop; `?abort=1` cancels in-flight requests |
| DELETE | `/api/v1/loadruns/{id}` | Delete a saved run |
| GET | `/api/v1/plugins` | `{plugins: [Info], available, indexConfigured, dir}`; Info = manifest + `{enabled, approved, ready, settings?, error?}` |
| POST | `/api/v1/plugins` | Body: a plugin archive (zip). Installs it unapproved (`201` Info; `422` if invalid) |
| POST | `/api/v1/plugins/{name}/approve` | Approve the current capabilities |
| POST | `/api/v1/plugins/{name}/enabled` | Body `{enabled}` |
| PUT | `/api/v1/plugins/{name}/config` | Body `{config: {key: value}}` (saved as `settings`) |
| DELETE | `/api/v1/plugins/{name}` | Uninstall |
| GET | `/api/v1/plugins/index?q=` | Search the configured index (`404` when none) |
| POST | `/api/v1/plugins/index/install` | Body `{name, version?}`: download (sha256-verified) and install |
| GET / DELETE | `/api/v1/session/variables` | Values set by scripts and extractors during this `pyro serve` (runtime, globals, per environment, per collection). Never written to files. DELETE resets them. |
| PATCH | `/api/v1/session/variables` | Body `{scope: globals\|environment\|collection\|runtime, name?, key, value?, reset?}`: set one current value (`value: null` unsets it for the session) or `reset` it to the file's initial value |

### Planned
| Method | Path | Phase | Description |
|---|---|---|---|

## Event WebSocket (implemented)
`GET /api/v1/events?token=<token>` (browsers can't set headers on WebSocket, so the token
goes in the query string; it is checked the same way).

Client → server:
```json
{"op":"subscribe","topic":"load:01HXYZ"}
{"op":"unsubscribe","topic":"load:01HXYZ"}
```
Server → client:
```json
{"topic":"load:01HXYZ","type":"metrics","data":{"t":1730450000,"vus":200,"rps":1834,"p95":212}}
```

| Topic | Events |
|---|---|
| `session:<id>` | `message` (`{seq, at, direction, kind, data, event?, ackId?, rttMs?, size}`), `closed` (`{error}`) |
| `run:<id>` | `result` (`{done, total, item}`, bodies trimmed to 64 KB), `finished` (the run view) |
| `load:<id>` | `tick` (`{tick, requests, errors}` once a second), `finished` (`{id, test, status, result}`) |
| `workspace` (planned) | `changed` (files edited on disk, through a file watcher) |

Events published before a client subscribes are not replayed. Clients subscribe first,
then fetch `GET /sessions/{id}` or `GET /runs/{id}` once and merge (by `seq` for
session messages, by position for run results). Slow subscribers drop events rather
than slowing the engine.
