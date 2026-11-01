# 0003 — In-house Socket.IO client

**Status:** Accepted (2026-11-01)

## Context
"Socket" support in Pyro means Socket.IO. The Go ecosystem's Socket.IO clients are
unmaintained or incomplete for Engine.IO v4 / Socket.IO v5, and in load mode Pyro needs
control over allocation and connection handling.

## Decision
Implement a small **Engine.IO v4 + Socket.IO v5 client** in `pkg/engine/protocol/socketio`,
layered on the WebSocket driver. Long-polling fallback comes later.

## Consequences
- Pyro owns the packet encoding (CONNECT/EVENT/ACK/BINARY_EVENT), namespaces, acks, and the ping/pong heartbeat.
- Conformance tests against the official Node `socket.io` server (4.x) run in CI.
- Ack round-trip time becomes a first-class metric (`sio_event_rtt`).
