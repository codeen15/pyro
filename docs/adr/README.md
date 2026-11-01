# Architecture Decision Records

Short records of decisions that are expensive to reverse. Add a new numbered file
rather than editing an accepted one; mark superseded records as such.

| # | Decision | Status |
|---|---|---|
| [0001](0001-go-engine.md) | Engine in Go, UI as local web app | Accepted |
| [0002](0002-wasm-plugins.md) | Plugins as WASM modules via wazero | Accepted |
| [0003](0003-socketio-in-house.md) | In-house Socket.IO client | Accepted |
| [0004](0004-yaml-file-per-request.md) | One YAML file per request, single `.req.yaml` extension | Accepted |
| [0005](0005-json-abi-and-go-sdk.md) | JSON plugin ABI and a standard-Go SDK (refines 0002) | Accepted |
| [0006](0006-agents-over-websocket.md) | Distributed agents: controller dials agents over WebSocket + JSON | Accepted |
| [0007](0007-agents-connect-out.md) | Agents may also connect out to the controller (amends 0006) | Accepted |
