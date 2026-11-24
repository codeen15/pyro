# Phase 17: Plugin Building Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make writing plugins easy: a `pyro plugin dev` loop that hot-reloads into a running
`pyro serve`, `pyro plugin test`, and JavaScript plugins that run in the same Wasm sandbox
as Go plugins, with enforced per-plugin memory and instance limits.

**Architecture:** JS plugins run on a goja-based runtime compiled to `wasip1` (`jsrt`),
embedded gzipped in `pkg/engine/plugin` and fed each plugin's script by a new `load` op.
The plugin `Manager` gains read-modify-write state under a lock file, `Reload`/`Watch`
(1 s polling), per-memory-limit wazero runtimes and a per-plugin instance cap. The CLI
gains `plugin dev`, `plugin test` and `plugin new --lang js`; the server publishes a
`plugins` event so the UI refreshes.

**Tech Stack:** Go 1.24+, wazero v1.12.0, goja, cobra, React + antd + react-query.

**Spec:** `docs/superpowers/specs/2026-11-24-phase-17-plugin-building-design.md`

## Global Constraints
- Pure Go, `CGO_ENABLED=0`, no new third-party dependency (polling, not fsnotify).
- `pkg/engine` never imports `internal/...`.
- ABI v1 unchanged for existing Go plugins; `describe/execute/sign/process` keep their shapes.
- Defaults: `memoryMB` 128 (wasm runtime), 256 (js runtime); max `memoryMB` 1024;
  `maxInstances` default 32 for js, 0 = unbounded for wasm.
- JS script limit 4 MB, valid UTF-8. Watch poll interval 1 s (server), 500 ms (`plugin dev`).
- Busy error text: `plugin <name> is busy: all <n> instances in use`.
- Load warning text: `plugin <name> is JavaScript and runs at most <n> calls at once; use a Go plugin for heavier load`
  (for wasm plugins with a cap: `plugin <name> runs at most <n> calls at once; the test peaks at <vus> VUs`).
- Tests: in-process servers, `t.TempDir()`, no network. `gofmt` clean.
- Docs updated in the same change as each user-visible feature (see Task 14 for the
  final pass; tasks note their own doc lines).

## Review Focus
1. **Upgrading Pyro with plugins already approved** (state.json has no `approvedMemoryMB`):
   they must stay approved. Test in Task 1.
2. **CLI and server writing state.json at the same time** (approve in UI while
   `plugin dev` installs): no lost approval/config. Test in Task 3.
3. **Reload while a call is in flight**: the call finishes on the old instance; no crash,
   the next call uses the new code. Test in Task 4.
4. **JS plugin with a syntax error or that throws at load**: install fails with the JS
   error and line number; `plugin dev` prints it and keeps watching. Tests in Tasks 8 and 11.
5. **`plugin dev` when the Go build fails**: the previously installed version stays
   installed and working. Test in Task 11.

---

### Task 1: Manifest: `runtime`, `.js` modules, `maxInstances`, memory defaults, approval of limits

**Files:**
- Modify: `pkg/engine/plugin/manifest.go` (Manifest, Limits, regexes, ParseManifest)
- Modify: `pkg/engine/plugin/manager.go` (`pluginState`, `info`, `Approve`, `install`)
- Test: `pkg/engine/plugin/plugin_test.go` (`TestManifestValidation` additions), new `pkg/engine/plugin/limits_test.go`

**Interfaces:**
- Produces:
  - `const RuntimeWasm = "wasm"`, `const RuntimeJS = "js"`
  - `Manifest.Runtime string` (`yaml:"runtime,omitempty" json:"runtime,omitempty"`), normalised to `"wasm"` when empty
  - `Limits.MaxInstances int` (`yaml:"maxInstances,omitempty" json:"maxInstances,omitempty"`)
  - `pluginState.ApprovedMemoryMB int` (`json:"approvedMemoryMB,omitempty"`)
  - `func (m *Manifest) IsJS() bool`

