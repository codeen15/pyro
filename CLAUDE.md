# Pyro

Pyro is an API client (REST, WebSocket, Socket.IO, Postman import) and load-testing
tool (stress, spike, soak, concurrency) with WASM plugins. A single Go binary runs the
engine and serves a React web UI on localhost. **Current phase: 4 done → Phase 5 (scale & polish) next**
(see `docs/roadmap.md`). Product context: `PRODUCT.md`; visual system: `DESIGN.md`.

## Layout
- `cmd/pyro`: CLI (cobra): `serve`, `init`, `import`, `run`, `load`, `agent`, `plugin`, `version`
- `pkg/engine/...`: the engine, a library with no dependency on server/CLI/UI. `model/` is the shared data model;
  `engine.go` is the send pipeline, `session.go` opens WS/SIO sessions; `vars/`, `auth/`, `check/` (assertions,
  extractors), `script/` (goja + pm.* shim, vendored chai), `runner/`, `report/` (JUnit, JSON, load HTML),
  `load/` (plans, executors, VUs, thresholds, segments and workers for distributed runs), `metrics/` (HDR histograms,
  deltas), `agent/` (distributed load agents), `plugin/` (wazero host), `importer/postman/`,
  `protocol/{httpdriver,wsdriver,sioclient}/`.
- `internal/store`: workspace persistence (YAML files, path safety, atomic writes, `Import`)
- `internal/history`: request history and saved load runs in SQLite (`~/.pyro/pyro.db`)
- `internal/server`: REST API `/api/v1` (chi), token auth, embedded SPA
- `internal/version`: build metadata (set via ldflags)
- `web/`: React + TS + Vite UI; `web/embed.go` embeds `web/dist`
- `plugins/sdk/go`: Go plugin SDK (separate module); `plugins/examples/{tcp,hmac}`: example plugins (separate modules)
- `docs/`: design docs (index below)

## Commands
```sh
make build        # UI build + Go binary → bin/pyro
make test         # go vet, go test ./..., UI typecheck
make dev-api      # engine on :7878, no token
make dev-ui       # Vite on :5173, proxies /api
./bin/pyro serve --open
./bin/pyro init <dir> --sample
```
`go test ./...` works without building the UI (`web/dist/.gitkeep` keeps the embed valid).

## Conventions
- `pkg/engine` must never import `internal/...`.
- Request paths are relative to `collections/`, slash-separated, with no extension (`payments/orders/create`). All request kinds use `.req.yaml`; `kind` picks the driver.
- Model types carry both `yaml` and `json` tags (lowerCamel, `omitempty` for optional fields).
- Store errors are `store.ErrNotFound`, `ErrInvalidPath` and `ErrExists`; the server maps them to 404/400/409.
- Tests use in-process servers (`httptest`, `t.TempDir()` workspaces); no network. Socket.IO conformance against the
  real Node server: `pkg/engine/protocol/sioclient/testdata/server.js` + `PYRO_SIO_URL`.
- Server pushes live data over `/api/v1/events` (topics `session:<id>`, `run:<id>`); clients subscribe, then fetch
  state once and merge.
- No CGO: dependencies must be pure Go. Builds use `CGO_ENABLED=0`.
- Plugin tests build the example plugins with `GOOS=wasip1` (needs only the Go toolchain); the plugin package under
  `-race` takes ~100 s because wazero compiles slowly with race instrumentation.
- UI: follow `DESIGN.md` (Pyrometry: heat colours only on measured data, flame accent for chrome). Tokens live in
  `web/src/styles.css`; heat thresholds in `web/src/heat.ts`. The impeccable design hook checks UI edits.
- Charts: `web/src/components/LineChart.tsx` (and the Go report) follow the dataviz rules: no dual axes, text never
  in data colours (heat shows as a swatch beside neutral text), hover readout + table view, colours `--viz-*`
  validated with the dataviz palette script against `--iron-0`.

## Docs
- `docs/PRD.md`: goals, users, non-goals
- `docs/architecture.md`: components, pipeline, tech choices
- `docs/data-model.md`: workspace and YAML formats, variable scopes, auth inheritance
- `docs/api.md`: REST endpoints, event WebSocket, token auth
- `docs/load-engine.md`: executors, flow DSL, metrics, thresholds
- `docs/plugins.md`: plugin kinds, ABI v1, manifest, security
- `docs/postman-import.md`: Postman mapping, `pm.*` support, import report
- `docs/roadmap.md`: phases, exit criteria, risks
- `docs/adr/`: architecture decision records
