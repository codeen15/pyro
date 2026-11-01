# Load Engine (Phase 3)

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

Open-model executors start iterations on a schedule. Latency is measured from the
**intended** start time, which avoids coordinated omission.

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
| `request: <path>` | Run a workspace request. Options: `once`, `hold` (streams), `send` (messages to send on a stream), `name` (metric tag override). |
| `think: {min,max}` / `think: 2s` | Pause, uniformly random between min and max. |
| `loop: {count \| while, steps}` | Repeat steps a fixed number of times, or while an expression is true. |
| `if: {expr, then, else}` | Branch on an expression over variables, e.g. `status == 200 && cartSize > 0`. |
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
| `http_req_duration` | trend | Total time, plus `_blocked`, `_dns`, `_connecting`, `_tls`, `_sending`, `_waiting` (TTFB) and `_receiving`. |
| `http_req_failed` | rate | Transport error, or a failed `status` assertion. |
| `ws_connecting` / `ws_session_duration` | trend | |
| `ws_msgs_sent` / `ws_msgs_received` | counter | |
| `sio_event_rtt` | trend | Socket.IO ack round trip. |
| `checks` | rate | Assertion pass rate. |
| `iterations` / `iteration_duration` | counter / trend | |
| `group_duration` | trend | Per `group`. |
| `vus` / `vus_max` | gauge | |
| `data_sent` / `data_received` | counter | Bytes. |
| `pyro_cpu` / `pyro_mem` | gauge | The load generator's own health; the dashboard warns when it saturates. |
| custom | any | `pyro.metrics.counter/trend/rate/gauge` from scripts or plugins. |

Sinks: the live UI (event WebSocket), SQLite summary plus downsampled series, an NDJSON raw
file (`--out raw.ndjson`), and plugin reporters (Prometheus remote-write, InfluxDB).

## Thresholds
- Syntax: `metric{tag:value}: ["<agg><op><value>", …]`, where `agg` is one of `avg`, `min`, `max`, `med`, `p(N)`, `rate` or `count`.
- Evaluated live every aggregation tick and again at the end.
- `abortOnFail: true` stops the test early, after an optional `delayAbortEval`.
- `pyro load` exits non-zero when any threshold fails, which is what CI checks.

## Safety
- Pyro asks for confirmation before load testing any host that is not loopback or in the workspace allow-list (`pyro.yaml: loadAllowHosts`). `--yes` skips the prompt in CI.
- Global caps: maximum VUs and maximum RPS per run (configurable).
- Stop is graceful: VUs stop taking new iterations, in-flight requests finish within `gracefulStop`, and then they are cancelled.

## Distributed mode (Phase 5)
`pyro agent --controller host:port` registers a node over gRPC. The controller splits
VUs and arrival rates in proportion to agent capacity, streams plans and data slices,
and merges the agents' HDR histograms, which is exact because they can be merged.
