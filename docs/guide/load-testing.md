# Load testing

A load test has **virtual users (VUs)** run your saved requests over and over, while Pyro
measures throughput, latency and errors. Plans are saved as
`loadtests/<name>.load.yaml`, next to your collections, so they reuse the same requests,
variables, auth and processors.

> Only load test systems you own or are allowed to test. Pyro asks before sending traffic to
> any host that isn't on your machine ([Safety](#safety)).

## Creating a load test

Open the **Load** tab in the sidebar and press **New load test**.

1. **Pick a profile:**

   | Profile | Shape |
   |---|---|
   | **Smoke** | 1–2 VUs for a minute, to check the flow works before anything heavier |
   | **Load** | Ramp to your expected peak, hold it, ramp down |
   | **Stress** | Climb past the expected peak in steps to see where it degrades |
   | **Spike** | A sudden jump from a small baseline, then back down |
   | **Soak** | A moderate level for an hour, to surface leaks and slow drift |
   | **Breakpoint** | Raise the request rate until thresholds fail, then stop |

2. Name it and set the target (VUs or rate).
3. Tick the requests each VU runs, in order. A YAML preview shows the plan.
4. Press **Create load test**.

Everything can be changed afterwards. To rename, duplicate or delete a load test, use **⋮** on
its row in the sidebar or next to **Save** on its page.

## The load test page

The header shows the plan's name and target, **Save**, the **⋮** menu and **Start**. Under it,
tiles count the peak VUs, scenarios, flow steps and thresholds. The tabs:

- **Designer:** edit the plan with forms.
- **YAML:** edit the same plan as text (format in [load-engine.md](../load-engine.md)).
- **Runs:** every past run of this test.

### Scenarios

A plan has one or more **scenarios** running at the same time, for example shoppers and
background API clients. Each scenario has a name, an **executor**, its flow and optional test
data.

| Executor | Model | Settings |
|---|---|---|
| **Constant VUs** | A fixed number of VUs loop for a duration | VUs, duration |
| **Ramping VUs** | VUs follow stages (ramp up, hold, ramp down) | Start VUs, stages |
| **Iterations per VU** | Each VU runs the flow N times, then stops | VUs, iterations, max duration |
| **Constant arrival rate** | Start N iterations per time unit, however long they take | Rate, per, duration, pre-allocated and max VUs |
| **Ramping arrival rate** | The start rate follows stages | Start rate, per, stages, VUs |

The VU executors are **closed** models: each VU waits for its iteration to finish before starting
the next. Arrival-rate executors are **open** models: iterations start on schedule even if the
system slows down, which is what real traffic does. When no VU is free, the iteration is
dropped and counted (`dropped_iterations`).

For stages, a chart previews the target over time. **Graceful stop** is how long in-flight
iterations may finish when the scenario ends.

**Max requests/s** at the top of the designer caps request starts across all VUs.

### The flow

Each iteration runs the flow's steps in order:

| Step | Does |
|---|---|
| **Request** | Runs a saved request with its processors, assertions and extractors. Tick **Once per VU** for steps like logging in. |
| **Think time** | Pauses for a random time between min and max (on top of any request timers) |
| **Script** | JavaScript with the `pyro.*` API for logic the steps can't express (below) |

Loops, conditions, weighted branches and groups are available in YAML; the designer shows
them and points to the YAML tab. Steps can be reordered and removed.

Requests' own pre/post-processors and timers apply in load tests too
([Processors](processors.md)).

### Test data (CSV)

Feed each iteration a row from a file, such as test users, the way JMeter's CSV Data Set
does. In a scenario's **Test data** section:

1. **Upload CSV** (CSV, TSV, text or JSON). The file is saved in the workspace's `data/` folder.
   Or pick a file already there.
2. **How columns become variables:**
   - With the first line holding column names (the default), each column becomes
     `{{column name}}`.
   - For a file without a header, type **Variable names** (`user,password,region`), applied to
     the columns in order. If the file does have a header you don't want, also tick **Skip the
     first line**.
3. Choose the **Delimiter** (comma, semicolon, tab or pipe).
4. Check the **preview**, which shows the first rows exactly as they'll be read. Rename a
   column's variable in its header, or clear it to leave that column out.
5. Choose **Rows**:
   - **Shared:** each iteration takes the next row.
   - **Per VU:** each VU keeps its own row.
   - **Random:** a random row each time.
6. Choose **When rows run out**: **Start over**, or **Stop the VU**.

Use the variables anywhere: `{{user}}` in a URL, header, body or socket message.

### WebSocket and Socket.IO steps

