# Contributing to Pyro

Thanks for helping. Issues and pull requests are welcome; for anything large, open an issue
first so we can agree on the approach.

## Setup
Requires Go (the version in `go.mod`) and Node 20+. No CGO, ever: every dependency must be
pure Go, and builds use `CGO_ENABLED=0`.

```sh
make build                 # UI + binary → bin/pyro
make dev-api               # engine on :7878 (no token)
make dev-ui                # UI on :5173 with hot reload
```

## Before you open a pull request
```sh
gofmt -l cmd internal pkg plugins scripts   # must print nothing
make test                                   # go vet, go test, plugin SDK tests, UI typecheck
go test -race ./internal/... ./pkg/...      # the plugin package takes ~100 s under -race
make build && (cd web && npm run e2e)       # browser tests (npx playwright install chromium once)
```
CI runs all of these, plus `go test` on macOS and Windows, the Socket.IO conformance test
against the official Node server, and a release dry run.

## How the code is organised
[`CLAUDE.md`](CLAUDE.md) is the short map (layout, conventions, commands);
[`docs/architecture.md`](docs/architecture.md) explains the design and
[`docs/`](docs/) has one document per area. Decisions that are expensive to reverse are
recorded in [`docs/adr/`](docs/adr/); add one when you make such a decision.

A few rules that reviews check:
- `pkg/engine` is a library: it never imports `internal/...`.
- Tests use in-process servers (`httptest`, `t.TempDir()` workspaces) and no external network.
- Model types carry both `yaml` and `json` tags (lowerCamel, `omitempty` for optional fields).
- UI changes follow [`DESIGN.md`](DESIGN.md): heat colours only on measured data, the flame
  accent for chrome, text never in data colours. Check phone width (390 px) for horizontal
  overflow.
- User-facing behaviour changes update the matching doc in `docs/`.

## Writing plugins
See [`docs/plugins.md`](docs/plugins.md); `pyro plugin new` scaffolds one.

## Commit messages
Imperative subject (≤ 72 characters), then a body explaining what and why.

## License
By contributing you agree that your contributions are licensed under the
[Apache License 2.0](LICENSE).
