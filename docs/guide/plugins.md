# Plugins

Plugins add features to Pyro without changing Pyro itself. They are written in Go (built to
WebAssembly) or JavaScript, and run in a sandbox. A plugin can provide:

| Kind | Adds | Example |
|---|---|---|
| **Request type** | A new kind of request, chosen when you create one | Raw TCP, Redis |
| **Auth type** | A way to sign requests, on the **Auth** tab | HMAC signatures |
| **Processor** | A pre- or post-processor step | Sign a value into a variable; verify a response signature |

Plugin request types work everywhere HTTP does: the editor, collection runs and load tests.

## Installing

In the **Plugins** sidebar tab press **Install plugin**. You can:
- **choose a file:** a `.pyro-plugin` or `.zip` archive;
- **search the plugin index**, when one is configured.

From the command line:

```sh
pyro plugin install tcp-0.1.0.pyro-plugin
pyro plugin install ./plugins/examples/tcp        # a plugin folder
pyro plugin install tcp                            # from the index ($PYRO_PLUGIN_INDEX)
pyro plugin list
```

A running `pyro serve` picks up plugin changes made by the CLI within about a second, and the
Plugins tab refreshes on its own.

Release archives include three example plugins: `plugin-tcp` (raw TCP requests),
`plugin-hmac` (HMAC auth plus the `hmac-var` processor) and `plugin-redis-js` (Redis commands,
written in JavaScript).

## Approving

A plugin can't reach the network unless its manifest declares the addresses it needs (for
example `localhost:*`). When you install one, Pyro shows those **permissions** and the plugin
stays inactive until you **approve** it on its page (or `pyro plugin approve <name>`). You can
also **disable**, **configure** (plugin-wide settings declared by the plugin) and **remove**
plugins there.

The permissions include the plugin's **memory limit** (128 MB unless its manifest says
otherwise; JavaScript plugins default to 256 MB). A new version that asks for a different
limit needs approval again, like one that asks for new network access. A plugin that uses
more memory than its limit fails that call with
`plugin <name> ran out of memory (limit <n> MB; raise limits.memoryMB)`; the next call starts
fresh. A plugin can also cap how many copies of it run at once (`maxInstances`): extra
calls wait their turn, and one that runs out of time while waiting fails with
`plugin <name> is busy: all <n> instances in use`.

Installed plugins live in `~/.pyro/plugins` (or `$PYRO_PLUGINS_DIR`), outside your
workspace. A workspace that uses a plugin type keeps working without it: requests are kept
as written and say which plugin they need.

For distributed load tests, install and approve the same plugins on every agent.

## Writing a plugin

You can write a plugin in **Go** or **JavaScript**. The steps are the same for both: create it
from a template, run `pyro plugin dev` while you edit, try single calls with `pyro plugin test`,
then pack it.

### 1. Create it

```sh
pyro plugin new ./redis --lang js --kind protocol --id redis    # JavaScript
pyro plugin new ./redis --lang go --kind protocol --id redis    # Go (the default language)
```

`--kind` is `protocol` (a request type), `auth` or `processor`. The template is a working
plugin to edit.

- **JavaScript** writes `plugin.js` (the whole plugin; nothing to build), `pyro-plugin.yaml`
  with `runtime: js`, `pyro.d.ts` (types for the `pyro` object, so your editor completes it) and
  a README.
- **Go** writes `main.go`, `pyro-plugin.yaml`, `go.mod` and a README. Run `go mod tidy` once to
  fetch the SDK, `github.com/codeen15/pyro/plugins/sdk/go`.

### 2. Run it while you edit

```sh
cd redis
pyro plugin dev .
```

`dev` installs the plugin, **approves its permissions for you** and prints them, then watches
the folder. On every save it rebuilds (Go) and reinstalls; a running `pyro serve` (or the team
server) picks the new version up within about a second, and the Plugins tab and the request
**Type** menu refresh on their own, so you can try the plugin in the app without a restart.

```
✓ redis 0.1.0 installed (built in 1.4s)      # a Go plugin; JavaScript prints no build time
  approved network: localhost:6379
```

