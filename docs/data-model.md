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
Extractor `from`: `jsonpath`, `header`, `regex`, `status`.

### WebSocket
```yaml
kind: websocket
name: Notifications
url: wss://api.example.com/ws
headers: [ { key: Authorization, value: "Bearer {{token}}" } ]
messages:                       # saved message templates
  - { name: Subscribe, content: '{"op":"sub","topic":"orders"}' }
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
messages:
  - { event: "chat message", content: '["hi"]', ack: true }   # content = JSON args array
```

## `*.env.yaml`
```yaml
name: dev
variables:
  - { key: baseUrl, value: "http://localhost:8080" }
  - { key: token, value: "dev-token", secret: true }   # masked in the UI and logs
  - { key: legacy, value: "x", disabled: true }
```

## Variable scopes (Phase 1)
`{{name}}` resolves from the narrowest scope outward:

1. **Runtime / VU variables:** set by extractors, scripts (`pm.variables.set`) or data-file rows.
2. **Data row:** the current iteration's CSV/JSON row.
3. **Environment:** the selected `*.env.yaml`.
4. **Collection:** `collection.yaml` `variables`.
5. **Globals:** workspace-level (planned).

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
| oauth2 | `grantType`, `tokenUrl`, `clientId`, `clientSecret`, `scope`, … |
| digest | `username`, `password` |
| plugin:&lt;id&gt; | the plugin's declared fields (e.g. `plugin:hmac`: `secret`, `keyId`, `header`) |

## `*.load.yaml`
See [load-engine.md](load-engine.md#plan-format).
