# Security

## Reporting a vulnerability
Please report vulnerabilities privately through GitHub's
[private vulnerability reporting](https://github.com/codeen15/pyro/security/advisories/new),
not in public issues. Include the version (`pyro version`), what you did and what happened.
Fixes go into the latest release; there are no long-term support branches yet.

## What Pyro protects against
Pyro sends requests on your behalf, runs scripts from collections, loads plugins and can
generate a lot of traffic. Its defences, by boundary:

**The local server (`pyro serve`)**
- Binds to `127.0.0.1` by default and requires the random session token it prints on every
  API call, as a header (so cross-site forms can't call it). The event WebSocket checks the
  token and rejects cross-origin connections.
- When listening on loopback, it answers only requests addressed to a loopback name or
  address, so a web page that rebinds its domain to 127.0.0.1 (DNS rebinding) can't reach
  it, even with `--no-token`.
- `--no-token` is refused unless bound to loopback. Binding elsewhere (`--host 0.0.0.0`)
  keeps the token mandatory; anyone with it has full access, so use it only on trusted
  networks.

**The team server (`pyro serve --team`)**
- Accounts sign in with a password (argon2id hashes) and get a random session cookie. The
  database keeps only its SHA-256 hash. The cookie is HttpOnly and SameSite=Lax, and Secure
  behind https.
- State-changing requests need the `X-Pyro-CSRF` header and a same-origin `Origin`, so other
  sites can't act with a member's cookie. The server sends no CORS headers.
- Failed sign-ins are rate-limited per account and per IP. Unknown emails cost the same time
  as wrong passwords.
- Every project route has a minimum role (viewer, editor, owner, or server admin for plugins),
  checked on every call. A test fails if a route has no entry. Non-members get 404.
- Sign-up is invite-only by default. The first account becomes the administrator, so create
  it before exposing the server.
- Members make the **server** send requests and run collection scripts. Anyone with access to
  a project can reach whatever the server's network can reach (SSRF by design), including
  internal services and cloud metadata addresses. Run it in a network segment that matches
  what your team is allowed to test.
- Request bodies never read files outside their project. Mock servers are disabled. Load
  tests are capped server-wide (`--max-loads`).
- Serve it over HTTPS (`--tls-cert`/`--tls-key` or a reverse proxy with `--public-url
  https://…`). Over plain HTTP, passwords and cookies can be read on the network.
- Cookies and OAuth 2.0 tokens fetched while sending requests are shared by a project's
  members.

**Workspace content (imported collections, cloned repositories)**
- Scripts run in an embedded JavaScript engine (goja) with no file system, process or
  environment access; `pm.sendRequest` can make HTTP requests only.
- Request bodies can only read files inside the workspace (symlinks followed) unless
  `allowFilesOutsideWorkspace: true` is set in `pyro.yaml`.
- Load tests ask before sending traffic to hosts that aren't loopback or listed in
  `loadAllowHosts`.
- Workspace paths are checked so files can't be read or written outside the workspace.
- Treat a collection like code: sending its requests sends whatever it contains to wherever
  its URLs point.

**Plugins**
- WebAssembly modules run in wazero's sandbox: no file system, no environment, and network
  access only through host functions that check the hosts the manifest declares.
- A plugin is unusable until you approve its capabilities; a new version asking for more
  needs approval again. Calls are time-limited; a crash or timeout discards the instance.
- Index downloads must use https (plain http only for localhost), and every archive must
  match its sha256 in the index. Signed index entries are planned.
- Approving `network: ["*:*"]` lets a plugin connect anywhere; approve only plugins you trust.

**Distributed agents (`pyro agent`)**
- Require a bearer token (random if not given) and run one test at a time.
- A controller with the token can make an agent send load to any host and run the
  collection's scripts on it. Keep the token secret and the port reachable only from your
  controllers.
- Traffic is plain HTTP unless the agent is started with `--tls-cert`/`--tls-key`; use TLS
  on untrusted networks.
- Agents that connect out (`--connect`) present the controller's agent token; the
  controller's agent listener (`--agent-listen`) accepts only that token and is separate
  from the UI's listener. Put it behind TLS (e.g. a reverse proxy, then `--connect https://…`)
  across untrusted networks.

## Out of scope
Denial of service by someone who already holds a session or agent token or a team account; load tests you
point at systems you aren't allowed to test.
