# Load Engine (implemented in Phase 3)

The load engine runs workspace requests with many concurrent virtual users (VUs),
which is the JMeter side of Pyro. It reuses the request pipeline described in
[architecture.md](architecture.md#request-pipeline) unchanged.

## Concepts
- **Virtual user (VU):** a goroutine with its own variables, cookie jar, script runtime and, optionally, its own connection pool.
- **Scenario:** a named workload: an executor, a flow and an optional data feeder.
- **Executor:** decides *how many* VUs or iterations run *when*.
- **Flow:** the ordered steps each iteration runs.

## Executors
| Executor | Model | Use for |
|---|---|---|
| `constant-vus` | closed | Fixed concurrency (`vus`, `duration`). |
| `ramping-vus` | closed | Stress tests: `stages` of `{duration, target}`. |
| `per-vu-iterations` | closed | Each VU runs N iterations, e.g. for data seeding. |
| `constant-arrival-rate` | open | Fixed RPS whatever the latency (`rate`, `timeUnit`, `preAllocatedVUs`, `maxVUs`). |
| `ramping-arrival-rate` | open | Breakpoint and spike tests at a target rate. |

Open-model executors start iterations on a schedule whatever the latency, so a slow
system doesn't slow the load down (no coordinated omission). When every allocated VU is
busy and `maxVUs` is reached, the iteration is **dropped** and counted in
`dropped_iterations`, which the dashboard and summary show. Defaults: `timeUnit: 1s`,
`preAllocatedVUs: 10`, `maxVUs` = `preAllocatedVUs`.

All executors accept `startTime` (offset from the test start), `gracefulStop` (default
30s: how long in-flight iterations may finish when the scenario ends) and, for
`ramping-vus`, `gracefulRampDown` (default 30s). `per-vu-iterations` takes `maxDuration`
(default 10m).

## Profiles
Templates in the UI that fill in a plan: **Smoke** (1–2 VUs, short), **Load** (expected peak),
**Stress** (ramp past peak), **Spike** (sudden jump and drop), **Soak** (hours at a moderate level),
**Breakpoint** (ramping arrival rate until thresholds fail).

## Plan format
```yaml
name: Checkout stress
environment: staging
maxRPS: 800                     # optional cap on requests started per second, all scenarios
scenarios:
  browse_and_buy:
    executor: ramping-vus
    stages:
      - { duration: 2m, target: 200 }
      - { duration: 5m, target: 200 }
      - { duration: 1m, target: 0 }
    gracefulRampDown: 30s
    flow:
      - request: payments/auth/login
        once: true                       # once per VU
      - group: checkout
        steps:
          - request: payments/orders/create-order
          - think: { min: 1s, max: 3s }
      - weighted:
          - { weight: 80, steps: [ { request: payments/catalog/list } ] }
          - { weight: 20, steps: [ { request: payments/catalog/search } ] }
    data: { file: data/users.csv, mode: per-vu }
  ws_listeners:
    executor: constant-vus
    vus: 500
    duration: 8m
    flow:
      - request: payments/realtime/notifications
        hold: 8m                         # keep the connection open
thresholds:
  http_req_duration{scenario:browse_and_buy}: ["p(95)<500", "p(99)<1200"]
  http_req_failed: ["rate<0.01"]
  checks: [{ threshold: "rate>0.99", abortOnFail: true }]
```

## Flow steps
| Step | Meaning |
|---|---|
| `request: <path>` | Run a workspace request (scripts, assertions and extractors included). Options: `once`, `hold` (streams), `send` (messages to send on a stream), `name` (metric tag override). |
| `think: {min,max}` / `think: 2s` | Pause, uniformly random between min and max. |
| `loop: {count \| while, steps}` | Repeat steps a fixed number of times, or while an expression is true. |
| `if: {expr, then, else}` | Branch on an expression over variables, e.g. `status == 200 && cartSize > 0`. Expressions see every variable (numeric strings compare as numbers) plus `status` and `duration` (ms) of the last response and `iteration`. |
| `weighted: [{weight, steps}]` | Choose one branch at random by weight, like JMeter's throughput controller. |
| `group: <name>` + `steps` | Named transaction. Its duration is recorded as `group_duration{group:<name>}`. |
| `once: true` | Run this step only on the VU's first iteration. |

## Script steps
For logic the flow DSL can't express, a step can run JavaScript with a `pyro.*` API:
```yaml
flow:
  - request: shop/login
  - script: |
      const list = pyro.request("shop/products", { name: "list products" });
      const items = list.json().items;
      pyro.check(list, { "has items": (r) => items.length > 0 });
      pyro.group("checkout", () => {
        const order = pyro.http.post(pyro.vars.get("baseUrl") + "/orders", { sku: items[0].sku });
        pyro.metrics.trend("order_ms").add(order.timings.total);
        if (order.status !== 201) pyro.fail("order failed: " + order.status);
      });
      pyro.sleep(1 + Math.random());
  - scriptFile: scripts/browse.js     # or keep the code in a workspace file
```

| API | What it does |
|---|---|
| `pyro.request(path, {name, vars})` | Sends a workspace request through the full pipeline (its scripts, auth, assertions, extractors) and returns `{ok, status, statusText, headers, body, json(), timings: {total, waiting}, passed, error}`. Use a literal path: requests are resolved before the test. |
| `pyro.http.get(url, opts)`, `.post(url, body, opts)`, `.put`, `.patch`, `.del`, `.head`, `.request(method, url, body, opts)` | Ad-hoc requests. Objects in `body` are sent as JSON. `opts`: `{name, headers}`. Only hosts the test was confirmed for (see below) may be called. |
| `pyro.check(value, {name: fn})` | Records each check in the `checks` rate (tagged by name); returns whether all passed. |
| `pyro.group(name, fn)` | Tags what `fn` sends with the group; records `group_duration`. |
| `pyro.sleep(seconds)` | Think time (stops promptly when the test stops). |
| `pyro.metrics.counter(name).add(n)`, `.trend(name).add(ms)`, `.rate(name).add(bool)` | Custom metrics, usable in thresholds and merged across agents. |
| `pyro.vars.get(name)`, `pyro.vars.set(name, value)` | The VU's variables (shared with request steps, `if` and `loop`). |
| `pyro.vu.id`, `pyro.vu.iteration`, `pyro.scenario`, `pyro.data` | The VU, its iteration, the scenario and the current data row. |
| `pyro.fail(message)` | Stops the script and counts the message as an error. |

Requests made from scripts are measured exactly like request steps (`http_req_*`, the
requests table) and respect `maxRPS`. Each VU has its own JavaScript runtime, and scripts
are compiled once per test, so a script step costs little more than the requests it makes.
A script that throws is counted as an error and the VU carries on. An endless loop is
interrupted when the test stops.

**Hosts.** `pyro.http` may only call hosts covered by the load test's host confirmation:
hosts found in request URLs, literal URLs in scripts (`pyro.http.get("https://…")`), the
plan's `hosts:` list, `loadAllowHosts` and loopback. URLs built at run time to other hosts are
refused; list those hosts under `hosts:` so they're confirmed up front.

## Data feeders
- File types: CSV (header row = variable names) and JSON (array of objects).
- `mode: shared` (every VU reads the next row from a global cursor), `per-vu` (each VU gets its own row, chosen by VU id), `random`.
- `onEnd: recycle | stop` decides what happens when the rows run out.
- Plugins of kind `datasource` can add more feeders.

## Metrics
Samples are written to per-VU ring buffers. Every second an aggregator merges them into
HDR histograms keyed by metric name and tags (`scenario`, `request`, `group`, `status`, `method`).

| Metric | Type | Notes |
|---|---|---|
| `http_reqs` | counter | |
| `http_req_duration` | trend | Total time. Phases: `http_req_blocked` (DNS lookup), `_connecting`, `_tls`, `_sending`, `_waiting` (TTFB), `_receiving`. |
| `http_req_failed` | rate | Transport error, or HTTP status ≥ 400. |
| `ws_sessions` / `ws_errors` | counter | Opened sessions; connect, send or connection-lost failures. |
| `ws_connecting` / `ws_session_duration` | trend | |
| `ws_msgs_sent` / `ws_msgs_received` | counter | |
| `dropped_iterations` | counter | Arrival-rate iterations that found no free VU. |
| `sio_event_rtt` | trend | Socket.IO ack round trip. |
| `checks` | rate | Assertion pass rate. |
| `iterations` / `iteration_duration` | counter / trend | |
| `group_duration` | trend | Per `group`. |
| `vus` / `vus_max` | gauge | |
| `data_sent` / `data_received` | counter | Bytes. |
| custom (planned) | any | `pyro.metrics.*` from scripts or plugins. |

### Load generator health
Every tick also records the load generator's own CPU use (process CPU time over the time
GOMAXPROCS cores had available, so 100% means every usable core was busy) and memory; for
distributed tests each agent reports its own. When a generator stays at 90% CPU or more for
3 ticks, the result carries a warning (dashboard, CLI summary, HTML report): latencies then
include time Pyro spent waiting for CPU, so use fewer VUs per machine, lighter scripts or
more agents. The dashboard shows a CPU tile while a local test runs and a CPU column in the
Agents table. CPU isn't measured on platforms other than Unix-likes and Windows.

