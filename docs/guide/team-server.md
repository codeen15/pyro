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
| **Owner** | Everything an editor can do, plus invite and remove members, change roles, and rename, move and delete projects and the workspace |

Server **administrators** install and approve plugins for the whole server. The first account
created on a server is an administrator.

The **project switcher** next to the logo shows the open project as *workspace / project*.
Click it to jump to another project. **Workspaces and projects** (in the switcher or the
account menu) lists all your workspaces. From there you can create workspaces and projects,
and rename or delete them.

Each person keeps their own **current values** for variables, their own request history, and
their own open WebSocket and Socket.IO sessions. Cookies and OAuth 2.0 tokens fetched in a
project are shared by its members for now.

Viewers see a **Read only** badge where Save would be, and the buttons for creating and
deleting things are hidden for them.

## Inviting people

Personal workspaces can't be shared. Create a team workspace, then from **Workspaces and
projects** choose **Members** on it and **Invite people** (owners only):

- **Invite link:** leave the email empty. Anyone you send the link to can join with the role
  you picked until it expires (1 to 30 days).
- **Invite by email:** enter an address. Only that address can use the link, once. If the
  server has email set up ([SMTP](#invite-emails)), the link is emailed for you. Either way it
  is shown so you can copy it.

Opening the link shows who invited you and to which workspace. People without an account
create one right there, even when sign-up is otherwise by invitation only. People who
already have an account sign in and are added. **Open invites** lists the links that still
work, and **Revoke** cancels one.

## Members and roles

The **Members** tab lists everyone in the workspace:

- **Owners** change roles with the role menu and **Remove** people.
- Anyone can **Leave** a team workspace.
- A workspace always keeps at least one owner. Make someone else an owner before you leave
  or step down.

The **Activity** tab shows what happened in the workspace, newest first:

- who saved, created or deleted which request, collection, environment or load test;
- imports and load test starts;
- members joining or leaving, and role changes;
- invites;
- projects created, renamed, moved or deleted.

## Moving and copying work

**A collection to another project:** in the sidebar, open the collection's **⋯** menu and
choose **Move or copy to…**. Pick the project (any project in a workspace where you're an
editor), then:

- **Copy** keeps it here too. You can copy into the same project to duplicate it.
  - Viewers can copy out of a project too.
- **Move** removes it from here. It needs the editor role here as well.
  - Pyro warns you if load tests in this project use the collection, since they stop
    working after the move.
- **Also copy environments** adds the environments the other project doesn't have.
- If the other project already has a collection with that name, the copy gets `-2`, `-3`…

**A whole project:** on **Workspaces and projects**, open a project's **⋯** menu:

- **Move to workspace…** (owners of both workspaces). Its members become the new
  workspace's members.
- **Download as .zip**. You get the project folder, the same layout as a local workspace.
- **Upload a project (.zip)** from a workspace's **⋯** menu. This is how you bring an
  existing local workspace onto the team server: zip the folder and upload it.

## Working at the same time

- When someone saves, creates or deletes something, the others see a short note (e.g.
  *Alice saved request orders/list*) and their sidebar and lists update. A request you're
  viewing without unsaved edits updates too.
- If you've been editing something that someone else saved in the meantime, saving asks
  first. Pick **Overwrite** to keep your version, **Load their version** to drop your edits,
  or **Keep editing**. This applies to requests, collection and folder settings,
  environments, globals and load tests. It works in local Pyro too, across browser tabs.

## Starting a team server

```sh
pyro serve --team --host 0.0.0.0 --public-url https://pyro.example.com
```

1. Open the address it prints and **create the first account**. It becomes the server
   administrator.
2. Create a team workspace and a project under **Workspaces and projects**.
3. Add people with [invites](#inviting-people). By default sign-up is **by invitation**:
   only people with an invite link can create an account. You can also allow sign-up
   (below) or create accounts with `pyro admin create-user`.

The server keeps two kinds of data:

- **The database:** accounts, sessions, workspaces, members, invites, activity, request
  history and saved load test runs. By default it's a SQLite file in the data directory, with
  nothing to set up. For a hosted server, use PostgreSQL ([below](#database)).
- **The data directory** (`--data`, default `~/.pyro/team`): each project's files.

```
~/.pyro/team/
  pyro.db          the SQLite database (when not using PostgreSQL)
  server.yaml      settings (optional)
  projects/<id>/   one ordinary Pyro workspace per project
```

To back the server up, copy the data directory, ideally while the server is stopped. With
PostgreSQL, back up the database as well (e.g. `pg_dump`).

## Database

SQLite is fine for a team on one machine. For a hosted server, point Pyro at PostgreSQL:

```sh
export PYRO_DATABASE_URL='postgres://pyro:secret@db.internal:5432/pyro?sslmode=require'
pyro serve --team
```

- Create an empty database and a user that owns it. Pyro creates and upgrades its tables
  when it starts.
- The URL can also go in `.env`, in `server.yaml` (`database:`) or after `--database`.
- `pyro admin` needs the same setting, so it can reach the same accounts.
- Project files still live in the data directory. Put it on a persistent volume.
- Run one Pyro server per data directory.
- Switching databases doesn't move existing accounts across. Start the new server with a
  fresh database and upload projects as `.zip` files.

## Settings from .env and the environment

`pyro serve --team` and `pyro admin` read a `.env` file from the folder you start them in, or
the file given with `--env-file`. Variables already set in the environment win over the file.
The repository's `.env.example` lists them all:

| Variable | Same as |
|---|---|
| `PYRO_DATABASE_URL` | `--database`: PostgreSQL URL (empty: SQLite) |
| `PYRO_DATA_DIR` | `--data` |
| `PYRO_HOST`, `PYRO_PORT` | `--host`, `--port` |
| `PYRO_PUBLIC_URL` | `--public-url` |
| `PYRO_TLS_CERT`, `PYRO_TLS_KEY` | `--tls-cert`, `--tls-key` |
| `PYRO_SIGNUP`, `PYRO_SIGNUP_DOMAINS` | `--signup`, `--signup-domain` (comma-separated) |
| `PYRO_MAX_LOADS` | `--max-loads` |
| `PYRO_SMTP_HOST`, `PYRO_SMTP_PORT`, `PYRO_SMTP_USERNAME`, `PYRO_SMTP_PASSWORD`, `PYRO_SMTP_FROM` | the `smtp` settings |

Flags win over the environment, which wins over `server.yaml`. Keep `.env` out of git: it
holds the database password.

## Settings

Flags, [environment variables](#settings-from-env-and-the-environment), or `server.yaml` in
the data directory.

```yaml
publicURL: https://pyro.example.com   # the address people open
signup: domains                       # invite-only (default) | open | domains
signupDomains: [example.com]          # with signup: domains
maxLoads: 2                           # load tests running at once, server-wide
database: postgres://pyro@db/pyro     # optional; default SQLite (prefer PYRO_DATABASE_URL for the password)
smtp:                                 # optional: email invites
  host: smtp.example.com
  port: 587                           # STARTTLS is used when the server offers it
  username: pyro@example.com
  from: "Pyro <pyro@example.com>"
  # password: set $PYRO_SMTP_PASSWORD instead of writing it here
```

| Sign-up policy | Who can create an account |
|---|---|
| `invite-only` | Nobody but the first person. Administrators add accounts with `pyro admin create-user` |
| `open` | Anyone who can reach the server |
| `domains` | Anyone with an email address in `signupDomains` |

### Invite emails

Without `smtp`, invites are links you copy and send yourself. With it, an invite to an email
address is also sent by email. Keep the password in `PYRO_SMTP_PASSWORD` rather than in the
file.

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

Pass the same `--data` and `--database` as the server (or run it where the server's `.env`
is). A password can also be
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