- [ ] **Step 1: Write failing tests** in `TestManifestValidation` / `limits_test.go`:
  - `runtime: js` + `module: plugin.js` parses; `Limits.MemoryMB == 256`, `MaxInstances == 32`.
  - no runtime + `module: x.wasm` → `Runtime == "wasm"`, `MemoryMB == 128`, `MaxInstances == 0`.
  - `runtime: js` + `module: x.wasm` → error containing `module must be a .js file`; `runtime: wasm` + `.js` → error containing `module must be a .wasm file`; `runtime: lua` → error containing `runtime must be wasm or js`.
  - `limits.memoryMB: 2048` → error containing `memoryMB must be at most 1024`; `maxInstances: -1` → error.
  - `TestApprovalCoversMemoryLimit`: install a plugin with approve, rewrite its manifest with `memoryMB: 512`, reinstall without approve → `Info.Approved == false`; `Approve` → true.
  - `TestLegacyApprovalStillValid`: write a `state.json` with `{"plugins":{"tcp":{"enabled":true,"approved":{"network":["*:*"]}}}}` (no `approvedMemoryMB`) next to the installed tcp example, `Open` → `Info.Approved == true`.

- [ ] **Step 2: Run** `go test ./pkg/engine/plugin -run 'TestManifestValidation|TestApprovalCoversMemoryLimit|TestLegacyApprovalStillValid'` — expect FAIL.

- [ ] **Step 3: Implement.** `moduleRe` splits into `wasmModuleRe` (`\.wasm$`) and `jsModuleRe` (`\.js$`). `info()` treats approval as `ps.Approved.Equal(caps) && approvedMem(ps) == man.Limits.MemoryMB`, where `approvedMem` returns 128 when `ApprovedMemoryMB == 0`. `Approve` and `install(approve=true)` record `ApprovedMemoryMB`. Show the limit where
  capabilities are shown: `approveFlow` and `pyro plugin list` in `cmd/pyro/plugin.go`
  print `memory: <n> MB` (and `runtime: js` for JS plugins), and `web/src/components/Plugins.tsx`
  shows `Memory <n> MB` beside the network capabilities (antd `Descriptions`, neutral text).

- [ ] **Step 4: Run the tests** — expect PASS; also `go test ./pkg/engine/plugin` stays green.

- [ ] **Step 5: Commit** `git commit -m "Plugins: runtime field, .js modules, maxInstances, memory limit approval"`

### Task 2: Enforce memory limits per plugin; cap live instances

**Files:**
- Modify: `pkg/engine/plugin/runtime.go` (`pool`), `pkg/engine/plugin/manager.go` (`Manager.h` → `hosts`, `host`, `compile`, `verify`, `Close`)
- Create: `pkg/engine/plugin/testdata/alloc/{go.mod,main.go,pyro-plugin.yaml}` (Go plugin, protocol `alloc`: allocates `options.mb` MB and touches every page, or sleeps `options.sleepMs`; `go.mod` replaces the SDK with `../../../../../plugins/sdk/go`)
- Modify: `pkg/engine/plugin/plugin_test.go` `TestMain` (also build `testdata/alloc` into `built["alloc"]`)
- Test: `pkg/engine/plugin/limits_test.go`

**Interfaces:**
- Produces:
  - `func (m *Manager) host(ctx context.Context, memoryMB int) (*host, error)` — one `*host` per distinct limit, all sharing `<dir>/cache`.
  - `pool.max int` and `pool.slots chan struct{}` (nil when `max == 0`); `pool.get` acquires a slot (waits on `ctx.Done()`), `put`/discard release it.
  - `var ErrBusy = errors.New("plugin busy")`; the returned error wraps it with the Global Constraints text.

- [ ] **Step 1: Write failing tests:**
  - `TestMemoryLimitEnforced`: install `alloc` with `memoryMB: 64`, Execute with `mb=100` → error (trap); with `memoryMB: 256` and `mb=100` → success.
  - `TestMaxInstancesQueues`: `maxInstances: 2`, three concurrent calls with `sleepMs=300` and a 5 s deadline → all succeed, total time ≥ 600 ms.
  - `TestMaxInstancesBusy`: `maxInstances: 1`, call A `sleepMs=1000`; call B with a 200 ms deadline → `errors.Is(err, ErrBusy)` and message `plugin alloc is busy: all 1 instances in use`.

