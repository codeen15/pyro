# Data Model & Workspace Format

The Go types live in `pkg/engine/model/model.go`. The on-disk layout is handled by `internal/store`.

## Workspace layout
```
my-workspace/
  pyro.yaml                                   # workspace metadata
  collections/
    payments/                                 # collection slug
      collection.yaml                         # collection settings
      auth/
        folder.yaml                           # optional folder-level settings
        login.req.yaml
      orders/create-order.req.yaml
      realtime/notifications.req.yaml         # WebSocket / Socket.IO use .req.yaml too
  globals.yaml                                # workspace-wide variables (optional)
  environments/
    dev.env.yaml
    prod.env.yaml
  loadtests/checkout-stress.load.yaml         # Phase 3
  data/users.csv                              # data files for runs and load tests
```

**Request paths:** a request is addressed by its slash-separated path relative to
`collections/`, **without** the `.req.yaml` extension, e.g. `payments/orders/create-order`.
The first segment is always the collection slug. The API, CLI and load plans all use this form.

**All request kinds share one extension:** `.req.yaml`. The `kind` field selects the protocol driver.

Files and directories starting with `.` are ignored. Writes are atomic (temp file + rename).
YAML is written with 2-space indentation.

## `pyro.yaml`
```yaml
name: My Workspace
version: 1
loadAllowHosts: [staging.example.com]   # load tests may target these without confirmation
loadMaxRPS: 500                         # optional ceiling on every load test's requests per second
allowFilesOutsideWorkspace: false       # default
```
Request bodies (binary file bodies and multipart file fields) may only read files inside
the workspace directory, after following symlinks. A collection you import or clone can't
send other files on your machine. Set `allowFilesOutsideWorkspace: true` to lift this.

## `collection.yaml` / `folder.yaml`
```yaml
name: Payments API
description: Public payments endpoints
variables:
  - key: baseUrl
    value: https://api.example.com
auth:
  type: bearer
  params: { token: "{{token}}" }
scripts:
  pre: "..."     # pm.*-compatible JS, runs before every request in scope
  post: "..."
think: { type: uniform, delay: 1s, range: 2s }   # default timer for requests beneath
pre:  [ { type: vars, vars: [ { key: tenant, value: acme } ] } ]   # processors around every request
post: [ { type: assert, assert: { type: status, op: lt, value: "500" } } ]
order: [auth, orders, checkout]   # optional run order of children (file/folder names)
```

Children listed in `order` come first, in that order (the tree and collection runs both
use it). The Postman importer fills it from the source order. Anything not listed
follows, folders first, sorted by name.

## `*.req.yaml`
`kind`: `http` | `websocket` | `socketio` | `plugin:<id>` (a request type from an installed plugin, see [plugins.md](plugins.md)).

### Plugin request types
```yaml
kind: plugin:tcp
name: TCP ping
url: tcp://{{host}}
body: { type: text, content: "ping\n" }      # sent as text
options:                                     # the plugin's declared fields
  readUntil: delimiter
  timeoutMs: "2000"
assertions: [ { type: body, op: matches, value: "^PONG" } ]
```

### HTTP
```yaml
kind: http
name: Create order
method: POST
url: "{{baseUrl}}/orders"
query:
  - { key: dryRun, value: "true", disabled: true }
headers:
  - { key: Content-Type, value: application/json }
body:
  type: json            # none | json | text | xml | form | multipart | graphql | file
  content: '{"sku":"{{sku}}","qty":1}'
auth:                   # omit to inherit from folder/collection
  type: bearer
  params: { token: "{{token}}" }
scripts: { pre: "...", post: "..." }
assertions:
  - { type: status, op: eq, value: "201" }
  - { type: jsonpath, path: "$.id", op: exists }
extract:
  - { var: orderId, from: jsonpath, path: "$.id" }
```