Send & receive, ping and close requests ([steps on a connection](realtime.md#steps-on-a-connection))
run as they do in the runner, on connections their VU opened earlier in the flow with a connect
request; each VU has its own. Tick **Once per VU** on the connect step to open one connection
per VU and reuse it every iteration. They add `ws_step_duration` (time per step, wait
included), `ws_step_failed` (rate of failed steps: a timeout, a connection that isn't open) and
`ws_ping` (ping round trip), and assertions on them count in `checks`.

A connect request with any of the settings below connects, sends messages, and keeps the
connection open for that time instead:

- **Keep open:** how long to stay connected after sending (e.g. `30s`).
- **Events to emit** (Socket.IO): each has an event name, typed **arguments** (Text, JSON,
  Number, Boolean, Binary, as in [the console](realtime.md#sending-socketio-events)), and
  **Ack** to wait for an acknowledgement.
- **Messages to send** (WebSocket): each payload as Text, JSON or Binary.
- **From the request's saved messages:** copies a message saved on the request.

Variables, including test data columns, are filled in for each VU. The step reports sessions,
connect time, messages sent and received, Socket.IO ack round trips (`sio_event_rtt`), and
errors.

### Script steps

```js
const res = pyro.request("shop/list-items");
pyro.check(res, { "status is 2xx": (r) => r.status < 300 });
if (res.json().items.length > 0) {
  pyro.vars.set("itemId", res.json().items[0].id);
  pyro.request("shop/add-to-cart");
}
pyro.sleep(0.5);
```

| API | Does |
|---|---|
| `pyro.request(path)` | Runs a saved request |
| `pyro.http.get/post/put/patch/del(url, …)` | Sends an ad-hoc request (only to confirmed hosts) |
| `pyro.check(value, {name: fn})` | Records checks |
| `pyro.group(name, fn)` | Times a block as a group |
| `pyro.sleep(seconds)` | Pauses |
| `pyro.vars.get/set` | Reads and writes the VU's variables |
| `pyro.metrics.counter/trend/rate(name)` | Custom metrics usable in thresholds |
| `pyro.vu.id`, `pyro.vu.iteration`, `pyro.scenario`, `pyro.data` | Context, including the current data row |
| `pyro.fail(message)` | Stops the script and counts the message as an error |

Details in [load-engine.md](../load-engine.md#script-steps).

### Thresholds

Thresholds decide whether the test **passed**:

| Example | Meaning |
|---|---|
| `http_req_duration: p(95)<500` | 95% of requests finish under 500 ms |
| `http_req_failed: rate<0.01` | Under 1% of requests fail |
| `http_req_duration{request:shop/login}: p(99)<800` | Just for one request |
| `checks: rate>0.99` | Assertions pass 99% of the time |

Aggregations: `avg`, `min`, `max`, `med`, `p(N)`, `rate`, `count`. Tick **Abort on fail** to stop
the test as soon as a threshold fails. The verdict uses the final values, so a brief spike that
recovers still passes; the dashboard marks it "failed earlier" so you can review it.

## Running a test

Press **Start**.
- If the plan targets hosts that aren't on your machine and aren't on the workspace's allow
  list, Pyro lists them and asks you to confirm.
- The environment selected in the top bar is used.
- With agents connected, choose **Run on**: this machine or the agents
  ([Distributed](distributed.md)).

The **dashboard** updates every second:
- **Stat tiles:** VUs, requests per second, p95 response time (with a heat swatch) and total
  requests with failures.
- **Thresholds:** each one's current value and verdict.
- **Charts:** requests per second, response time percentiles (p50/p95/p99), active VUs and
  failed requests. **Table view** shows the same numbers per second.
- **Tables:** per-request results and the most common errors.
- **A CPU warning** when the load generator itself is the bottleneck.

**Stop** ends the test gracefully (in-flight requests finish within the graceful stop).
**Abort now** cancels immediately.

When it finishes, the verdict shows **Passed**, **Failed**, **Aborted** or **Stopped**, with
the environment and who ran it.
- **Report** opens a self-contained HTML report with charts.
- **Download** saves an Excel workbook (summary, requests, thresholds, timeline), the
  per-request CSV, the JSON result or the HTML report.

Every run also appears under [Reports](reports.md) with the project's collection runs.

## Comparing runs

On the **Runs** tab, tick two runs and press **Compare**. You see:
- requests per second, failure rate and latency percentiles side by side, with the change;
- each threshold's verdict in both runs;
- the charts overlaid;
- a per-request p95 table.

The earlier run is the baseline; **Swap** reverses it. Changes under 2% read as no change.

## From the command line

```sh
pyro load checkout-stress -e staging --html report.html --xlsx report.xlsx
pyro load ./plans/smoke.load.yaml --yes
PYRO_TOKEN=pyro_pat_… pyro load checkout-stress --report-to https://pyro.example.com --project 3f9c…
```

`pyro load` prints live progress and exits non-zero unless every threshold passed, so it can
gate a CI pipeline. See [Command line](cli.md#pyro-load).

## Safety

- **Host confirmation:** Pyro asks before load testing any host that isn't loopback or listed
  under `loadAllowHosts` in `pyro.yaml`. `--yes` skips the prompt in CI.
- **VU cap:** plans needing more than 50,000 VUs (or `--max-vus`) are refused.
- **Request rate cap:** the plan's *Max requests/s*, `--max-rps`, and `loadMaxRPS` in
  `pyro.yaml`. The lowest one applies.
- **One at a time:** one load test runs per `pyro serve`.