- [ ] **Step 2: Run** `go test ./pkg/engine/plugin -run 'TestMemoryLimit|TestMaxInstances'` — expect FAIL.

- [ ] **Step 3: Implement.** `newHost(ctx, cacheDir, memoryMB)` already takes the limit; `Manager.hosts map[int]*host`. `compile`/`verify` use `m.host(ctx, l.man.Limits.MemoryMB)`. Idle cap becomes `min(max, maxIdle)` when `max > 0`. A slot wait that ends because the ctx ended returns the busy error.

- [ ] **Step 4: Run** `go test ./pkg/engine/plugin` — expect PASS.

- [ ] **Step 5: Commit** `git commit -m "Plugins: enforce memoryMB per plugin; cap live instances"`

### Task 3: state.json read-modify-write under a lock file

**Files:**
- Create: `pkg/engine/plugin/state.go` (move `state`, `pluginState`, `saveState` here)
- Modify: `pkg/engine/plugin/manager.go` (`install`, `Remove`, `update` use `mutate`)
- Test: `pkg/engine/plugin/state_test.go`

**Interfaces:**
- Produces:
  - `func (m *Manager) mutate(f func(st *state) error) error` — takes `<dir>/.state.lock` (`os.OpenFile` with `O_CREATE|O_EXCL`, retry every 20 ms, treat a lock older than 10 s as stale and remove it, give up after 5 s with `plugin state is locked by another Pyro process`), re-reads `state.json`, applies `f`, writes temp + rename, sets `m.st`, unlocks.
  - `func readState(dir string) (state, error)`

- [ ] **Step 1: Write failing tests:**
  - `TestTwoManagersDontLoseWrites`: two `Open` on the same dir with the tcp and hmac examples installed; 50 goroutines alternately `SetConfig` on manager A (tcp, `{"n": i}`) and `Approve`/`SetEnabled` on manager B (hmac); afterwards a fresh `Open` shows tcp's last config and hmac approved+enabled.
  - `TestStaleLockIsBroken`: create `.state.lock` with mtime 1 min ago → `SetEnabled` succeeds.

- [ ] **Step 2: Run** `go test ./pkg/engine/plugin -run 'TestTwoManagers|TestStaleLock' -race` — expect FAIL.

- [ ] **Step 3: Implement** as in Interfaces. `m.mu` is held across `mutate` so in-process callers stay serialised.

- [ ] **Step 4: Run** the tests with `-race` — expect PASS.

- [ ] **Step 5: Commit** `git commit -m "Plugins: lock and re-read state.json on every write"`

### Task 4: `Manager.Reload`, `Watch`, `Subscribe`

**Files:**
- Create: `pkg/engine/plugin/reload.go`
- Modify: `pkg/engine/plugin/manager.go` (`loaded` gets `fp fingerprint`; `Open` uses the same scan as `Reload`)
- Test: `pkg/engine/plugin/reload_test.go`

**Interfaces:**
- Produces:
  - `type fingerprint struct{ manifest, module int64; size int64 }` (mtimes in ns + module size)
  - `func (m *Manager) Reload() (changed bool, err error)` — rescans; new → load, gone → close pool + drop, fingerprint differs → replace (old pool is closed once its in-flight calls return: `pool.retire()` closes idle now and closes busy instances on `put`).
  - `func (m *Manager) Watch(ctx context.Context, every time.Duration)` — blocks; each tick compares `state.json` mtime and the dir fingerprints; on change calls `Reload` and then every subscriber.
  - `func (m *Manager) Subscribe(f func()) (cancel func())`
  - Mutations in this process (`install`, `Remove`, `update`) also notify subscribers.

