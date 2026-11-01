# 0007 — Agents may also connect out to the controller

**Status:** Accepted (2026-11-02). Amends [0006](0006-agents-over-websocket.md).

## Context
ADR 0006 has the controller dial each agent, so every agent needs an open inbound port.
Load machines are often behind NAT, in other clouds, or on networks where only outbound
connections are allowed.

## Decision
- `pyro agent --connect <controller>` opens the WebSocket itself, sends `hello` with its
  info, then waits. The controller (`pyro serve --agent-listen`, or `pyro load
  --agent-listen --wait-agents N`) holds these idle connections and claims them as workers
  for a test. From `job` onwards the exchange is exactly ADR 0006's.
- The agent listener is a separate HTTP server from the UI, so the UI can stay bound to
  loopback while agents reach the controller over the network.
- One connection carries one job; the agent reconnects afterwards. Lost connections are
  retried with exponential backoff (1 s to 30 s); a rejected token stops the agent.
- Idle connections are pinged every 15 s and dropped when they stop answering.

## Consequences
- Agents need only outbound access; the controller needs one open port for all agents.
- Both modes can be mixed in one test (`--agent` addresses and connected agents).
- The controller must be reachable when a test is about to start; agents that connect
  later wait for the next test.