Body variants: `form`/`multipart` use `fields` (a key/value list; in multipart a field with
`file: true` sends the file at `value`, relative to the workspace root or absolute), `file` uses `file`,
and `graphql` uses `content` (the query) plus `variables` (a JSON string).

Assertion `type`: `status`, `header`, `jsonpath`, `body`, `duration`.
`op`: `eq`, `neq`, `lt`, `gt`, `contains`, `exists`, `matches`.
Extractor `from`: `jsonpath`, `header`, `regex`, `status`, `boundary` (the text between
`left` and `right`), `xpath` (XPath 1.0 on an XML body; the first match's text).
`{{variables}}` are filled in to an assertion's `path` and `value` and an extractor's `path`,
`left` and `right` before they run.

## Think time and pre/post processors
JMeter-style timers and processors, on any request and on `collection.yaml` / `folder.yaml`:

```yaml
think: { type: gaussian, delay: 800ms, range: 200ms }
pre:
  - { type: counter, counter: { var: orderNo, start: 1, step: 1, end: 9999, format: "%05d" } }
  - { type: vars, vars: [ { key: idem, value: "{{$guid}}" }, { key: ref, value: "ord-{{orderNo}}" } ] }
  - { type: script, script: "pm.variables.set('ts', Date.now())" }
post:
  - { type: extract, extract: { var: orderId, from: jsonpath, path: "$.id" } }
  - { type: extract, extract: { var: csrf, from: boundary, left: 'name="csrf" value="', right: '"' } }
  - { type: assert, assert: { type: status, op: eq, value: "201" } }
  - { type: plugin:hmac-var, options: { secret: "{{secret}}", header: X-Signature } }
  - { type: script, name: Log slow orders, disabled: true, script: "..." }
```

**Timers** (`think`) run before the request in load tests, and in collection runs started with
`--think` (or "Apply think time" in the UI). Manual sends never wait, and a timer's wait is never
counted as response time. `delay` is the constant part and `range` the random part:

| `type` | Wait |
|---|---|
| `constant` | `delay` |
| `uniform` | `delay` + a random 0..`range` |
| `gaussian` | `delay` + a normal deviation of `range` (never below 0) |
| `poisson` | `delay` + a Poisson-distributed wait with mean `range` |
| `none` | no wait, even if a folder or the collection sets a timer |

Durations are Go-style (`300ms`, `1.5s`); a bare number is milliseconds. The request's own
`think` wins, then the nearest folder's, then the collection's. A load test's `think` steps
still add pauses between steps on top of request timers.

**Processors** run in every mode (manual sends, collection runs and load tests):

| `type` | Phase | Does |
|---|---|---|
| `script` | pre, post | pm.*-compatible JavaScript, like `scripts.pre` / `scripts.post` |
| `vars` | pre, post | Sets each enabled key to its value in the run's variables; `{{vars}}` and `{{$functions}}` are resolved, in order (like JMeter's User Parameters) |
| `counter` | pre | `var` = `start` on first use, then `+ step` (default 1) each time; wraps to `start` after passing `end` (0 = never); optional printf `format`. Counts per virtual user. |
| `extract` | post | One extractor (fields as above) |
| `assert` | post | One assertion (fields as above) |
| `plugin:<id>` | per plugin | A processor from an installed plugin; `options` are its fields, variables resolved ([plugins.md](plugins.md)) |

Order: before the request, each level from the collection through folders to the request runs
its legacy `scripts.pre` and then its `pre` list, top to bottom. After the response, each
level runs `scripts.post` and then its `post` list, and finally the request's legacy
`assertions` and `extract`. A pre-processor that fails (a script error, an invalid processor
or an unavailable plugin) stops the request, as in Postman. `disabled: true` keeps a
processor without running it.

`scripts`, `assertions` and `extract` still load and run. The editor shows them in the
Pre/Post-processors lists and saves them in the `pre`/`post` form the first time you edit a list.

