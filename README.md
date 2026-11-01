# Pyro

An API client (REST, WebSocket, Socket.IO; import from Postman, OpenAPI, HAR and
cURL, export to Postman) and load-testing tool (stress, spike, soak, concurrency;
distributed across agents) with WASM plugins, in a single binary that serves a web UI.

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
pyro import -w my-api ~/Downloads/my-collection.postman_collection.json   # or openapi.yaml, a .har, cURL
pyro serve -w my-api
pyro run my-collection -w my-api -e staging --junit results.xml   # CI
pyro load checkout-stress -w my-api -e staging --html report.html  # load test
pyro load checkout-stress -w my-api --agent 10.0.0.5:7979 --agent 10.0.0.6:7979 --agent-token s3cret  # distributed (pyro agent on each)
pyro load checkout-stress -w my-api -o prometheus=http://localhost:9090/api/v1/write  # stream metrics
pyro export my-collection -w my-api -o my-collection.postman_collection.json
make plugins && pyro plugin install plugins/dist/tcp-0.1.0.pyro-plugin --yes  # raw TCP requests
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
| `cmd/pyro` | CLI (`serve`, `init`, `import`, `export`, `run`, `load`, `agent`, `plugin`, `version`) |
| `pkg/engine/model` | Protocol-agnostic data model shared by all modes |
| `pkg/engine` | Send pipeline, scripts (pm.*), assertions, runner, load engine, distributed agents, metrics and live outputs, reports, HTTP/WebSocket/Socket.IO, importers (Postman, OpenAPI, HAR, cURL), Postman exporter |
| `internal/store` | Workspace persistence as git-friendly YAML |
| `internal/history` | Request history (SQLite) |
| `internal/server` | REST API under `/api/v1`, embedded SPA |
| `web` | React + TypeScript UI (Vite), embedded via `web/embed.go` |
| `plugins` | Go plugin SDK and example plugins (raw TCP, HMAC auth) |

## Workspace format

```
pyro.yaml
collections/<collection>/collection.yaml
collections/<collection>/<folder>/<request>.req.yaml
environments/<name>.env.yaml
```