- [ ] **Step 1: Write failing tests:**
  - `TestReloadSeesOtherProcessInstall`: A and B on one dir; B `InstallDir(tcp, true)`; `A.Reload()` → `changed == true`, A's `Execute` against `echoServer` works.
  - `TestReloadSeesRemoveAndApprove`: B removes → A no longer lists it; B approves → A's `Info.Approved` true after Reload.
  - `TestReloadDuringCall`: A starts a slow `alloc` call (`sleepMs=500`); B reinstalls `alloc`; A.Reload; the slow call returns success; next call succeeds.
  - `TestWatchNotifies`: `go A.Watch(ctx, 50*time.Millisecond)`, `Subscribe` a channel send; B installs → notified within 1 s.

- [ ] **Step 2: Run** `go test ./pkg/engine/plugin -run 'TestReload|TestWatch' -race` — expect FAIL.

- [ ] **Step 3: Implement** as in Interfaces.

- [ ] **Step 4: Run** with `-race` — expect PASS.

- [ ] **Step 5: Commit** `git commit -m "Plugins: Reload, Watch and change subscribers"`

### Task 5: Wire Watch into serve/team/agent; `plugins` event; UI refresh

**Files:**
- Modify: `cmd/pyro/main.go` (serve), `cmd/pyro/team.go`, `cmd/pyro/agent.go`: after `openPlugins()`, `go plugins.Watch(ctx, time.Second)` with the command's context.
- Modify: `internal/server/server.go` (`New`: if `cfg.Engine.Plugins != nil`, `s.unsubPlugins = m.Subscribe(func(){ s.hub.publish("plugins", "changed", nil) })`; `Close` calls it)
- Modify: `web/src/App.tsx` (subscribe to `plugins`; on event `queryClient.invalidateQueries({ queryKey: ["plugins"] })`)
- Modify: `docs/api.md` (events: `plugins` topic, type `changed`, no data)
- Test: `internal/server/plugins_test.go`

**Interfaces:**
- Consumes: `Manager.Watch`, `Manager.Subscribe` (Task 4).

- [ ] **Step 1: Write failing test** `TestPluginsEventOnChange`: server with a plugin Manager on a temp dir; subscribe to topic `plugins` over `/api/v1/events`; install tcp through a second Manager on the same dir and call `Reload` on the server's Manager → receive `{"topic":"plugins","type":"changed"}` within 2 s.
- [ ] **Step 2: Run** `go test ./internal/server -run TestPluginsEventOnChange` — expect FAIL.
- [ ] **Step 3: Implement** the wiring above; drop the "restart after CLI changes" sentence from `docs/plugins.md` and `docs/guide/plugins.md`.
- [ ] **Step 4: Run** `go test ./internal/server ./cmd/...` and `cd web && npm run typecheck` — expect PASS.
- [ ] **Step 5: Commit** `git commit -m "Serve reloads plugins on change and tells the UI"`

### Task 6: SDK hook for runtimes (`HandleOp`)

**Files:**
- Modify: `plugins/sdk/go/pyro.go` (`handle`)
- Test: `plugins/sdk/go/pyro_test.go`

**Interfaces:**
- Produces: `func HandleOp(op string, f func(raw json.RawMessage) any)` — extra ops for language runtimes; `handle` consults it before `unknown operation`. The reply value is JSON-encoded as-is. `describe` already reads the registration maps at call time, so a runtime can register handlers during `load` with the existing `Register*` functions.

- [ ] **Step 1: Write failing test** `TestHandleOp`: `HandleOp("load", ...)` returning `map[string]string{"ok":"1"}`; `dispatch([]byte(`{"op":"load","x":1}`))` → `{"ok":"1"}`, and the handler saw the raw message.
- [ ] **Step 2: Run** `cd plugins/sdk/go && go test ./...` — expect FAIL.
- [ ] **Step 3: Implement.** `dispatch` keeps the raw bytes and passes them to the op handler.
- [ ] **Step 4: Run** — expect PASS.
- [ ] **Step 5: Commit** `git commit -m "Go SDK: HandleOp for language runtimes"` (tag `plugins/sdk/go/v0.2.0` at release; bump `sdkVersion` in `cmd/pyro/plugin.go` then, not now).

