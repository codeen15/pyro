# 0001 — Engine in Go, UI as a local web app

**Status:** Accepted (2026-11-01)

## Context
Pyro must handle interactive API work and generate heavy load (10k+ concurrent virtual
users) on Linux, macOS and Windows. Options considered: Go, Rust, Node.js/TypeScript, Java/Kotlin.
For the UI: a local server with a browser, or a desktop wrapper (Tauri/Electron).

## Decision
Write the engine in **Go** and ship one static binary that serves a **React SPA** on
localhost (`pyro serve`) and also runs headless (`pyro run`, `pyro load`).

## Consequences
- Goroutines make one-VU-per-goroutine cheap. k6 shows the model works.
- `CGO_ENABLED=0` cross-compiles to every target, so all dependencies must be pure Go (e.g. `modernc.org/sqlite`).
- The same binary works on CI servers and remote boxes; there's no separate GUI build per OS.
- Script compatibility with Postman has to come from an embedded JS engine (goja), not native Node.
- A Tauri wrapper stays possible later without changing the engine.
