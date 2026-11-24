# redis-js

A Redis request type for Pyro, written in JavaScript (`runtime: js`): one `plugin.js`
and a manifest, no build step. It speaks RESP over a plain TCP connection, so it works
in the editor, collection runs and load tests.

## Using it

Request kind `plugin:redis`. The URL is `redis://host[:port]` (port defaults to 6379).
The body is one command line, split on whitespace; wrap an argument that contains
spaces in double quotes (`SET greeting "hello big world"`; inside quotes `\"` and `\\`
are escapes).

| Field | Default | Meaning |
|---|---|---|
| `db` | `0` | When not 0, `SELECT <db>` is sent first. |
| `timeoutMs` | `5000` | Connect and read timeout. |

The connection is opened per request and closed afterwards. The reply becomes the response:

| Reply | Status | Body |
|---|---|---|
| simple string (`+OK`) | 0 | the text |
| integer (`:42`) | 0 | the number |
| bulk string (`$`) | 0 | the text |
| null bulk or null array | 0, status text `nil` | empty |
| array | 0 | JSON, e.g. `["x","y"]` (nulls are `null`) |
| error (`-ERR ...`) | 1, status text `ERR` | the error text |

## Try it

The manifest allows `*:6379`; narrow `capabilities.network` to your server before sharing it.

```sh
cd plugins/examples/redis-js
pyro plugin test . --protocol redis --url redis://localhost:6379 --body "SET a 1"
pyro plugin test . --protocol redis --url redis://localhost:6379 --body "GET a"
pyro plugin dev .      # rebuilds and reinstalls on every save
pyro plugin pack .     # redis-js-0.1.0.pyro-plugin
```
