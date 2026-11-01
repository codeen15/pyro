# 0005 — JSON plugin ABI and a standard-Go SDK

**Status:** Accepted (2026-11-01). Refines [0002](0002-wasm-plugins.md).

## Context
ADR 0002 chose WASM plugins with a MessagePack ABI and SDKs for TinyGo and Rust. While
building Phase 4 we found that Go 1.24+ can build WASI reactor modules itself
(`GOOS=wasip1 -buildmode=c-shared` with `//go:wasmexport`), and that neither TinyGo nor
a Rust toolchain was available to test against.

## Decision
- Messages between host and plugin are **JSON** in linear memory, not MessagePack.
- The first SDK targets the **standard Go toolchain**, not TinyGo.
- The Rust SDK is deferred until it can be built and tested.

## Consequences
- Every language has JSON built in, so a plugin author needs no extra library, and messages
  are readable when debugging.
- Each call costs a little more (de)serialisation than MessagePack would. Plugin calls do
  network I/O, so this doesn't show up in practice (50 VUs ran 2.1k TCP requests/s with
  p99 19 ms).
- Standard-Go modules are larger (2–5 MB) than TinyGo's would be, and compile in about
  0.5 s on first use (cached on disk afterwards). Instances start in 2–4 ms.
- The ABI is versioned (`abi: 1`); switching encodings later would be ABI 2.