A build or install error is printed and the previous version keeps working. A JavaScript syntax
error, or an exception while the script loads, shows the JavaScript error with its line and
column. Press Ctrl-C to stop. Use `--once` to do a single build and install (for scripts).

### 3. Try one call

```sh
pyro plugin test redis --protocol redis --url redis://localhost:6379 --body 'PING'
```

This runs one call and prints the response (or the signed request for an auth plugin, or the
tests for a processor), the call time and the plugin's log lines, without a collection. Repeat
runs are quick because compiled code is cached; see [Command line](cli.md#pyro-plugin-test).

### 4. Pack and share it

```sh
pyro plugin pack .        # writes redis-0.1.0.pyro-plugin and prints its sha256
```

Share the `.pyro-plugin` file; others install it with `pyro plugin install` or the Plugins tab
and approve its permissions. Before you share it, narrow the manifest's `network` list to the
addresses the plugin really needs.

A Go processor, for example:

```go
func process(ctx *pyro.Context, p *pyro.Process) error {
	if p.Phase == "pre" {
		p.SetVar("requestId", newID())
		p.Request.SetHeader("X-Request-Id", p.Vars["requestId"])
		return nil
	}
	p.Test("status below 500", p.Response.Status < 500, "")
	return nil
}
```

and the same in JavaScript:

```js
pyro.registerProcessor("request-id", (ctx, p) => {
  if (p.phase === "pre") {
    const id = "req-" + Date.now()
    p.setVar("requestId", id)
    p.request.setHeader("X-Request-Id", id)
    return
  }
  p.test("status below 500", p.response.status < 500)
})
```

JavaScript notes:
- There is no `console`; log with `pyro.log.info(...)` (the lines show in the response's console
  and in `pyro plugin test`).
- Assigning `p.vars.x = …` or replacing `p.request = {…}` does not reach Pyro. Call
  `p.setVar(name, value)` and change the request object you were given (`setHeader` and so on).
- Open connections with `pyro.dial` inside your handlers. A `pyro.dial` at the top level of the
  script runs when the plugin is installed, before it is approved, and fails.
- If the script has a syntax error or throws while loading, the install fails with the JavaScript
  error and its position.
- `plugins/examples/redis-js` in the Pyro repository is a complete example: a Redis protocol
  plugin in one `plugin.js`.

### JavaScript or Go?

| | JavaScript | Go |
|---|---|---|
| Build step | none | `go build` for `wasip1` (done by `pyro plugin dev`) |
| Speed | about 17 times slower than Go on compute-heavy code | native speed |
| First use | Pyro compiles its JavaScript engine once, about 7 s (then about 0.25 s from the cache); an instance starts in about 17 to 26 ms | an instance starts in 2 to 4 ms |
| Default limits | 256 MB memory, 32 calls at once | 128 MB memory, no cap on calls |
| Best for | quick request types, signing and processors; anything run by hand or in small runs | heavy load tests, parsing or compression work |

Both run in the same sandbox, with the same permissions and approval. For a plugin used
at thousands of calls a second, use Go. The JavaScript engine adds about 4.6 MB (compressed)
to Pyro itself.

### Limits

A manifest's `limits` section sets two caps. Both are shown when you approve the plugin.
`memoryMB` is the most memory one copy of the plugin may use (default 128 for Go, 256 for
JavaScript, at most 1024); past it a call fails with `plugin <name> ran out of memory` and
the next call starts fresh. `maxInstances` is how many copies may run at once (default 32 for JavaScript, unlimited
for Go): further calls wait for a free one, and a call that times out while waiting fails with
`plugin <name> is busy: all <n> instances in use`. A load test warns before it starts when a
plugin's `maxInstances` is below the test's peak number of virtual users, since the extra
users would queue. Plugins get the real clock and real random numbers (`Date.now()` and
`Math.random()` work), but cannot sleep: to wait, read from a connection with a timeout.

The manifest (`pyro-plugin.yaml`), the SDK API, the JavaScript API, the ABI and the security
model are described in [plugins.md](../plugins.md).
