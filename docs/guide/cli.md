# Command line

`pyro <command> --help` prints the same information. Commands that work on a workspace take
`-w <dir>`. Without it they use the current folder if it has `pyro.yaml`, otherwise
`~/.pyro/workspace`.

## pyro serve
Start the engine and the web UI.

```sh
pyro serve --open
pyro serve -w my-api -p 0
```

| Flag | Meaning |
|---|---|
| `-w, --workspace` | Workspace folder |
| `-p, --port` | Port (default 7878; `0` picks a free port) |
| `--host` | Address to bind (default `127.0.0.1`) |
| `--open` | Open the UI in a browser |
| `--no-token` | Disable the session token (loopback only; for development) |
| `-o, --out` | Stream every load test's live metrics: `influxdb=URL`, `prometheus=URL`, `json=FILE`, `csv=FILE` |
| `--agent` | Load agents for distributed tests (`host:port`, repeat or comma-separate) |
| `--agent-listen` | Accept agents that connect out, e.g. `:7980` |
| `--agent-token` | Token for the agents (default `$PYRO_AGENT_TOKEN`) |
| `--agent-insecure` | Skip TLS verification for `https` agents |
| `--team` | Run the multi-user [team server](team-server.md) instead of a single workspace |
| `--data` | With `--team`: data directory (default `~/.pyro/team`) |
| `--database` | With `--team`: PostgreSQL URL (default: SQLite in the data directory) |
| `--env-file` | With `--team`: read settings from this file (default: `.env` in the current folder) |
| `--config` | With `--team`: settings file (default `<data>/server.yaml`) |
| `--public-url` | With `--team`: the address people open, e.g. `https://pyro.example.com` |
| `--signup`, `--signup-domain` | With `--team`: who may create an account (`invite-only`, `open`, `domains`) |
| `--tls-cert`, `--tls-key` | With `--team`: serve HTTPS |
| `--max-loads` | With `--team`: load tests that may run at once across all projects (default 2) |

```sh
pyro serve --team --host 0.0.0.0 --public-url https://pyro.example.com --signup domains --signup-domain example.com
```

## pyro admin
Manage a team server's accounts (on the server machine; works while it runs). All take
`--data`, `--database` and `--env-file`, like `serve --team`.

| Command | Does |
|---|---|
| `create-user <email>` | Create an account; asks for the password (`--name`, `--admin`) |
| `reset-password <email>` | Set a new password and sign the account out everywhere |
| `set-admin <email>` | Make the account a server administrator (`--revoke` to undo) |
| `users` | List accounts |

## pyro init
Create a workspace.

```sh
pyro init my-api --sample
```

| Flag | Meaning |
|---|---|
| `--name` | Workspace name (defaults to the folder name) |
| `--sample` | Add an example collection and environment |

## pyro import
Import Postman, OpenAPI/Swagger, HAR, JMeter or cURL. The format is detected; `-` reads
standard input.

```sh
pyro import shop.postman_collection.json staging.postman_environment.json
pbpaste | pyro import - --into shop
```

| Flag | Meaning |
|---|---|
| `--into` | Add to an existing collection or folder |
| `--report` | Write the import report(s) as JSON to a file |

## pyro export
Export a collection, or an environment, as Postman v2.1 JSON.

```sh
pyro export shop -o shop.postman_collection.json
pyro export -e staging -o staging.postman_environment.json
```

| Flag | Meaning |
|---|---|
| `-o, --output` | File to write (default: standard output) |
| `-e, --environment` | Export this environment instead |

## pyro run
Run requests as a functional test suite. Exits 1 if any request or test fails.

```sh
pyro run payments -e staging
pyro run payments/orders -d data/users.csv --junit results.xml
```

| Flag | Meaning |
|---|---|
| `-e, --environment` | Environment name |
| `-d, --data` | CSV or JSON file; one iteration per row |
| `-n, --iterations` | Iterations (default: data rows, or 1) |
| `--delay` | Wait between requests, e.g. `200ms` |
| `--think` | Wait each request's think time, as a load test would |
| `--bail` | Stop at the first failed request |
| `--timeout` | Per-request timeout (default 30s) |
| `--junit`, `--json` | Write a JUnit XML or JSON report |
| `--insecure` | Skip TLS certificate verification |
| `-q, --quiet` | Only print failures and the summary |

