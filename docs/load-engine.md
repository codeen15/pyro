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
| `pyro_cpu` / `pyro_mem` (planned) | gauge | The load generator's own health. |
| custom (planned) | any | `pyro.metrics.*` from scripts or plugins. |

Outputs: the live dashboard (event WebSocket, one tick per second), saved runs in SQLite
(summary plus every tick), `pyro load --json` / `--html`. Planned: an NDJSON raw-sample
file and plugin sinks (Prometheus remote-write, InfluxDB).

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
- VU cap: a plan needing more than 50,000 VUs (or `--max-vus`) is refused. A global RPS cap is planned.
- One load test at a time per `pyro serve`.
- Stop is graceful: VUs stop taking new iterations, in-flight requests finish within `gracefulStop`, and then they are cancelled. A second stop (Ctrl+C twice, or "Abort now") cancels immediately.

## Distributed mode (Phase 5)
`pyro agent --controller host:port` registers a node over gRPC. The controller splits
VUs and arrival rates in proportion to agent capacity, streams plans and data slices,
and merges the agents' HDR histograms, which is exact because they can be merged.