Results: the live dashboard (event WebSocket, one tick per second), saved runs in SQLite
(summary plus every tick), `pyro load --json` / `--html`.

### Comparing runs
A load test's **Runs** tab keeps every run. Tick two and press **Compare** to see them side by
side (the earlier one is the baseline; **Swap** reverses it): a summary of requests per
second, failure rate and latency percentiles with the change between runs, each threshold's
verdict in both, the charts overlaid (baseline violet, candidate teal; a table view lists
the same values per second), and a per-request table with p95 for both runs. Changes under
2% read as "no change"; for latency and failures lower is better, for requests per second
higher is better.

### Live outputs
`--out` / `-o` (repeatable) streams every tick to other systems, on `pyro load` and on
`pyro serve` (for every test started from the UI):

| Output | Example | Notes |
|---|---|---|
| InfluxDB 1.x | `-o influxdb=http://localhost:8086/write?db=pyro` | A bare `http://host:8086` means `/write?db=pyro`. |
| InfluxDB 2.x | `-o influxdb=http://localhost:8086/api/v2/write?org=acme&bucket=load` | Token from `$PYRO_INFLUXDB_TOKEN`. |
| Prometheus | `-o prometheus=http://localhost:9090/api/v1/write` | Remote write 1.0. Start Prometheus with `--web.enable-remote-write-receiver` (or use Mimir, Thanos, VictoriaMetrics…). |
| JSON | `-o json=ticks.ndjson` | One object per tick: the tick plus the per-request table so far. |
| CSV | `-o csv=ticks.csv` | One row per tick: VUs, requests, rates, latency percentiles, failing thresholds. |

