# Pyro user guide

Pyro is an API client and a load-testing tool in one program. You define a request once, then
send it by hand, run it as part of a functional test suite, or have thousands of virtual users
run it in a load test. It speaks HTTP, WebSocket and Socket.IO, imports Postman, OpenAPI, HAR,
cURL and JMeter, and can be extended with WebAssembly plugins.

This guide explains how to use it. The same pages are available inside the app under
**Docs** in the top bar.

| Page | What it covers |
|---|---|
| [Getting started](getting-started.md) | Install, start the app, a tour of the screen, workspaces |
| [Requests](requests.md) | Collections, folders, HTTP requests, the response viewer, history |
| [Variables and environments](variables.md) | `{{variables}}`, environments, globals, collection variables, cookies |
| [WebSocket and Socket.IO](realtime.md) | Realtime consoles, messages and typed arguments |
| [Processors and tests](processors.md) | Pre- and post-processors, scripts, assertions, extractors, think time |
| [Collection runner](runner.md) | Running a collection as a test suite, data files, CI |
| [Load testing](load-testing.md) | Load tests, profiles, the designer, test data, sockets, thresholds, results |
| [Distributed load tests](distributed.md) | Running one load test across several machines, live metric outputs |
| [Import, export, docs and mocks](import-export.md) | Postman, OpenAPI, HAR, cURL, JMeter; Postman export; API docs; mock servers |
| [Plugins](plugins.md) | Installing, approving and writing plugins |
| [Command line](cli.md) | Every `pyro` command and flag |
| [Workspace files](workspace.md) | What Pyro writes to disk, git, settings in `pyro.yaml` |
| [Keyboard shortcuts and tips](tips.md) | Shortcuts, themes, troubleshooting |

Reference material for contributors (file formats in detail, the REST API, the plugin ABI,
architecture) lives in the parent [`docs/`](../) folder.
