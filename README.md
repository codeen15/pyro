# Pyro

An API client (REST, WebSocket, Socket.IO, Postman import) and load-testing
tool (stress, spike, soak, concurrency) with WASM plugins, in a single binary
that serves a web UI.

Design docs: [`docs/`](docs/) (start with [architecture](docs/architecture.md) and [roadmap](docs/roadmap.md)).

## Build

Requires Go 1.23+ and Node 20+.

```sh
make build          # builds the UI, embeds it, writes bin/pyro
./bin/pyro serve --open
```

On first run with no workspace, Pyro creates `~/.pyro/workspace` with an
example collection. To use a workspace in a project directory:

```sh
pyro init my-api --sample
pyro import -w my-api ~/Downloads/my-collection.postman_collection.json
pyro serve -w my-api
pyro run my-collection -w my-api -e staging --junit results.xml   # CI
```

`pyro serve` binds to 127.0.0.1 and prints a one-time link carrying a session
token; API calls without it are rejected.

## Develop

```sh
make dev-api   # terminal 1: engine on :7878 without token
make dev-ui    # terminal 2: Vite on :5173 with hot reload, proxies /api
make test      # go vet + go test + UI typecheck
```

## Layout

| Path | What |
|---|---|
| `cmd/pyro` | CLI (`serve`, `init`, `import`, `run`, `version`) |
| `pkg/engine/model` | Protocol-agnostic data model shared by all modes |
| `pkg/engine` | Send pipeline, scripts (pm.*), assertions, runner, reports, HTTP/WebSocket/Socket.IO, Postman importer |
| `internal/store` | Workspace persistence as git-friendly YAML |
| `internal/history` | Request history (SQLite) |
| `internal/server` | REST API under `/api/v1`, embedded SPA |
| `web` | React + TypeScript UI (Vite), embedded via `web/embed.go` |

## Workspace format

```
pyro.yaml
collections/<collection>/collection.yaml
collections/<collection>/<folder>/<request>.req.yaml
environments/<name>.env.yaml
```