Series (every one labelled `test` and `run`; durations in seconds):
`pyro_vus`, `pyro_vus_max`, `pyro_http_reqs_total`, `pyro_http_reqs_per_second`,
`pyro_http_req_failed_ratio`, `pyro_http_req_duration_seconds{quantile="0.5|0.9|0.95|0.99|1"}`
(1 is the max), `pyro_http_req_duration_seconds_avg`, `pyro_iterations_per_second`,
`pyro_dropped_iterations`, `pyro_data_received_bytes_per_second`,
`pyro_ws_msgs_received_per_second`, `pyro_threshold_passing{threshold}` (1/0), per request
`pyro_request_reqs_total`, `pyro_request_failed_total` and
`pyro_request_duration_seconds{quantile="0.95|0.99"}` (labels `request`, `scenario`,
`method`), and for distributed tests `pyro_agent_vus` and `pyro_agent_reqs_per_second`
(label `agent`), `pyro_generator_cpu_ratio`, `pyro_generator_memory_bytes` and
`pyro_agent_cpu_ratio`. Percentiles are per tick; the per-request ones are cumulative.

Writes happen off the test's own goroutines: a slow or unreachable backend never slows the
test. Failed writes and dropped ticks are reported when the test ends. With `pyro serve`,
file outputs are rewritten by each test.

