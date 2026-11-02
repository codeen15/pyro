# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users
Teams that both build APIs and test them before release: the same people author
requests while developing and later stress test those APIs. Both jobs carry equal
weight; neither the request editor nor the load-test dashboard is secondary.
- **While building:** compose and send REST, WebSocket and Socket.IO requests, switch
  environments, read responses, debug realtime traffic.
- **Before release:** run collections as functional suites in CI, then run load
  tests (stress, spike, soak, concurrency) and judge results against thresholds.

## Product Purpose
Define an API once and use that definition for manual sending, automated functional
runs, and load tests with thousands of virtual users, instead of keeping copies in
separate tools (Postman for building, JMeter/k6 for load). Success is a team moving its
Postman collections in and running a meaningful load test without re-describing the API.

## Positioning
One engine and one data model drive interactive requests, collection runs and load
tests. A request saved in the editor *is* the unit a load scenario runs, and plugins
extend all three modes at once. Workspaces are plain YAML files that live in git
beside the code.

## Operating Context
- Runs locally as a single binary (`pyro serve`) and opens in the browser; the same
  binary runs headless in CI (`pyro run`, `pyro load`).
- Users arrive from Postman (import of v2.0/v2.1 collections and environments) and
  from JMeter/k6 for load testing.
- Workspaces are directories in a git repository, edited in the UI or in a code editor.
- Long sessions next to an IDE and a terminal; load tests are watched live for minutes
  to hours.

## Capabilities and Constraints
- Protocols: HTTP/REST, WebSocket, Socket.IO built in; more through WASM plugins.
- Phase status and scope: see `docs/roadmap.md`. Specs live in `docs/`.
- Local-first: no accounts, no hosted backend; collaboration happens through git.
- Terminology: workspace, collection, folder, request, environment, variable, session
  (WebSocket/Socket.IO connection), run (collection run), load test, scenario, VU
  (virtual user), threshold, plugin.
- Undecided: license, distribution channels beyond GitHub releases.

## Brand Commitments
- Name: **Pyro**. Logo: a red flame with a lightning bolt (`web/src/assets/pyro-logo.png`); its red
  (`#cf1920`) is the brand and primary colour. The UI uses Ant Design's standard look (see `DESIGN.md`).
- Open-source project: copy should be plain and technical, not marketing.

## Evidence on Hand
- No users, benchmarks, testimonials or adoption figures exist yet. Do not fabricate them.
- Real sample content: `pyro init --sample` workspace; Postman fixture
  `pkg/engine/importer/postman/testdata/shop.v21.json`.

## Product Principles
1. **Define once, use everywhere.** Every feature should work on the shared request
   model so manual, run and load modes stay in step.
2. **Honest numbers.** Timings, percentiles and errors are shown exactly as measured,
   with nothing smoothed over or hidden.
3. **Files are the truth.** Anything in the UI maps to readable YAML on disk;
   nothing important lives only in the app.
4. **Fast for daily use.** Keyboard-first, dense, and quick to start, since the editor
   is open all day.
5. **Explain failures.** Errors name the problem and what to do next.

## Accessibility & Inclusion
No product-specific requirement established beyond WCAG 2.2 AA for the web UI
(keyboard operation, contrast, visible focus, reduced motion).
