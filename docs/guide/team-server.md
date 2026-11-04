# Team server

`pyro serve` runs Pyro for you alone, on your own machine. `pyro serve --team` runs it as a
**shared server** that your team signs in to. Everyone works in the same collections, load
tests and environments from their own browser. Nothing needs installing on their computers.

## Workspaces and projects

A team server organises work in three levels:

- **Workspace.** Every account gets a **personal** workspace that only that person can see.
  Anyone can also create **team** workspaces.
- **Project.** A workspace holds any number of projects. A project has its own collections,
  environments, globals, load tests and test data, exactly like a local Pyro workspace
  folder.
- **Role.** Your role in a workspace decides what you can do in its projects:

| Role | Can |
|---|---|
| **Viewer** | Open everything, send requests, open WebSocket and Socket.IO sessions, run collections, view load test results |
| **Editor** | Everything a viewer can do, plus create, change and delete collections, requests, environments, load tests and data files; import; start load tests; create projects |
| **Owner** | Everything an editor can do, plus rename and delete projects and the workspace |

Server **administrators** install and approve plugins for the whole server. The first account
created on a server is an administrator.

The **project switcher** next to the logo shows the open project as *workspace / project*.
Click it to jump to another project. **Workspaces and projects** (in the switcher or the
account menu) lists all your workspaces. From there you can create workspaces and projects,
and rename or delete them.

Each person keeps their own **current values** for variables, their own request history, and
their own open WebSocket and Socket.IO sessions. Cookies and OAuth 2.0 tokens fetched in a
project are shared by its members for now.

## Starting a team server

```sh
pyro serve --team --host 0.0.0.0 --public-url https://pyro.example.com
```

1. Open the address it prints and **create the first account**. It becomes the server
   administrator.
2. Create a team workspace and a project under **Workspaces and projects**.
3. Add people. By default sign-up is **by invitation**, so either create accounts with
   `pyro admin create-user`, or allow sign-up (below).

Everything the server keeps lives in its **data directory** (`--data`, default
`~/.pyro/team`):

```
~/.pyro/team/
  pyro.db          accounts, sessions, workspaces, projects, history, load runs
  server.yaml      settings (optional)
  projects/<id>/   one ordinary Pyro workspace per project
```

To back the server up, copy the whole directory, ideally while the server is stopped.

## Settings

Flags or `server.yaml` in the data directory. Flags win.

```yaml
publicURL: https://pyro.example.com   # the address people open
signup: domains                       # invite-only (default) | open | domains
signupDomains: [example.com]          # with signup: domains
maxLoads: 2                           # load tests running at once, server-wide
```

| Sign-up policy | Who can create an account |
|---|---|
| `invite-only` | Nobody but the first person. Administrators add accounts with `pyro admin create-user` |
| `open` | Anyone who can reach the server |
| `domains` | Anyone with an email address in `signupDomains` |

## HTTPS

Passwords and session cookies must not travel over plain HTTP on a network. Either:

- serve HTTPS directly: `--tls-cert server.crt --tls-key server.key`; or
- put Pyro behind an HTTPS reverse proxy (nginx, Caddy, a load balancer) and pass
  `--public-url https://…` so cookies are marked secure.

Pyro warns at start-up when it serves plain HTTP on a non-local address.

## Managing accounts from the command line

Run these on the server machine. They work while the server is running.

```sh
pyro admin create-user alice@example.com --name "Alice"     # asks for a password
pyro admin create-user bob@example.com --admin
pyro admin reset-password alice@example.com                 # also signs her out everywhere
pyro admin set-admin bob@example.com --revoke
pyro admin users
```

Pass `--data` if the server doesn't use the default data directory. A password can also be
piped in, e.g. `echo "$PW" | pyro admin create-user …`.

## Your account

The **account menu** (the person icon, top right) has:

- **Account**: change your name or password. Changing your password signs out your other
  sessions.
- **Workspaces and projects**.
- **Sign out**.

Sessions last 14 days from your last visit. After 10 wrong passwords in 15 minutes, sign-in
pauses for that account.

## What's different from local Pyro

- **Mock servers are off.** They would open ports on the shared machine. Run `pyro mock`
  locally instead.
- Request bodies can only read files inside the project, whatever `allowFilesOutsideWorkspace`
  says.
- Requests are sent **from the server**, so they reach whatever the server can reach. Run it
  somewhere that's allowed to talk to the systems you test, and not to ones you don't. See
  the [security notes](../../SECURITY.md).
- Load tests share the server. At most `maxLoads` (default 2) run at once, and a project runs
  one at a time.
