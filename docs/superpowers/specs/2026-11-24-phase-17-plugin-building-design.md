# Phase 17: building plugins (dev loop + JavaScript plugins)

Status: draft for approval · 2026-11-24

## Intent

Pyro is open source. Both our own team and outside developers should be able to write a
plugin and run it in Pyro with little friction. Plugins already work (Go SDK, `pyro plugin
new`, install, approve), but the loop from "edit" to "see it run" is clunky, and Go is the
only language. Phase 17 fixes both. Sharing plugins (public index, signed entries, browse
and install in the app) is **phase 18**, designed separately.

## Goals
1. **A fast development loop.** `pyro plugin dev <dir>` rebuilds and reinstalls on every
   save, and a running `pyro serve` picks the change up within about a second, without a
   restart.
2. **Test a plugin without the app.** `pyro plugin test <dir>` runs one call and prints
   the result and the plugin's logs.
3. **JavaScript plugins** with no toolchain: `pyro-plugin.yaml` + `plugin.js`, the same
   three kinds as Go (protocol, auth, processor), the same sandbox and approval rules.
4. Docs: the user guide teaches both languages end to end.

## Non-goals
- Public index, signatures, browsing plugins in the app (phase 18).
- New plugin kinds (assertion, extractor, datasource, …) and streaming protocols.
- A Rust SDK.
- npm packages inside JS plugins. Authors who want them bundle into one file themselves
  (esbuild etc.); Pyro doesn't resolve `node_modules`.
- Node.js APIs (`fs`, `process`, `Buffer`, `setTimeout` event loop). JS plugins see only
  the `pyro` API.

## Anti-goals (would make this a failure even if it works)
- JS plugins escaping the sandbox that Go plugins live in (memory cap, timeouts,
  capability-checked network, approval).
- The dev loop corrupting `state.json` or losing an approval/setting because the CLI and
  the server both wrote it.
- `go test ./...` needing anything beyond the Go toolchain.

## Constraints
- Pure Go, `CGO_ENABLED=0`; `pkg/engine` never imports `internal/...`.
- ABI v1 stays: Go plugins built today keep working unchanged.
- The CLI and the server are separate processes sharing `~/.pyro/plugins`
  (or `$PYRO_PLUGINS_DIR`). The team server and agents use the same `plugin.Manager`.
- Docs updated in the same change (guide + `docs/plugins.md` + `docs/guide/cli.md`).

## Approaches considered

**JS runtime.** Measured on this machine (spike, goja compiled to `wasip1`):

| | A: goja inside WebAssembly | B: goja on the host |
|---|---|---|
| Binary size | +4.6 MB gzipped (21 MB raw) | 0 |
| First compile | ~7.5 s once, ~0.25 s from wazero's cache | none |
| Instance start | ~17–26 ms (pooled) | <1 ms |
| JS throughput | ~17× slower | native |
| Sandbox | same as Go plugins | no memory cap |

A third option, compiling JS to Wasm with Javy/QuickJS, needs a Rust toolchain for authors,
so it was dropped. **Chosen: A**, because phase 18 invites outsiders' plugins and every
plugin should run in the same sandbox. Docs position JS for convenience and Go for heavy load.

**Dev loop.** X: the server watches its plugins folder and reloads; Y: `pyro plugin dev`
uploads builds to the running server over the API (needs address + token, new endpoint);
Z: `pyro serve --plugin-dev <dir>` (restart to start or stop). **Chosen: X**, which works
with whatever is running and also removes today's "restart after a CLI install" caveat.

## Design

### 1. JavaScript plugins

**Manifest.** A new optional field `runtime: js` (default `wasm`). For JS, `module` names
the entry script (`module: plugin.js`). Everything else (provides, fields, capabilities,
config, limits) is unchanged.

```yaml
name: redis
version: 0.1.0
abi: 1
runtime: js
module: plugin.js
provides:
  - { kind: protocol, id: redis, title: Redis, urlPlaceholder: redis://localhost:6379 }
capabilities: { network: ["localhost:6379"] }
```

**The JS runtime module** (`jsrt`) is a Go program built for `GOOS=wasip1
-buildmode=c-shared` using goja and the Go plugin SDK. It is a normal ABI v1 plugin:
it exports `pyro_alloc/pyro_free/pyro_invoke` and imports the `pyro` host module. It adds
one op the host sends once per new instance, before anything else:

```jsonc
{"op": "load", "files": {"plugin.js": "…source…"}, "entry": "plugin.js"}  // → {} | {"error": "…"}
```

It runs the entry script, which registers handlers; `describe`, `execute`, `sign` and
`process` are then answered by calling those JS handlers. Errors thrown in JS become error
replies, with the JS stack in the message.

