# Pyro

Pyro is an API client (REST, WebSocket, Socket.IO, Postman import) and load-testing
tool (stress, spike, soak, concurrency) with WASM plugins. A single Go binary runs the
engine and serves a React web UI on localhost. **Current phase: 12 (think time, pre/post processors) done; next: push, check CI, tag v0.1.0**
(see `docs/roadmap.md`). Product context: `PRODUCT.md`; visual system: `DESIGN.md`.

## Layout
- `cmd/pyro`: CLI (cobra): `serve`, `init`, `import`, `export`, `run`, `load`, `agent`, `mock`, `docs`, `plugin`, `version`
- `pkg/engine/...`: the engine, a library with no dependency on server/CLI/UI. `model/` is the shared data model;
  `engine.go` is the send pipeline (pre-processors → resolve → auth → execute → post-processors), `session.go` opens WS/SIO sessions; `vars/`, `auth/`, `check/` (assertions,
  extractors incl. boundary/XPath), `process/` (timers, set-variables and counter processors), `script/` (goja + pm.* shim, vendored chai), `runner/`, `report/` (JUnit, JSON, load HTML),
  `load/` (plans, executors, VUs, thresholds, script steps (`scripting.go`, goja with `pyro.*`), segments and workers for distributed runs), `metrics/` (HDR histograms,
  deltas), `agent/` (distributed load agents), `output/` (InfluxDB, Prometheus, NDJSON/CSV live outputs), `plugin/` (wazero host), `importer/{postman,openapi,har,curl,jmeter,auto}/`, `exporter/postman/`, `docs/` (HTML API reference), `mock/` (mock server from examples),
  `protocol/{httpdriver,wsdriver,sioclient}/`.
- `internal/store`: workspace persistence (YAML files, path safety, atomic writes, `Import`)
- `internal/history`: request history and saved load runs in SQLite (`~/.pyro/pyro.db`)
- `internal/server`: REST API `/api/v1` (chi), token auth, embedded SPA
- `internal/version`: build metadata (set via ldflags)
- `web/`: React + TS + Vite UI; `web/embed.go` embeds `web/dist`
- `plugins/sdk/go`: Go plugin SDK (separate module); `plugins/examples/{tcp,hmac}`: example plugins (separate modules)
- `docs/`: design docs (index below)
- `scripts/dist`: release builder (`make dist`); `.github/workflows`: CI and tag-triggered draft releases
- `web/e2e`: Playwright tests against the real binary (`e2e/server.mjs` starts a mock API and `pyro serve`)

## Commands
```sh
make build        # UI build + Go binary → bin/pyro
make test         # go vet, go test ./..., UI typecheck
make dev-api      # engine on :7878, no token
make dev-ui       # Vite on :5173, proxies /api
./bin/pyro serve --open
./bin/pyro init <dir> --sample
make dist         # release archives + SHA256SUMS in dist/
cd web && npm run e2e   # browser tests (needs bin/pyro and npx playwright install chromium)
```
`go test ./...` works without building the UI (`web/dist/.gitkeep` keeps the embed valid).

## Conventions
- `pkg/engine` must never import `internal/...`.
- Request paths are relative to `collections/`, slash-separated, with no extension (`payments/orders/create`). All request kinds use `.req.yaml`; `kind` picks the driver.
- Model types carry both `yaml` and `json` tags (lowerCamel, `omitempty` for optional fields).
- Store errors are `store.ErrNotFound`, `ErrInvalidPath` and `ErrExists`; the server maps them to 404/400/409.
- Tests use in-process servers (`httptest`, `t.TempDir()` workspaces); no network. Socket.IO conformance against the
  real Node server: `pkg/engine/protocol/sioclient/testdata/server.js` + `PYRO_SIO_URL` (also `PYRO_SIO_POLLING_URL`
  for a polling-only server and `PYRO_SIO_FASTPING_URL` for keepalive; see the CI workflow).
- Killing background processes: use ports (`fuser -k PORT/tcp`) or `pgrep -f "pat[t]ern"`; a bare `pkill -f pattern`
  matches the shell running it.
- Server pushes live data over `/api/v1/events` (topics `session:<id>`, `run:<id>`, `load:<id>`); clients subscribe,
  then fetch state once and merge.
- Distributed load tests: agents send metric deltas (`metrics.SeriesDelta`), never summaries, so the controller's
  percentiles stay exact. Agent tests run in-process (`httptest` agents); `pkg/engine/load` tests use an in-memory worker.
- No CGO: dependencies must be pure Go. Builds use `CGO_ENABLED=0`.
- Security boundaries (see SECURITY.md): body files stay inside the workspace (`httpdriver.ResolveFile`); the server
  rejects non-loopback Host headers unless `AnyHost`; plugin index URLs must be https except on loopback.
- Plugin tests build the example plugins with `GOOS=wasip1` (needs only the Go toolchain); the plugin package under
  `-race` takes ~100 s because wazero compiles slowly with race instrumentation.
- UI: Ant Design 6 (`antd`, `@ant-design/icons`); follow `DESIGN.md` (antd components first, brand red `#cf1920`
  as `colorPrimary` in `web/src/theme.tsx`, heat swatches only on measured data). Use `App.useApp()` for
  message/modal, never alert/confirm/prompt. Icons via `web/src/icons.tsx`. Custom CSS tokens live in
  `web/src/styles.css` (light in `:root`, dark under `:root[data-theme="dark"]`; every new colour needs both);
  heat thresholds in `web/src/heat.ts`. The impeccable design hook checks UI edits. e2e helper `pickOption`
  (`web/e2e/fixtures.ts`) drives antd Selects.
- Charts: `web/src/components/LineChart.tsx` (and the Go report) follow the dataviz rules: no dual axes, text never
  in data colours (heat shows as a swatch beside neutral text), hover readout + table view, colours `--viz-*`
  validated with the dataviz palette script against the card surface (`--iron-1`).

## Docs
- `docs/PRD.md`: goals, users, non-goals
- `docs/architecture.md`: components, pipeline, tech choices
- `docs/data-model.md`: workspace and YAML formats, variable scopes, auth inheritance
- `docs/api.md`: REST endpoints, event WebSocket, token auth
- `docs/load-engine.md`: executors, flow DSL, metrics, thresholds
- `docs/plugins.md`: plugin kinds, ABI v1, manifest, security
- `docs/postman-import.md`: Postman mapping, `pm.*` support, import report
- `docs/docs-and-mocks.md`: collection settings UI, generated API docs, mock servers
- `docs/import-export.md`: OpenAPI/Swagger, HAR and cURL import; import into a collection; Postman export
- `docs/roadmap.md`: phases, exit criteria, risks
- `docs/releasing.md`: tagging, the release workflow, archive contents
- `SECURITY.md`: threat model by boundary; `CONTRIBUTING.md`: checks before a PR
- `docs/adr/`: architecture decision records