## pyro load
Run a load test (a name from `loadtests/`, or a plan file). Exits non-zero unless every
threshold passes.

```sh
pyro load checkout-stress -e staging --html report.html
pyro load checkout-stress --agent 10.0.0.5:7979 --agent-token s3cret
```

| Flag | Meaning |
|---|---|
| `-e, --environment` | Environment (overrides the plan's) |
| `--html`, `--json` | Write an HTML report or the full JSON result |
| `-o, --out` | Stream live metrics (repeatable) |
| `--max-rps` | Cap requests per second across all VUs |
| `--max-vus` | Refuse plans needing more VUs (default 50000) |
| `-y, --yes` | Don't ask before targeting non-local hosts |
| `--insecure` | Skip TLS certificate verification |
| `-q, --quiet` | No live progress line |
| `--agent`, `--agent-listen`, `--agent-token`, `--agent-insecure` | Distributed testing ([Distributed](distributed.md)) |
| `--wait-agents`, `--wait-timeout` | With `--agent-listen`: how many agents to wait for, and for how long |

## pyro agent
Run a load agent for distributed tests.

```sh
pyro agent --token s3cret
pyro agent --connect https://controller.example.com:7980 --token s3cret
```

| Flag | Meaning |
|---|---|
| `--token` | Token controllers must present (default `$PYRO_AGENT_TOKEN`, else random and printed) |
| `-p, --port`, `--host` | Where to listen (default `0.0.0.0:7979`) |
| `--connect` | Dial out to a controller instead of listening |
| `--tls-cert`, `--tls-key` | Serve TLS |
| `--insecure` | With `--connect`: skip TLS verification |
| `--name` | Name shown in results (default: host name) |
| `--max-vus` | Refuse shares needing more VUs (default 50000) |

## pyro mock
Serve a collection's saved examples as a mock API.

```sh
pyro mock shop --port 8090 --delay 200ms
```

| Flag | Meaning |
|---|---|
| `-p, --port`, `--host` | Where to listen (default `127.0.0.1:8090`) |
| `--delay` | Wait before each response |
| `-q, --quiet` | Don't log requests |

## pyro docs
Write an HTML API reference for a collection: `pyro docs shop -o shop-api.html`.

## pyro plugin
Manage plugins ([Plugins](plugins.md)).

| Command | Does |
|---|---|
| `install <dir \| archive \| name[@version]>` | Install (`--yes` approves, `--index` sets the index URL) |
| `list` | List installed plugins |
| `approve <name>` | Review and approve a plugin's permissions |
| `enable <name>`, `disable <name>` | Turn a plugin on or off |
| `remove <name>` | Uninstall |
| `search [query]` | Search the plugin index |
| `new <dir> --kind protocol\|auth\|processor --id <id>` | Create a plugin from a template (`--sdk` points at a local SDK copy) |
| `pack <dir>` | Package a built plugin as `.pyro-plugin` and print its sha256 |

## pyro version
Print the version and build information.

## Environment variables

| Variable | Used for |
|---|---|
| `PYRO_AGENT_TOKEN` | The agent token (controller and agents) |
| `PYRO_SMTP_PASSWORD` | Team server: password for the `smtp` settings |
| `PYRO_DATABASE_URL`, `PYRO_DATA_DIR`, `PYRO_HOST`, `PYRO_PORT`, `PYRO_PUBLIC_URL`, … | Team server settings; see [Team server](team-server.md#settings-from-env-and-the-environment) |
| `PYRO_INFLUXDB_TOKEN` | InfluxDB 2.x output token |
| `PYRO_PLUGIN_INDEX` | Plugin index URL |
| `PYRO_PLUGINS_DIR` | Where plugins are installed (default `~/.pyro/plugins`) |
