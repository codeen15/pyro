# Plugins (Phase 4)

Pyro plugins are WebAssembly modules run by [wazero](https://wazero.io) (pure Go,
no CGO). WASM makes them sandboxed, portable across every OS and architecture,
and possible to write in several languages.

## Plugin kinds
| Kind | Purpose | Examples |
|---|---|---|
| `protocol` | New request `kind: plugin:<name>`, using host-provided sockets | gRPC, MQTT, raw TCP/UDP, Kafka |
| `auth` | Sign requests or fetch tokens | AWS SigV4, HMAC |
| `assertion` | Custom checks | XPath, JSON Schema |
| `extractor` | Custom parsing into variables | protobuf decode |
| `datasource` | Feed load-test data | DB query, Faker |
| `reporter` / `sink` | Export results and metrics | Prometheus, InfluxDB, Slack |
| `importer` | Convert other formats into workspaces | Insomnia, OpenAPI |
| `ui` (later) | Extra panels in a sandboxed iframe | Custom result views |

One plugin may provide several kinds.

## Manifest: `pyro-plugin.yaml`
```yaml
name: mqtt
version: 1.2.0
abi: 1
description: MQTT 3.1.1/5 protocol
author: Example Org
license: Apache-2.0
module: mqtt.wasm
provides:
  - kind: protocol
    id: mqtt                     # request kind becomes plugin:mqtt
capabilities:                    # shown to the user for approval on install
  network:
    - "*:1883"
    - "*:8883"
  env: []
  files: []
config:                          # JSON Schema; the UI renders it as a form
  type: object
  properties:
    clientIdPrefix: { type: string, default: pyro }
```

## ABI v1
Messages crossing the host/guest boundary are **MessagePack**, passed as `(ptr, len)` in guest linear memory.

**Exported by the plugin**
| Function | Purpose |
|---|---|
| `pyro_alloc(size) -> ptr` / `pyro_free(ptr, size)` | Guest memory management for host → guest buffers. |
| `pyro_init(cfg_ptr, cfg_len) -> status` | Called once per instance with the resolved config. |
| `pyro_describe() -> (ptr,len)` | Returns the capabilities and kinds actually implemented, checked against the manifest. |
| `pyro_invoke(op_ptr, op_len) -> (ptr,len)` | One entry point for every operation: `{kind, op, payload}`, e.g. `protocol.execute`, `protocol.connect`, `session.send`, `assert.check`, `datasource.next`, `report.write`. |

**Imported from the host (`pyro` module)**
| Function | Notes |
|---|---|
| `log(level, ptr, len)` | Goes to the engine log and the UI. |
| `metric_emit(ptr, len)` | `{name, type, value, tags}` into the metrics pipeline. |
| `var_get` / `var_set` | VU-scoped variables. |
| `net_dial(addr) -> handle`, `net_read`, `net_write`, `net_close`, `tls_dial` | **Capability-gated**: the address must match `capabilities.network`. |
| `now_ns()`, `sleep_ns()` | Timers; `sleep` yields the VU without blocking an OS thread. |
| `poll_events(handle) -> (ptr,len)` | Delivers incoming stream messages for protocol sessions. |

Version rules: the host rejects a plugin whose `abi` major it doesn't support. New host
functions are added in minor versions and are optional for guests.

## Execution model
- The compiled module is cached once per plugin, and instances are **pooled**. In load mode each VU borrows an instance, so plugin state is per VU unless the plugin uses host variables.
- Every call runs under the VU's context: cancelling it (stop, timeout) interrupts the guest.
- Memory and fuel limits are configurable per plugin.

## Security
- No ambient authority. Plugins can't touch the filesystem, network or environment except through capabilities declared in the manifest, which the user approves when installing.
- A capability change on upgrade triggers approval again.
- Index entries carry a sha256, and optionally a signature (minisign/cosign). Installs fail on a mismatch.

## Distribution
- **Index:** a git repository (`pyro-plugins/index`) of manifests plus artifact URLs and checksums.
- **CLI:** `pyro plugin search|install <name>[@version]|list|remove|update`, and `pyro plugin install ./my-plugin.wasm` for local development.
- **Location:** `~/.pyro/plugins/<name>/<version>/`. Enabled plugins are tracked in SQLite.
- **UI:** a Plugin Manager tab to browse, install, approve capabilities and configure plugins.

## SDKs
- **Go (TinyGo)** and **Rust** SDKs under `plugins/sdk/`. They wrap the ABI, MessagePack handling and host calls behind typed interfaces such as `ProtocolDriver` and `Assertion`.
- Templates: `pyro plugin new --kind protocol --lang go`.
- Reference plugin: **raw TCP** (`plugins/examples/tcp`). It doubles as the integration test fixture.