### WebSocket
```yaml
kind: websocket
name: Notifications
url: wss://api.example.com/ws
headers: [ { key: Authorization, value: "Bearer {{token}}" } ]
messages:                       # saved message templates
  - { name: Subscribe, content: '{"op":"sub","topic":"orders"}' }
compose: { content: '{"op":"ping"}' }   # the console composer's message, saved with the request (never sent by itself)
```

### Socket.IO
```yaml
kind: socketio
name: Chat
url: https://chat.example.com
socketio:
  path: /socket.io/             # default
  namespace: /
  auth: '{"token":"{{token}}"}' # CONNECT packet payload (JSON)
  listen: [ "chat message" ]    # events shown in the console
  transport: auto               # auto (WebSocket, falling back to long-polling) | websocket | polling
messages:
  - { event: "chat message", content: '["hi"]', ack: true }   # content = JSON args array
  - { event: "upload", content: '[{"$binary": "AAEC/w=="}, "name.bin"]' }   # binary attachment
```
### Socket steps (`action`)
A WebSocket or Socket.IO request does one thing, set by `action` (absent means `connect`):

```yaml
# collections/chat/open.req.yaml: opens the connection "chat"
kind: websocket
name: Open chat
connection: chat                # the name later steps use (default: "default")
url: wss://chat.example.com/ws
---
# collections/chat/join.req.yaml: sends, then waits for the welcome
kind: websocket
name: Join
action: send                    # connect | send | ping | close
connection: chat                # empty: its own connection, from this request's url/headers/auth
messages:                       # sent in order (for connect requests: saved templates)
  - { content: '{"type":"join","token":"{{token}}"}' }
wait:
  for: message                  # message | ack (Socket.IO) | time; absent: don't wait
  event: welcome                # Socket.IO event name
  contains: '"ok":true'
  regex: '^\{'
  jsonpath: $.type              # must exist…
  equals: welcome               # …and equal this
  timeout: 5s                   # default 10s; for "time", how long to collect
assertions:
  - { type: jsonpath, path: $.token, value: "{{token}}" }
---
# collections/chat/leave.req.yaml
kind: websocket
name: Leave
action: close
connection: chat
closeCode: 1000                 # WebSocket: 1000 or 3000–4999
closeReason: bye                # at most 123 bytes
```

All of a `message` wait's conditions must hold; with none, the next received message matches.
`ack` waits for the ack of the last message sent with `ack: true`. A step's response body is
the matched message's data (for `time`, a JSON array of every received message), so
post-processors work on it as on an HTTP body. A step only sees messages it hasn't seen
before: replies that arrived between steps are matched by the next wait. `ping` is WebSocket
only. `connection`, message content and events and the wait's text fields take
`{{variables}}`.

A message's `content` is the JSON array of its arguments, any number of them, e.g.
`'["lobby", {"user": "{{user}}"}, {{count}}, true]'`. The editor shows it as a list of typed
arguments (Text, JSON, Number, Boolean, Binary), as Postman does, with a switch to edit the
raw array. A `{{variable}}` may stand in for a whole number, boolean or JSON argument: it is
filled in before the array is parsed. WebSocket messages carry one payload, edited as Text,
JSON (with formatting) or Binary (base64, `binary: true`).

Socket.IO binary attachments appear in received messages as `{"$binary": "<base64>"}` at
their place in the arguments, and the same notation sends one (wherever it appears in the
arguments). With `transport: auto`, Pyro tries a WebSocket and falls back to HTTP
long-polling when the server or a proxy refuses it; the console shows the transport in use.

## `*.env.yaml`
```yaml
name: dev
variables:
  - { key: baseUrl, value: "http://localhost:8080" }
  - { key: token, value: "dev-token", secret: true }   # masked in the UI and logs
  - { key: legacy, value: "x", disabled: true }
```

