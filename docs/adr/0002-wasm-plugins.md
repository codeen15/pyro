# 0002 — Plugins as WASM modules via wazero

**Status:** Accepted (2026-11-01); the encoding and SDK choices are refined by [0005](0005-json-abi-and-go-sdk.md).

## Context
Pyro needs JMeter-style installable plugins that can add protocols, auth, assertions,
data sources and reporters, and they must run inside load tests. Options: WASM, out-of-process
gRPC plugins (HashiCorp go-plugin), or JS-only extensions.

## Decision
Plugins are **WebAssembly modules** run by **wazero**, using a MessagePack-based ABI with
capability-gated host functions. See [plugins.md](../plugins.md).

## Consequences
- Sandboxed by default: no filesystem or network access unless declared and approved.
- One artifact runs on every OS and architecture. wazero is pure Go, so there's no CGO.
- Calls are in-process, which suits hot paths in load tests better than per-call IPC.
- WASM has no native sockets, so the host must provide `net_*` functions and protocol plugins depend on them.
- Writing plugins needs a WASM-capable toolchain (TinyGo, Rust). The SDKs reduce the friction.
