# Distributed load tests

When one machine can't generate enough load (the dashboard warns when the load generator's
CPU is saturated), run the same test from several machines. One Pyro is the **controller**;
the others run `pyro agent`.

On a **team server** you add and manage agents in the app instead, with no flags: see
[Load agents on other machines](team-server.md#load-agents-on-other-machines). The sections
below are for `pyro load` and a local `pyro serve`.

## Agents the controller connects to

On each load machine:

```sh
pyro agent --token s3cret            # listens on port 7979
```

Then from the controller:

```sh
pyro load checkout-stress --agent 10.0.0.5:7979 --agent 10.0.0.6:7979 --agent-token s3cret
```

Or start the app with the agents, and choose **Run on: N agents** next to **Start**:

```sh
pyro serve --agent 10.0.0.5:7979,10.0.0.6:7979 --agent-token s3cret
```

## Agents that connect out (behind NAT or a firewall)

Agents can dial the controller instead. On the controller:

```sh
pyro serve --agent-listen :7980 --agent-token s3cret
# or, for a one-off run that waits for 3 agents:
pyro load checkout-stress --agent-listen :7980 --wait-agents 3 --agent-token s3cret
```

On each load machine:

```sh
pyro agent --connect controller.example.com:7980 --token s3cret
```

Connected agents wait for tests and reconnect by themselves. The agent listener is separate
from the UI, which stays on your machine. (On a team server, agents that dial out connect to
the server's own address and port instead; there is no separate listener.)

## Running agents with Docker or systemd

Agents use the same Docker image as the team server, with `agent` as the command, and
`deploy/` has a Compose file and a systemd unit for them. See
[Load agents on other machines](team-server.md#load-agents-on-other-machines). That page
also covers the team server, where administrators add agents (direct, or connecting in with
their own token) under **Administration → Agents**. `--agent-listen` is for `pyro serve` and
`pyro load`; `pyro serve --team` rejects it, because its agents connect through the main
port.

## What to know

- **Splitting:** each agent gets a share of every scenario. VU counts are split as evenly as
  whole numbers allow; arrival rates are divided exactly. A rate cap is split too.
- **Nothing to copy:** agents receive the plan, the requests with their settings, the
  environment, data rows and body files. They don't need the workspace. In **shared** data
  mode each row goes to one agent; **per VU** and **random** data go to every agent.
- **Exact results:** agents stream raw measurements, so percentiles, thresholds and
  per-request tables cover every sample. The dashboard and the HTML report add an **Agents**
  table.
- **Plugins:** plugin request and auth types must be installed and approved on every agent.
  The controller checks before starting.
- **Failures:** if an agent drops out for 30 seconds, the test stops and fails, naming the
  agent.
- **Security:** agents trust any controller with the token. On a team server each agent has
  its own token. Keep it secret, set it with
  `PYRO_AGENT_TOKEN`, and expose port 7979 only to controllers. For TLS, start agents with
  `--tls-cert` and `--tls-key` and address them as `https://host:port`.

## Streaming live metrics elsewhere

`-o` (repeatable, on `pyro load` and `pyro serve`) sends every tick to other systems while the
test runs:

| Output | Example |
|---|---|
| InfluxDB 1.x | `-o influxdb=http://localhost:8086/write?db=pyro` |
| InfluxDB 2.x | `-o influxdb=http://localhost:8086/api/v2/write?org=acme&bucket=load` (token in `PYRO_INFLUXDB_TOKEN`) |
| Prometheus remote write | `-o prometheus=http://localhost:9090/api/v1/write` |
| JSON lines | `-o json=ticks.ndjson` |
| CSV | `-o csv=ticks.csv` |

A slow or unreachable backend never slows the test; dropped writes are reported at the end.
The metric names are listed in [load-engine.md](../load-engine.md#live-outputs).