## Examples (saved responses)
A request may keep `examples`: responses saved for documentation and sharing.
```yaml
examples:
  - name: 200 OK
    status: 200
    headers: [ { key: Content-Type, value: application/json } ]
    body: |
      { "id": "ord_1" }
```
**Save as example** on a response adds one; per-response headers (`Date`, `Content-Length`,
`Connection`, …) and `Set-Cookie` are left out, and JSON is pretty-printed. Postman
examples import and export; OpenAPI imports add one per documented JSON response.

## Variable scopes
`{{name}}` resolves from the narrowest scope outward:

1. **Runtime / VU variables:** set by extractors, scripts (`pm.variables.set`) or data-file rows.
2. **Data row:** the current iteration's CSV/JSON row.
3. **Environment:** the selected `*.env.yaml`.
4. **Folders, then collection:** `folder.yaml` / `collection.yaml` `variables`.
5. **Globals:** `globals.yaml` at the workspace root (same format as an environment),
   used everywhere: sends, collection runs, load tests and agents.

**Initial and current values.** The files hold *initial* values, shared through git.
While `pyro serve` runs, scripts and extractors (`pm.environment.set`, `pm.globals.set`, …)
and edits in the Variables page change *current* values, which apply to later sends but
are never written to the files. The Variables page (the sliders button next to the
environment menu) shows both and can reset a current value or save it as the initial one.
`GET/PATCH /api/v1/session/variables` expose them.

Dynamic variables such as `{{$guid}}`, `{{$timestamp}}` and `{{$randomInt}}` follow
Postman's names. A variable that can't be resolved is left as is and flagged in the response view.

## Auth inheritance
When a request has no `auth`, Pyro walks up the tree (folder → parent folder →
collection) and uses the first `auth` it finds. `type: none` explicitly disables auth
and stops that walk. Auth `type`: `none`, `basic`, `bearer`, `apikey`, `oauth2`, `digest`, `plugin:<name>`.
`params` holds the type-specific fields:

| Type | Params |
|---|---|
| basic | `username`, `password` |
| bearer | `token` |
| apikey | `key`, `value`, `in` (`header`/`query`) |
| oauth2 | Postman's names: `grant_type` (`authorization_code_with_pkce`, `authorization_code`, `client_credentials`, `password_credentials`; none = use `accessToken` as is), `accessToken`, `authUrl`, `accessTokenUrl`, `refreshTokenUrl`, `clientId`, `clientSecret`, `client_authentication` (`header` default, `body`), `scope`, `audience`, `username`/`password` (password grant), `redirect_uri` (default `http://<pyro>/oauth2/callback`), `challengeAlgorithm` (`S256` default, `plain`), `addTokenTo` (`header` default, `queryParams`), `headerPrefix` (default `Bearer`) |
| digest | `username`, `password`. Pyro answers the server's `401` Digest challenge (RFC 7616: MD5, SHA-256 and their `-sess` variants, `qop=auth`, `userhash`) by re-sending once; the challenge is cached per host so later requests authenticate without the extra round trip. |
| plugin:&lt;id&gt; | the plugin's declared fields (e.g. `plugin:hmac`: `secret`, `keyId`, `header`) |

### OAuth 2.0 tokens
With a grant configured (and no `accessToken`), Pyro gets tokens itself and caches them
in memory per configuration:
- **Client credentials** and **password** grants fetch a token on the first send (also in
  `pyro run`, load tests, where all VUs share one token, and on agents) and reuse it until
  30 s before it expires; a refresh token is used when the provider gave one.
- **Authorization code** (with or without PKCE) needs a browser sign-in: **Get new access
  token** in the Auth tab opens the provider's page; it redirects to Pyro's
  `/oauth2/callback` (register that URL with the provider), and the token is cached for
  later sends. CLI runs can't sign in; use a pasted token or another grant there.
- The implicit grant isn't supported (deprecated by OAuth 2.1).
Tokens are never written to the workspace.

## `*.load.yaml`
See [load-engine.md](load-engine.md#plan-format).