**JS API** (global `pyro`), mirroring the Go SDK:
```js
pyro.registerProtocol("redis", (ctx, req) => {
  const conn = pyro.dial("localhost:6379", { tls: false, timeoutMs: 5000 })
  try {
    conn.write(`PING\r\n`)                       // string (UTF-8) or Uint8Array
    const reply = conn.read({ timeoutMs: 2000 }) // Uint8Array; null at EOF; throws on timeout
    return { status: 0, statusText: "OK", body: reply, headers: {} }
  } finally { conn.close() }
})
pyro.registerAuth(id, (ctx, req) => { req.setHeader("X-Sig", …) })
pyro.registerProcessor(id, (ctx, p) => { p.setVar(…); p.test(name, passed, msg) })
pyro.log.info("…")  // debug/info/warn/error
pyro.encoding.{utf8, base64, hex}.{encode, decode}
pyro.crypto.{sha256, hmacSHA256}(…)   // returns Uint8Array
```
`req` has `kind, name, method, url, headers, query, body, options` plus `option(key, def)`,
`header(name)`, `setHeader(name, value)`; `ctx` has `config` and `params`, like Go.

**Host side** (`pkg/engine/plugin`):
- The runtime is embedded gzipped (`//go:embed jsrt.wasm.gz`) and compiled once per
  `Manager`. All JS plugins share the compiled module; each JS plugin gets its own instance
  pool, and every instance is sent `load` with that plugin's script before first use.
- Install/verify for `runtime: js`: skip the `\0asm` check, check the script is valid
  UTF-8 and under 4 MB, run `load` + `describe` exactly like a Go plugin.
- Compile the runtime in the background when the first JS plugin is installed or found
  at start, so no request waits on the one-time 7.5 s compile.
- Timeouts, capability checks and approval are untouched: they apply to the runtime
  instance exactly as to a Go module. Memory and instance limits are §1a.

### 1a. Memory and instance limits (all plugins)

**Enforce `limits.memoryMB` per plugin (fixes an existing gap).** Today the manifest's
`memoryMB` (default 128) is parsed but never applied; every instance of every plugin gets
the single 512 MB ceiling set in `newHost` (`runtime.go`). Phase 17 makes the manifest
value real for Go and JS: the Manager keeps one wazero Runtime per distinct memory limit
(sharing the on-disk compilation cache), and each plugin compiles into the Runtime for its
limit. The JS runtime compiles once per distinct limit used by JS plugins (~0.25 s from
cache after the first time). Defaults: 128 MB for Go plugins (as documented), **256 MB for
JS plugins**; confirm 256 by measuring a JS plugin's resident memory under a test that
allocates, and adjust the default if needed. A manifest may raise its limit up to 1024 MB;
the approval screen and `pyro plugin list` show the limit, and a changed limit counts as a
changed capability set (needs re-approval).

**Cap live instances per plugin.** New optional `limits.maxInstances`. Default: 32 for JS
plugins; Go plugins keep today's unbounded behaviour unless the manifest sets it. When
all instances are busy, a call waits for one, up to the call's timeout, then fails with
"plugin redis is busy: all 32 instances in use". The idle-pool limit becomes
min(maxInstances, 4096). The load engine warns at the start of a run when a plan's peak
VUs exceed a used plugin's `maxInstances`:
"plugin redis is JavaScript and runs at most 32 calls at once; use a Go plugin for heavier load".
The docs' "JS or Go?" note explains both limits.

**Where the runtime lives.** Source in `plugins/runtime/js` (its own Go module, depends on
the SDK and goja). `make jsrt` builds it with `-trimpath -ldflags=-s -w` and writes
`pkg/engine/plugin/jsrt.wasm.gz`, which is **committed** so `go test ./...` and
`go install` work with only the Go toolchain. CI rebuilds it and fails if the committed file
differs (the build is deterministic for a pinned Go version).

**TypeScript.** `pyro plugin new --lang js` writes `pyro.d.ts` next to `plugin.js` so
editors autocomplete the API. Authors who write TypeScript compile to one JS file
themselves.

### 2. The running app reloads plugins (`Manager.Reload`)

- New `Manager.Reload()` re-reads `state.json` and rescans the folder. For each plugin it
  compares a fingerprint (manifest + module size and mtime): new ones load, removed ones
  drop, changed ones swap in (the old pool closes after in-flight calls finish).
  Unchanged plugins keep their warm pools.
