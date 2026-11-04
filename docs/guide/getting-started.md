# Getting started

## Install

Download the archive for your system from the
[Releases page](https://github.com/codeen15/pyro/releases) (Linux, macOS and Windows, on
amd64 and arm64). Check it against `SHA256SUMS`, unpack it and put `pyro` on your `PATH`:

```sh
tar xzf pyro_<version>_linux_amd64.tar.gz
sudo mv pyro_<version>_linux_amd64/pyro /usr/local/bin/
pyro version
```

On Windows, unzip `pyro_<version>_windows_amd64.zip` and run `pyro.exe`.

Pyro is a single file with nothing else to install: the engine and the web interface are
inside it.

To build from source instead you need Go (the version in `go.mod`) and Node 20 or later:

```sh
make build            # builds the UI, embeds it and writes bin/pyro
./bin/pyro serve --open
```

## Start the app

```sh
pyro serve --open
```

Pyro starts on `127.0.0.1:7878` and opens your browser. The address it prints carries a
one-time session token; other pages and programs on your machine can't use the engine
without it. If you close the tab, open the printed link again.

Useful options (all in [Command line](cli.md)):

| Option | Use it to |
|---|---|
| `-w <dir>` | Open a specific workspace (see below) |
| `-p <port>` | Use another port (`-p 0` picks a free one) |
| `--open` | Open the browser for you |

## Workspaces

Everything you create lives in a **workspace**: a folder of plain YAML files you can commit to
git and review like code ([Workspace files](workspace.md)).

- With no `-w`, Pyro uses the current folder if it contains `pyro.yaml`, otherwise
  `~/.pyro/workspace` (created on first run with an example collection).
- Create one for a project with `pyro init my-api --sample` (`--sample` adds an example
  collection and environment), then `pyro serve -w my-api`.

**Working as a team?** Run one shared [team server](team-server.md) (`pyro serve --team`)
instead. People sign in from their browsers, and workspaces hold projects with members and roles.

## A tour of the screen

- **Top bar:** the logo (back to the start page), the workspace name, the **Environment**
  picker, buttons for **environments and globals**, **cookies**, **[Reports](reports.md)**
  and **Docs**, and **Import**. On a phone, cookies, Reports and Docs are under **More** (⋮).
- **Sidebar:** four tabs.
  - **Collections:** your requests, in collections and folders. Search filters by name or path.
  - **History:** every request you sent, newest first.
  - **Load:** load tests, plus a link to a test that is running.
  - **Plugins:** installed plugins.
- **Main area:** the open request (editor on top, response below; drag the gap between them to
  resize), or a page such as a load test, settings or this guide.
- **Status bar:** whether the engine is running, its address, the workspace folder, and the
  **theme** menu (System, Light, Dark).

## Your first request

1. In **Collections**, hover a collection and press **+**, or create a collection with the **+**
   next to the search box.
2. Pick **HTTP**, give it a name and press **Create request**. The type is fixed once the request
   exists.
3. Type a URL, for example `https://httpbin.org/get`, and press **Send** (or `Ctrl` + `Enter`).
4. The response appears below: status, time, size, body, headers, cookies and timing.
5. Press **Save** (`Ctrl` + `S`) to write the request to its file.

Next: [Requests](requests.md) and [Variables and environments](variables.md).
