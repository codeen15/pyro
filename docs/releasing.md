# Releasing

1. Make sure `main` is green in CI (it includes a release dry run).
2. Move the `CHANGELOG.md` entry from "unreleased" to the version and date; commit.
3. Tag and push:
   ```sh
   git tag -a v0.1.0 -m "Pyro v0.1.0"
   git push origin v0.1.0
   ```
4. The **Release** workflow runs the tests, builds every archive with `make dist` and opens a
   **draft** GitHub release with them and generated notes. Tags with a suffix (`v0.2.0-rc.1`)
   become pre-releases.
5. Download one archive, check it against `SHA256SUMS`, run `pyro version`, then publish the
   draft.

Locally, `make dist VERSION=v0.1.0` builds the same files into `dist/`:

| File | Contents |
|---|---|
| `pyro_<version>_<os>_<arch>.tar.gz` / `.zip` (Windows) | `pyro`, `LICENSE`, `NOTICE`, `README.md`, `THIRD_PARTY_LICENSES.txt` |
| `plugin-tcp_<version>.pyro-plugin`, `plugin-hmac_<version>.pyro-plugin` | Example plugins for `pyro plugin install` |
| `SHA256SUMS` | Checksums of everything above |

Targets: Linux, macOS and Windows on amd64 and arm64, all built with `CGO_ENABLED=0` and
`-trimpath`. `THIRD_PARTY_LICENSES.txt` is generated from the Go modules compiled in and the
npm packages bundled into the UI; the build warns about any package without a license file.

The plugin SDK (`plugins/sdk/go`) is its own Go module. To make it installable with
`go get`, tag it separately as `plugins/sdk/go/v0.1.0`; until then, `pyro plugin new` needs
`--sdk` pointing at a checkout.