- `pyro serve` (and `serve --team`, and `pyro agent`) poll the folder every second
  (`state.json` + top-level plugin dirs' fingerprints; no new dependency) and call
  `Reload` on change. The server then publishes a `plugins` event over `/api/v1/events`;
  the Plugins tab and the request Type/Auth menus refetch.
- **No lost writes:** every Manager write becomes read-modify-write: re-read `state.json`,
  apply the one change, write atomically (temp + rename, as today), under an advisory
  lock file (`.state.lock`, `O_CREATE|O_EXCL` with stale-lock timeout; pure Go, works on
  Windows). The CLI and the server can then both change state safely.
- The docs drop the "restart after CLI changes" caveat.

### 3. `pyro plugin dev <dir>`

- Watches the plugin directory (poll, 500 ms, ignoring the built module and dot-files).
- On change: Go plugins run `GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o
  <module> .` in the dir; JS plugins need no build. Then install from the dir.
- **Approval in dev:** the developer running `dev` is the author, so `dev` approves the
  manifest's capabilities on install and prints them (`approved network: localhost:6379`).
  If capabilities change between saves it prints the new set and approves again.
- Output per cycle: `✓ rebuilt redis 0.1.0 in 1.4s, installed` or the build/verify error
  (compiler output verbatim). It keeps watching after errors. Ctrl-C stops; the plugin
  stays installed.
- Running `pyro serve` sees each install through Reload (§2).

### 4. `pyro plugin test <dir | name>`

Runs one call in-process with a temporary Manager (nothing installed, approval implied),
or against an installed plugin by name.
```sh
pyro plugin test ./redis --url redis://localhost:6379 --body 'PING' --opt timeoutMs=2000
pyro plugin test ./hmac  --auth hmac --param secret=s3cret --url https://api.test/x
pyro plugin test ./jwt-check --processor jwt-check --phase post --status 200 --body '{"…"}'
```
Prints the response (status, headers, body; `--json` for machine output), the signed
request (auth), or vars/tests (processor), plus the plugin's logs and the call time. Exit
code 1 on an error or a failed test, so plugin authors can use it in their own CI. With
several provides, the kind/id flag picks one; with one, it's implied.

### 5. Scaffolding
`pyro plugin new <dir> --lang go|js` (default `go`). JS writes `pyro-plugin.yaml`
(`runtime: js`), `plugin.js`, `pyro.d.ts`, `README.md`. The printed next step becomes
`pyro plugin dev <dir>` for both languages. Templates exist for each kind × language.

### 6. Examples and docs
- `plugins/examples/redis-js`: a JS protocol plugin (RESP over TCP); tested in CI
  against an in-process fake RESP server, like the TCP example.
- `docs/guide/plugins.md`: rewrite "Writing a plugin" as a walkthrough (new → dev →
  test → pack) with Go and JS tabs, plus a "JS or Go?" note with the numbers above.
- `docs/plugins.md`: `runtime`, the `load` op, the JS API reference, `dev`/`test`.
- `docs/guide/cli.md`: `plugin dev`, `plugin test`, `plugin new --lang`.
- `docs/roadmap.md`: phase 17 (and 18 as planned), `CHANGELOG.md`, `CLAUDE.md` phase line.

## Testing
- JS runtime: unit tests in `plugins/runtime/js` for API behaviour; host tests install
  the JS example, run execute/sign/process, check capability denial, timeouts (an infinite
  JS loop is cut at the timeout and the instance discarded) and the memory cap.
- Limits: a Go and a JS plugin that allocate past their `memoryMB` fail with a clear
  error while a plugin with a higher limit succeeds; with `maxInstances: 2`, a third
  concurrent call waits and then succeeds, or times out with the "busy" error; the load
  engine prints the warning when VUs exceed the cap.
- Reload: tests where a second Manager on the same dir installs/removes/approves while the
  first reloads; a concurrency test that interleaves writes from two Managers and asserts
  no state is lost.
- `plugin dev` / `plugin test`: CLI tests with `t.TempDir()` plugins (JS needs no build;
  one Go case builds a tiny plugin, as plugin tests already do).
- e2e: a JS plugin installed while `pyro serve` runs appears in the Type menu without a
  reload of the page.

## Exit criteria
A developer runs `pyro plugin new ./x --lang js`, `pyro plugin dev ./x`, edits
`plugin.js`, and sees the change in a running `pyro serve` within ~2 s with no restart;
`pyro plugin test` runs it from the terminal; the same works with `--lang go` (rebuild
time aside). JS plugins pass the same sandbox tests as Go plugins.

## Decided with the user after review
- Enforce `memoryMB` per plugin for Go and JS (§1a), JS default 256 MB.
- Cap live JS instances with `limits.maxInstances` (default 32), queue up to the call
  timeout, warn in load tests.

## Calls made without asking (check these)
- Max `memoryMB` of 1024 MB, and a changed limit needing re-approval.
- In team mode, the `plugins` event goes to every project's server, since they share one
  Manager. Archives (`pack`, `InstallArchive`) accept a `.js` module when `runtime: js`.
- Committing the built `jsrt.wasm.gz` (~4.6 MB) to git, with CI verifying it is up to date.
- `pyro plugin dev` auto-approves the plugin's capabilities (it is the author's own plugin).
- Polling instead of fsnotify (no new dependency; 1 s is fast enough).
- JS API shape (`pyro.dial`, `conn.read` returns `Uint8Array`, `pyro.encoding`,
  `pyro.crypto`) and the example plugin (Redis).

## Left to the builder
- jsrt internals (how handlers are stored, goja ↔ JSON conversion details).
- Exact `plugin test` flag names beyond those shown, and output formatting.
- Fingerprint details and lock timeout values.
