# Plugins (implemented in Phase 4)

Pyro plugins are WebAssembly modules (WASI, reactor mode) run by
[wazero](https://wazero.io), which is pure Go. They are sandboxed, run on every OS
and architecture, and can only reach the network addresses they declare and you approve.

Code: host in `pkg/engine/plugin`, Go SDK in `plugins/sdk/go`, examples in
`plugins/examples/{tcp,hmac}`.

## What plugins can add
| Kind | Status | Purpose | Example |
|---|---|---|---|
| `protocol` | ✅ | A request type `plugin:<id>` (request/response) | Raw TCP (`plugins/examples/tcp`) |
| `auth` | ✅ | An auth type `plugin:<id>` that signs requests | HMAC-SHA256 (`plugins/examples/hmac`) |
| `processor` | ✅ | A pre/post processor `plugin:<id>` that sets variables, rewrites the request (pre) or reports checks (post) | HMAC sign/verify (`hmac-var` in `plugins/examples/hmac`) |
| streaming protocols | planned | Sessions like WebSocket (connect/send/receive) | MQTT |
| `assertion`, `extractor`, `datasource`, `reporter`, `importer`, `ui` | planned | | |

Plugin requests work everywhere HTTP requests do: the editor, scripts, assertions,
extractors, collection runs and load tests. In load results they are reported with the
plugin id where HTTP shows the method (e.g. `TCP`), under the `http_*` metric names.
A plugin auth type can sign any request (HTTP or plugin), set on a request, folder or collection.

## Manifest: `pyro-plugin.yaml`
```yaml
name: tcp                 # lowercase letters, digits, dashes
version: 0.1.0
abi: 1
title: Raw TCP
description: Send bytes over a TCP (or TLS) connection and read the reply.
author: Pyro
license: Apache-2.0
module: tcp.wasm          # the module file next to the manifest
provides:
  - kind: protocol        # or auth
    id: tcp               # request kind becomes plugin:tcp
    title: TCP
    urlPlaceholder: tcp://localhost:7000
    fields:               # rendered as a form; stored in request.options (auth: auth.params)
      - { key: readUntil, label: Read until, type: select, options: [delimiter, close, bytes, idle], default: delimiter }
      - { key: timeoutMs, label: Read timeout (ms), type: number, default: "5000", help: "…" }
capabilities:
  network: ["*:*"]        # host:port patterns; see below
config: []                # optional plugin-wide settings, same field format
limits: { memoryMB: 128 } # optional
```

Field `type`: `string`, `secret` (masked), `number`, `boolean`, `select` (with `options`),
`text` (multi-line). Values are strings and may contain `{{variables}}`, which Pyro resolves
before the plugin sees the request.

## Capabilities and approval
- `network` lists `host:port` patterns the plugin may connect to. `*` matches any host or
  any port, and host globs work (`*.example.com:443`). Hosts match **literally**: list both
  `localhost:*` and `127.0.0.1:*` if you use both.
- Plugins have no filesystem, environment or clock access beyond what WASI gives a
  sandboxed module, and they can't open connections except through `net_dial`, which checks
  the capabilities on every call.
- A newly installed plugin is **not usable until approved**. Approval records the exact
  capability set. Upgrading to a version that asks for anything different needs approval
  again; reinstalling with the same capabilities keeps it.
- Approve in the Plugins tab, with `pyro plugin approve <name>`, or with
  `pyro plugin install … --yes`.
- Every call has a 60 s timeout (or the request's own), and connections a plugin leaves
  open are closed when the call ends. A crash or timeout discards that instance.

## ABI v1
Messages crossing the boundary are **JSON**, passed through the module's linear memory
(see [ADR 0005](adr/0005-json-abi-and-go-sdk.md) for why JSON instead of MessagePack).

**Exported by the plugin**
| Function | Purpose |
|---|---|
| `pyro_alloc(size u32) -> ptr u32` | Buffer for the host to write a message into |
| `pyro_free(ptr u32, size u32)` | Release a buffer the plugin returned |
| `pyro_invoke(ptr u32, size u32) -> u64` | Handle one message; returns `ptr<<32 \| len` of the JSON reply |
| `_initialize` | WASI reactor start (Go's `-buildmode=c-shared` provides it) |

**Messages** (host → plugin, and the reply):
```jsonc
{"op": "describe"}                                  // → {"abi": 1, "provides": [{"kind": "protocol", "id": "tcp"}]}
{"op": "execute", "id": "tcp", "request": {…}, "config": {…}}
                                                    // → {"response": {"status", "statusText", "headers", "body" (base64), "remoteAddr"}} | {"error": "…"}
{"op": "sign", "id": "hmac", "request": {…}, "params": {…}, "config": {…}}
                                                    // → {"request": {…headers, query, url updated}} | {"error": "…"}
{"op": "process", "id": "hmac-var", "phase": "pre" | "post", "request": {…}, "response": {…},
 "vars": {…}, "params": {…}, "config": {…}}
                                                    // → {"vars": {…to set}, "request": {…} (pre only), "tests": [{"name", "passed", "error"}]} | {"error": "…"}
```
For `process`, `request` is unresolved before the request (pre) and as sent after it (post);
`response` (post only) is `{status, statusText, headers, body (base64), timeMs}`; `vars`
holds every variable in scope; `params` are the processor's fields with variables resolved.
A pre-processor may change the method, URL, headers, query and text body. A processor's
manifest entry may list `phases: [pre]` or `[post]` (default both).
`request` is `{kind, name, method, url, headers, query, body, options}` with variables
resolved (except before the request in `process`). On install, Pyro calls `describe` and refuses modules that don't register
everything their manifest declares.

**Imported from the host (module `pyro`)**
| Function | Notes |
|---|---|
| `log(level u32, ptr, len)` | 0 debug, 1 info, 2 warn, 3 error; shown in the response's console |
| `net_dial(addrPtr, addrLen, tls u32, timeoutMs u32) -> i32` | Handle ≥ 1, or −1 (see `last_error`). Capability-checked. |
| `net_write(handle, ptr, len) -> i32` | Bytes written or −1 |
| `net_read(handle, ptr, cap, timeoutMs) -> i32` | Bytes read, 0 at EOF, −2 on timeout, −1 on error |
| `net_close(handle)` | |
| `last_error(ptr, cap) -> u32` | Copies the last error message |

## Go SDK
Plugins build with the **standard Go toolchain** (1.24+); no TinyGo needed:
```go
package main

import pyro "github.com/codeen15/pyro/plugins/sdk/go"

func init() {
	pyro.RegisterProtocol("echo", pyro.ProtocolFunc(func(ctx *pyro.Context, req *pyro.Request) (*pyro.Response, error) {
		conn, err := pyro.Dial("localhost:7000", false, 5*time.Second)
		…
		return &pyro.Response{StatusText: "OK", Body: reply}, nil
	}))
}

func main() {}
```
```sh
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o echo.wasm .
```
SDK API: `RegisterProtocol`, `RegisterAuth`, `Request` (`Option`, `Header`, `SetHeader`),
`Response`, `Context` (`Config`, `Params`), `Dial` → `Conn` (`Read`, `Write`, `Close`,
`SetReadTimeout`; `ErrTimeout`, `io.EOF`), `Logf`. Panics in plugin code become error replies.

`pyro plugin new <dir> --kind protocol|auth|processor --id <id> [--sdk <path>]` scaffolds a plugin.
The SDK module isn't published yet, so pass `--sdk` with the path to `plugins/sdk/go` in a
Pyro checkout. A Go plugin module is about 2–5 MB; instances start in 2–4 ms and are pooled
(one per concurrent call), so load tests pay that once per VU.

**Rust SDK: not written yet.** The ABI is language-neutral, so any language that targets
WASI and can export functions works.

## Installing and managing
```sh
pyro plugin install ./plugins/examples/tcp        # a directory with the manifest + module
pyro plugin install tcp-0.1.0.pyro-plugin         # an archive (zip)
pyro plugin install tcp --index https://…/index.json
pyro plugin list | approve <n> | enable <n> | disable <n> | remove <n>
pyro plugin search [query] --index …
pyro plugin pack <dir>                            # writes <name>-<version>.pyro-plugin, prints its sha256
make plugins                                      # builds and packs the examples into plugins/dist
```
Plugins install to `~/.pyro/plugins/<name>/` (or `$PYRO_PLUGINS_DIR`); approvals, enabled
state and settings are in `state.json` there; compiled modules are cached in `cache/`.
`pyro serve` reads the folder at start, so changes made with the CLI while it runs show up
after a restart (changes made in the UI apply immediately).

The UI's Plugins tab lists plugins, installs archives, shows capabilities with an Approve
button, toggles plugins on and off, edits settings and removes plugins. A request's
**Type** menu offers installed request types; the Auth tab offers installed auth types.

## Plugin index
An index is a JSON file; archive URLs may be relative to it, and every entry needs a sha256:
```json
{"plugins": [{"name": "tcp", "version": "0.1.0", "title": "Raw TCP", "kinds": ["protocol"],
              "url": "tcp-0.1.0.pyro-plugin", "sha256": "8da00d9e…"}]}
```
Point Pyro at one with `--index` or `PYRO_PLUGIN_INDEX` (also used by `pyro serve` for the
UI). A public index isn't published yet. Signed entries (minisign/cosign) are planned.
