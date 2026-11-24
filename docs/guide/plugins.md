# Plugins

Plugins add features to Pyro without changing Pyro itself. They are WebAssembly modules that
run in a sandbox. A plugin can provide:

| Kind | Adds | Example |
|---|---|---|
| **Request type** | A new kind of request, chosen when you create one | Raw TCP |
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

Release archives include two example plugins: `plugin-tcp` (raw TCP requests) and
`plugin-hmac` (HMAC auth plus the `hmac-var` processor).

## Approving

A plugin can't reach the network unless its manifest declares the addresses it needs (for
example `localhost:*`). When you install one, Pyro shows those **permissions** and the plugin
stays inactive until you **approve** it on its page (or `pyro plugin approve <name>`). You can
also **disable**, **configure** (plugin-wide settings declared by the plugin) and **remove**
plugins there.

The permissions include the plugin's **memory limit** (128 MB unless its manifest says
otherwise; JavaScript plugins default to 256 MB). A new version that asks for a different
limit needs approval again, like one that asks for new network access. A plugin that uses
more memory than its limit fails that call with `plugin crashed`; the next call starts
fresh. A plugin can also cap how many copies of it run at once (`maxInstances`): extra
calls wait their turn, and one that runs out of time while waiting fails with
`plugin <name> is busy: all <n> instances in use`.

Installed plugins live in `~/.pyro/plugins` (or `$PYRO_PLUGINS_DIR`), outside your
workspace. A workspace that uses a plugin type keeps working without it: requests are kept
as written and say which plugin they need.

For distributed load tests, install and approve the same plugins on every agent.

## Writing a plugin

Plugins are written in Go with Pyro's SDK and built with the standard Go toolchain:

```sh
pyro plugin new ./my-plugin --kind protocol --id redis      # or --kind auth, --kind processor
cd my-plugin
go mod tidy               # fetches the SDK, github.com/codeen15/pyro/plugins/sdk/go
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o redis.wasm .
pyro plugin install .
pyro plugin pack .        # writes redis-0.1.0.pyro-plugin and prints its sha256
```

Try a plugin without installing it or writing a collection: `pyro plugin test . --protocol redis
--url redis://localhost:6379 --body PING` runs one call and prints the response and the plugin's
log lines (see [Command line](cli.md#pyro-plugin-test)).

The template is a working plugin to edit. A processor, for example:

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

You can also write a plugin in **JavaScript**, with no build step: set `runtime: js` and
`module: plugin.js` in the manifest, and register handlers from the script
(`pyro.registerProtocol("redis", (ctx, req) => { … })`). Install it the same way
(`pyro plugin install .`). If the script has a syntax error or throws while loading, the
install fails and shows the JavaScript error with its line and column. JavaScript plugins
run under the same permissions and limits as Go ones, but are slower and run at most 32
calls at once by default; prefer Go for heavy load tests. The first JavaScript plugin
makes Pyro compile its JavaScript engine once (several seconds, in the background).
`plugins/examples/redis-js` in the Pyro repository is a complete example: a Redis protocol
plugin in one `plugin.js`.

The manifest (`pyro-plugin.yaml`), the SDK API, the ABI, JavaScript plugins and the security
model are described in [plugins.md](../plugins.md).