Accuracy: against a target that sleeps exactly 50 ms, 5000 VUs measured p95 52 ms and p99
53 ms (the extra is the target's own timer and scheduling).

## Thresholds
- Syntax: `metric{tag:value}: ["<agg><op><value>", …]`, where `agg` is one of `avg`, `min`, `max`, `med`, `p(N)`, `rate` or `count`.
- Evaluated live every tick (for the dashboard and `abortOnFail`) and again at the end.
- **The verdict uses the final values**: a p95 that spiked during warm-up and recovered
  passes. A threshold that failed at some point is marked "failed earlier" for review.
- A counter with no samples counts as 0 (so `ws_errors: ["count<1"]` passes when there were
  none); a trend or rate with no samples shows "no data" and doesn't fail.
- `abortOnFail: true` stops the test early (status `aborted`), after an optional `delayAbortEval`.
- `pyro load` exits non-zero unless the status is `passed`, which is what CI checks.

## Safety
- Pyro asks for confirmation before load testing any host that is not loopback or in the workspace allow-list (`pyro.yaml: loadAllowHosts`). `--yes` skips the prompt in CI.
- The UI asks the same way, inline, listing the hosts.
- VU cap: a plan needing more than 50,000 VUs (or `--max-vus`) is refused.
- Requests-per-second cap: `maxRPS` in the plan, `--max-rps` on `pyro load`, and
  `loadMaxRPS` in `pyro.yaml` (a workspace-wide ceiling); the lowest applies. Request starts
  are spaced evenly across every VU (WebSocket/Socket.IO connects count too); the wait comes
  before sending, so it isn't counted in `http_req_duration`. Distributed tests split the cap
  between agents. VUs still waiting for a slot when their scenario ends give up rather than
  sending late. With arrival-rate executors, a cap below the arrival rate shows up as busy
  VUs and dropped iterations.
- One load test at a time per `pyro serve`.
- Stop is graceful: VUs stop taking new iterations, in-flight requests finish within `gracefulStop`, and then they are cancelled. A second stop (Ctrl+C twice, or "Abort now") cancels immediately.

## Distributed tests
Run one test from several machines when one load generator isn't enough. Start an agent on
each machine, then point the controller at them:

```sh
pyro agent --token s3cret                       # on each load machine (port 7979)
pyro load checkout-stress --agent 10.0.0.5:7979 --agent 10.0.0.6:7979 --agent-token s3cret
pyro serve --agent 10.0.0.5:7979,10.0.0.6:7979 --agent-token s3cret   # UI: "Run on: N agents"
```
Agents behind NAT or firewalls can connect out instead ([ADR 0007](adr/0007-agents-connect-out.md)):
```sh
pyro agent --connect controller.example.com:7980 --token s3cret      # on each load machine
pyro load checkout-stress --agent-listen :7980 --wait-agents 3 --agent-token s3cret
pyro serve --agent-listen :7980 --agent-token s3cret                 # UI lists connected agents
```
The agent listener is separate from the UI, which stays on loopback. Connected agents wait
for a test, reconnect after each one, and retry with backoff if the controller is away.
Both kinds of agents can share a test.

`PYRO_AGENT_TOKEN` can replace `--token`/`--agent-token`. Agents with `--tls-cert/--tls-key`
are addressed as `https://host:port` (`--agent-insecure` skips verification).

How it works ([ADR 0006](adr/0006-agents-over-websocket.md)):
- **Splitting.** Each agent gets a segment of the plan. VU counts (`vus`, `startVUs`, stage
  targets, `preAllocatedVUs`, `maxVUs`) are split as evenly as whole numbers allow, so they
  add up to the original; arrival rates are divided exactly. A scenario that leaves an agent
  with no VUs (2 VUs over 3 agents) is skipped on that agent. VU ids don't overlap across
  agents.
- **What agents receive.** The plan segment, the requests it uses with their collection
  settings, the environment, data rows and body files. Agents don't need the workspace.
  Data in `shared` mode is split so each row is used by one agent; `per-vu` and `random`
  data go to every agent whole. Body files whose path contains `{{variables}}` can't be
  sent; requests using them fail on the agent.
- **Merging.** Agents send per-series deltas (counts, sums, HDR bucket counts) every tick.
  The controller merges them, so percentiles, per-request tables and thresholds cover
  every sample exactly. Thresholds (and `abortOnFail`) are evaluated on the controller only.
  Live charts trail the agents by up to one tick.
- **Plugins.** Plugin request and auth types must be installed and approved on every agent;
  the controller checks before starting.
- **Requests per second.** A cap (`maxRPS`, `--max-rps`, `loadMaxRPS`) is split evenly
  between agents.
- **Failures.** Before starting, the controller checks every agent (token, protocol
  version, busy). If an agent disconnects or stops reporting for 30 s during the test, the
  others are stopped and the test fails with `agent <name> failed: …`. Stopping or
  aborting the test stops or aborts every agent.
- **Results** list each agent's segment, requests and peak VUs (`agents` in the JSON, an
  Agents table in the dashboard and the HTML report).

Agents run one test at a time and enforce their own `--max-vus`. The host confirmation
(see Safety) happens on the controller. Agents trust any controller with the token: keep
the token secret and the port reachable only from controllers.