### Task 7: The JS runtime (`plugins/runtime/js`) and `make jsrt`

**Files:**
- Create: `plugins/runtime/js/go.mod` (module `github.com/codeen15/pyro/plugins/runtime/js`; requires goja at the root module's version and the SDK with `replace ../../sdk/go`)
- Create: `plugins/runtime/js/main.go` (load op, registration bridge), `api.go` (the `pyro` object), `convert.go` (Request/Response ↔ JS)
- Create: `plugins/runtime/js/api_test.go` (native tests of pure parts)
- Modify: `Makefile` (`jsrt` target), `.github/workflows/ci.yml` (rebuild and `git diff --exit-code pkg/engine/plugin/jsrt.wasm.gz`; gofmt/vet/test for the new module)
- Create (generated): `pkg/engine/plugin/jsrt.wasm.gz`

**Interfaces:**
- Consumes: `pyro.HandleOp`, `pyro.RegisterProtocol/Auth/Processor`, `pyro.Dial`, `pyro.Logf` (SDK).
- Produces:
  - op `{"op":"load","entry":"plugin.js","files":{"plugin.js":"…"}}` → `{}` or `{"error":"plugin.js:12:5 SyntaxError: …"}`. A second `load` on the same instance returns an error.
  - JS API exactly as in the spec §1: `pyro.registerProtocol(id, fn)`, `registerAuth`, `registerProcessor`, `pyro.dial(addr, {tls, timeoutMs}) → {write(string|Uint8Array), read({timeoutMs, max}) → Uint8Array|null, close()}`, `pyro.log.{debug,info,warn,error}`, `pyro.encoding.{utf8,base64,hex}.{encode,decode}`, `pyro.crypto.sha256(data)`, `pyro.crypto.hmacSHA256(key, data)` (both → Uint8Array; string args are UTF-8). Request object: fields of SDK `Request` plus `option(key, def)`, `header(name)`, `setHeader(name, value)`. Processor object: `phase, request, response, vars, setVar(n, v), test(name, passed, msg)`. Protocol handlers return `{status, statusText, headers (object or [{key,value}]), body (string|Uint8Array), remoteAddr}`.
  - Read timeout throws `Error("read timed out")`; EOF returns `null`. Thrown JS errors become the reply's `error` with `name: message` and the first stack frame.
  - `make jsrt`: `cd plugins/runtime/js && GOOS=wasip1 GOARCH=wasm go build -trimpath -buildmode=c-shared -ldflags="-s -w -buildid=" -o /tmp/jsrt.wasm . && gzip -n -9 -c … > pkg/engine/plugin/jsrt.wasm.gz`.

- [ ] **Step 1: Write failing native tests** in `api_test.go`: `encoding` round-trips (`hex.encode(utf8.encode("hi")) == "6869"`, base64 of 0–255 bytes), `crypto.hmacSHA256("key","The quick brown fox jumps over the lazy dog")` hex == `f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8`, request helpers (`option` default, `setHeader` replaces case-insensitively), response conversion accepts both header forms.
- [ ] **Step 2: Run** `cd plugins/runtime/js && go test ./...` — expect FAIL.
- [ ] **Step 3: Implement** `main.go`, `api.go`, `convert.go`. Wasm-only code (host calls) stays behind the SDK; the runtime itself has no build tags beyond what the SDK needs.
- [ ] **Step 4: Run** native tests — PASS; `make jsrt` — produces `jsrt.wasm.gz` (~4–5 MB); running `make jsrt` twice gives identical bytes (`sha256sum`).
- [ ] **Step 5: Commit** `git commit -m "JS plugin runtime (goja on wasip1) and make jsrt"`

### Task 8: Host support for JS plugins

**Files:**
- Create: `pkg/engine/plugin/js.go` (`//go:embed jsrt.wasm.gz`, lazy gunzip, `compileJS(ctx, memoryMB)`)
- Modify: `pkg/engine/plugin/manager.go` (`install`/`verify`/`compile` branch on runtime; `InstallArchive` keeps `.js` files; background precompile in `Open`/`install` when a JS plugin exists)
- Modify: `pkg/engine/plugin/runtime.go` (`pool.init *loadMsg`: sent once per new instance in `get`)
- Modify: `cmd/pyro/plugin.go` `packCmd` (error hint for a missing module depends on runtime)
- Create: `pkg/engine/plugin/testdata/js/{echo,throws,syntax,loop,alloc}/` (manifest + `plugin.js`)
- Test: `pkg/engine/plugin/js_test.go`

**Interfaces:**
- Consumes: Task 1 manifest fields, Task 2 hosts/pool cap, Task 7 `jsrt.wasm.gz` and `load` op.
- Produces: JS plugins usable through the existing `Execute`, `Sign`, `Process`, `InstallDir`, `InstallArchive` with no API change. One compiled jsrt per memory limit, kept in `host.jsrt wazero.CompiledModule`.

- [ ] **Step 1: Write failing tests** in `js_test.go`:
  - `TestJSProtocolEcho`: `testdata/js/echo` (dials `echoServer`, writes body + `\n`, reads a line) → response body `echo:hi`, logs contain the plugin's `pyro.log.info` line.
  - `TestJSAuthAndProcessor`: echo dir also registers auth `js-sign` (sets `X-Sig` to hex hmac of body) and processor `js-check` (post: `test("status ok", …)`, `setVar("seen","1")`) → Sign sets the header; Process returns the var and a passing test.
  - `TestJSCapabilityDenied`: manifest network `localhost:1` → Execute error contains `may not connect to`.
  - `TestJSSyntaxErrorFailsInstall`: `testdata/js/syntax` → `InstallDir` error contains `plugin.js:` and `SyntaxError`.
  - `TestJSThrowIsAnErrorReply`: handler throws `new Error("boom")` → Execute error contains `Error: boom`.
  - `TestJSInfiniteLoopTimesOut`: `while(true){}` with a 2 s ctx deadline → error `plugin timed out`; a following call on the echo plugin still works.
  - `TestJSMemoryLimit`: `testdata/js/alloc` allocating 300 MB of arrays with default 256 MB → error; with `memoryMB: 512` → success.
  - `TestJSArchiveInstall`: zip of manifest + `plugin.js` → `InstallArchive` works.
- [ ] **Step 2: Run** `go test ./pkg/engine/plugin -run TestJS` — expect FAIL.
- [ ] **Step 3: Implement.** For `runtime: js`, `install` skips the `\0asm` check, rejects non-UTF-8 or > 4 MB scripts (`plugin.js is larger than 4 MB` / `is not valid UTF-8`), and `verify` sends `load` then `describe`. `load` errors fail the call with the JS message.
- [ ] **Step 4: Run** `go test ./pkg/engine/plugin` (and once with `-race`; note the time) — expect PASS.
- [ ] **Step 5: Commit** `git commit -m "Plugins: JavaScript plugins on the embedded runtime"`

### Task 9: Load-test warning for capped plugins

**Files:**
- Modify: `pkg/engine/plugin/manager.go` (`func (m *Manager) Lookup(kind, id string) (Info, bool)`)
- Modify: `pkg/engine/load/test.go` (`warnings()` appends `t.pluginWarnings`, computed once in `New` from `jobPlugins()` + processors and `PeakVUs(plan)`)
- Create: `pkg/engine/load/pluginwarn.go` with `func pluginCapWarnings(infos []plugin.Info, peakVUs int) []string`
- Test: `pkg/engine/load/pluginwarn_test.go`

- [ ] **Step 1: Write failing test** `TestPluginCapWarnings`: JS info with `MaxInstances: 32` and peak 100 → exactly the JS warning text from Global Constraints; peak 10 → none; wasm info with `MaxInstances: 0` → none; wasm with 4 and peak 10 → the wasm text.
- [ ] **Step 2: Run** `go test ./pkg/engine/load -run TestPluginCapWarnings` — expect FAIL.
- [ ] **Step 3: Implement.** `jobPlugins` also notes `processor` kinds from the request's processors.
- [ ] **Step 4: Run** `go test ./pkg/engine/load` — expect PASS.
- [ ] **Step 5: Commit** `git commit -m "Load tests warn when a plugin's instance cap is below peak VUs"`

### Task 10: `pyro plugin test`

**Files:**
- Create: `cmd/pyro/plugintest.go`
- Modify: `cmd/pyro/plugin.go` (add the subcommand)
- Modify: `docs/guide/cli.md`
- Test: `cmd/pyro/plugintest_test.go`

**Interfaces:**
- Consumes: `plugin.Open`, `InstallDir(dir, true)` into a temp Manager (for a directory argument) or the user's Manager (for a name; must be approved), `Execute`, `Sign`, `Process`.
- Produces: `pyro plugin test <dir|name> [--protocol id | --auth id | --processor id] [--url u] [--method m] [--body b] [--header k=v]… [--opt k=v]… [--param k=v]… [--config k=v]… [--phase pre|post] [--status n] [--response-body b] [--json] [--timeout 30s]`. With one provide the kind/id flag is optional. Exit code 1 on a call error or any failed processor test.

- [ ] **Step 1: Write failing tests** (run the cobra command in-process, capture stdout): protocol via a JS testdata echo plugin against `echoServer` prints `OK` and `echo:hi`, plus the log line; `--json` output parses with `response.body`; auth prints the signed `X-Sig` header; a processor with a failing test exits with code 1; a plugin with two provides and no kind flag errors `this plugin provides 3 features; pick one with --protocol, --auth or --processor`.
- [ ] **Step 2: Run** `go test ./cmd/pyro -run TestPluginTest` — expect FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — expect PASS.
- [ ] **Step 5: Commit** `git commit -m "pyro plugin test: run one plugin call from the terminal"`

### Task 11: `pyro plugin dev`

**Files:**
- Create: `cmd/pyro/plugindev.go`
- Modify: `cmd/pyro/plugin.go`, `docs/guide/cli.md`
- Test: `cmd/pyro/plugindev_test.go`

**Interfaces:**
- Consumes: `openPlugins`/`pluginsDir`, `InstallDir(dir, true)`.
- Produces: `pyro plugin dev <dir> [--interval 500ms] [--once]`. Fingerprint of all files in `dir` except the manifest's `module` (for wasm), dot-files and `node_modules`. Cycle: for `wasm`, run `go build` with `GOOS=wasip1 GOARCH=wasm -buildmode=c-shared -o <module> .` in `dir`; then `InstallDir`. Output lines:
  - `✓ redis 0.1.0 installed (built in 1.4s)` / `✓ redis 0.1.0 installed` (JS)
  - `  approved network: localhost:6379` on first install or when capabilities/memory change
  - `✗ build failed:` + compiler output verbatim / `✗ install failed: <err>`; keeps watching.
  - `--once` does one cycle and exits non-zero on failure (used by tests and CI).
- `func runDevCycle(dir string, m *plugin.Manager, out io.Writer) error` is the testable unit.

- [ ] **Step 1: Write failing tests:** JS plugin dir → `runDevCycle` installs and approves (`Info.Ready`); editing `plugin.js` then a second cycle changes the reply; a JS syntax error → error printed, previous version still executes; a Go plugin with a compile error (copy of `testdata/alloc` with a broken line) → `✗ build failed` with the compiler's text, earlier install intact.
- [ ] **Step 2: Run** `go test ./cmd/pyro -run TestPluginDev` — expect FAIL.
- [ ] **Step 3: Implement** (`runDevCycle` + a ticker loop calling it on fingerprint change; Ctrl-C via `cmd.Context()`).
- [ ] **Step 4: Run** — expect PASS.
- [ ] **Step 5: Commit** `git commit -m "pyro plugin dev: rebuild and reinstall on save"`

### Task 12: `pyro plugin new --lang js` and templates

**Files:**
- Modify: `cmd/pyro/plugin.go` (`newPluginCmd`: `--lang go|js`; next-step text `pyro plugin dev <dir>`)
- Create: `cmd/pyro/templates/js/{protocol,auth,processor}.js`, `cmd/pyro/templates/js/pyro.d.ts`, `README.md` template; move the Go templates next to them if they are inline today (`//go:embed templates`)
- Test: `cmd/pyro/plugin_test.go` (or new `pluginnew_test.go`)

- [ ] **Step 1: Write failing tests:** for each kind, `plugin new <tmp> --lang js --kind <k> --id x` then `runDevCycle(--once)` succeeds and `plugin test` (protocol: against `echoServer`; auth: signs; processor: passes) exits 0. The generated manifest has `runtime: js`, `module: plugin.js`.
- [ ] **Step 2: Run** — expect FAIL.
- [ ] **Step 3: Implement.** `pyro.d.ts` declares exactly the Task 7 API.
- [ ] **Step 4: Run** — expect PASS.
- [ ] **Step 5: Commit** `git commit -m "pyro plugin new --lang js"`

### Task 13: Example `plugins/examples/redis-js`

**Files:**
- Create: `plugins/examples/redis-js/{pyro-plugin.yaml,plugin.js,README.md}` (protocol `redis`: URL `redis://host:port`, body is a command line like `SET k v`, encodes RESP arrays, parses simple strings, errors (status 1, statusText `ERR`), integers, bulk strings, arrays (rendered as JSON); fields `timeoutMs`, `db`)
- Modify: `Makefile` `plugins` target (pack `redis-js` too; no build step)
- Test: `pkg/engine/plugin/integration_test.go` (`TestRedisJSExample` with an in-process fake RESP server handling `PING`, `SET`, `GET`, `LRANGE`, an error reply)

- [ ] **Step 1: Write failing test** `TestRedisJSExample`: `PING` → body `PONG`; `SET a 1` then `GET a` → `1`; `LRANGE l 0 -1` → `["x","y"]`; unknown command → status 1, statusText `ERR`, body has the error text.
- [ ] **Step 2: Run** — expect FAIL.
- [ ] **Step 3: Implement** the example.
- [ ] **Step 4: Run** `go test ./pkg/engine/plugin -run TestRedisJS` — expect PASS.
- [ ] **Step 5: Commit** `git commit -m "Example JS plugin: Redis"`

### Task 14: Docs, e2e, roadmap

**Files:**
- Modify: `docs/guide/plugins.md` (walkthrough new → dev → test → pack, Go and JS side by side; "JS or Go?" with the spec's numbers, memoryMB and maxInstances), `docs/plugins.md` (`runtime`, `limits.maxInstances`, enforced `memoryMB`, `load` op, JS API reference, `HandleOp`, dev/test), `docs/guide/cli.md` (check Tasks 10–12 lines), `docs/architecture.md` (jsrt + watch in one paragraph), `docs/roadmap.md` (phase 17 row ✅ with what shipped; phase 18 row "planned": public index, signed entries, browse/install in the app), `CHANGELOG.md`, `CLAUDE.md` (phase line; `plugins/runtime/js` in Layout; `make jsrt` in Commands)
- Create: `web/e2e/plugins-dev.spec.ts`

- [ ] **Step 1: Write the e2e test:** with `pyro serve` running (existing fixture), run `pyro plugin install <redis-js example> --yes` using the same `PYRO_PLUGINS_DIR` as the server; without reloading the page, the request Type menu offers `Redis` within 5 s.
- [ ] **Step 2: Run** `cd web && npm run e2e -- plugins-dev` — expect PASS (it depends on Tasks 4–5 and 8).
- [ ] **Step 3: Write the docs** listed above.
- [ ] **Step 4: Run** `make test` and `cd web && npm run e2e` — expect PASS.
- [ ] **Step 5: Commit** `git commit -m "Docs and e2e for plugin dev loop and JS plugins"`
