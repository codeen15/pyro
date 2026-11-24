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
  jwt.secret       the key that signs sign-in tokens (unless PYRO_JWT_SECRET is set)
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
| `PYRO_JWT_SECRET` | the key that signs sign-in tokens (default: `<data>/jwt.secret`) |
| `PYRO_LDAP_URL`, `PYRO_LDAP_BIND_DN`, `PYRO_LDAP_BIND_PASSWORD`, `PYRO_LDAP_BASE_DN`, `PYRO_LDAP_USER_FILTER`, `PYRO_LDAP_ADMIN_GROUP` | the `ldap` settings ([LDAP](#ldap--active-directory)) |
| `PYRO_OIDC_<ID>_SECRET` | an SSO provider's client secret ([single sign-on](#single-sign-on-openid-connect)) |
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

## Deploying the team server

Pyro is one program: the web UI and the engine are in the same binary, so a deployment is a
single container or service. Every setting comes from `PYRO_*` environment variables. The
`deploy/` folder of the repository has a ready-made kit:

| File | Use |
|---|---|
| `deploy/docker-compose.yml` | The team server with Docker Compose: pulls the image or builds it |
| `deploy/.env.example` | Every server setting, with comments |
| `deploy/pyro.service` | The team server as a systemd service, without Docker |
| `deploy/docker-compose.agent.yml`, `deploy/agent.env.example`, `deploy/pyro-agent.service` | [Load agents](#load-agents-on-other-machines) |

Whichever way you run it, the server keeps everything it writes (the SQLite database, the
sign-in key, project files and installed plugins) in one data directory: `/data` in the
image, `/var/lib/pyro` with systemd. Back that up. If you point it at PostgreSQL, back up
the database too.

### 1. Pull the image

The image is published to `ghcr.io/codeen15/pyro` for amd64 and arm64:

| Tag | What |
|---|---|
| `latest` | The newest release |
| `0.2.0`, `0.2` | A release, or the newest patch of a minor release |
| `edge` | The `main` branch, for trying changes before a release |

```sh
docker run -d --name pyro --restart unless-stopped \
  -v pyro-data:/data -p 127.0.0.1:7878:7878 \
  -e PYRO_PUBLIC_URL=https://pyro.example.com \
  ghcr.io/codeen15/pyro:latest
```

Or with Compose, from a checkout (or just the `deploy/` folder):

```sh
cp deploy/.env.example deploy/.env      # then edit it
docker compose -f deploy/docker-compose.yml up -d
```

The image runs as an unprivileged user and has no shell. Its port is bound to 127.0.0.1, so
only a reverse proxy on the same machine reaches it (see [HTTPS](#https)). Set
`PYRO_BIND=0.0.0.0` in `.env` to publish it on the network.

### 2. Build the image yourself

From a checkout, with Docker:

```sh
make docker                                         # builds pyro:dev; IMAGE=name:tag to change
docker compose -f deploy/docker-compose.yml up -d --build   # or build and start with Compose
```

`make docker-smoke` builds the image and checks it: it starts in team mode, runs as the
unprivileged user on a fresh volume, keeps its data across a restart and runs admin commands.

### 3. Dokploy (and similar platforms)

Dokploy runs the Compose file and puts its Traefik proxy in front of it, with HTTPS:

1. Create a **Compose** service from the repository, with `deploy/docker-compose.yml` as the
   compose path. (Or create an **Application** from the image `ghcr.io/codeen15/pyro:latest`.)
2. In **Environment**, paste the settings you need from `deploy/.env.example`. Set
   `PYRO_PUBLIC_URL` to the address people will open, starting with `https://`.
3. In **Domains**, add the host name, container port **7878**, and turn HTTPS on.
4. **Deploy**, open the address and create the first account.

Dokploy routes to the container port itself, so the host port mapping isn't needed there.
Portainer, Coolify and other Compose-based platforms work the same way.

### 4. A machine without Docker

Download the archive for your platform from the GitHub release, then:

```sh
sudo useradd --system --home /var/lib/pyro --shell /usr/sbin/nologin pyro
tar xzf pyro_<version>_linux_amd64.tar.gz
sudo install -m 755 pyro_<version>_linux_amd64/pyro /usr/local/bin/pyro
sudo install -d -m 750 /etc/pyro
sudo install -m 640 -g pyro deploy/.env.example /etc/pyro/pyro.env   # then edit it
sudo install -m 644 deploy/pyro.service /etc/systemd/system/pyro.service
sudo systemctl daemon-reload && sudo systemctl enable --now pyro
```

The service listens on 127.0.0.1:7878 for a reverse proxy on the same machine and keeps its
data in `/var/lib/pyro`.

### The database

Leave `PYRO_DATABASE_URL` empty to use SQLite in the data directory. To use a PostgreSQL you
already run, set it, for example
`PYRO_DATABASE_URL=postgres://pyro:secret@db.example.com:5432/pyro?sslmode=require`. The
database must be reachable from the container or machine Pyro runs on (inside Docker,
`localhost` is the container itself). See [Database](#database).

### HTTPS in front

Put a reverse proxy in front and set `PYRO_PUBLIC_URL=https://…`:

- Dokploy, Coolify and similar platforms: their proxy does it (above).
- Caddy: `pyro.example.com { reverse_proxy 127.0.0.1:7878 }` gets a certificate by itself.
- nginx or Traefik: proxy to `127.0.0.1:7878`, and pass WebSocket upgrades (live updates use
  `/api/v1/events`).

Or let Pyro serve HTTPS itself: set `PYRO_TLS_CERT` and `PYRO_TLS_KEY` (paths inside the
container or machine) and publish the port (`PYRO_BIND=0.0.0.0`).

### Admin commands

`pyro admin` must reach the same data directory and database as the server:

```sh
docker exec pyro /pyro admin users                              # Docker
docker compose -f deploy/docker-compose.yml exec pyro /pyro admin users
sudo -u pyro env $(grep -v '^#' /etc/pyro/pyro.env | xargs) \
  PYRO_DATA_DIR=/var/lib/pyro pyro admin users                  # systemd
```

### Updating

- Docker: `docker compose -f deploy/docker-compose.yml pull && docker compose -f deploy/docker-compose.yml up -d`,
  or change `PYRO_IMAGE_TAG` to a release. In Dokploy, redeploy.
- Without Docker: replace `/usr/local/bin/pyro` and `sudo systemctl restart pyro`.

Updates keep the data directory, so accounts, projects and plugins stay.

### If pulling the image fails

A new GitHub package is **private** until its owner makes it public (on GitHub: the
package's **Package settings → Change visibility**). Until then, log in first:
`docker login ghcr.io` with a GitHub token that can read packages, or add the registry
credentials in Dokploy.

### Load agents on other machines

[Load agents](distributed.md) use the **same image** (or binary), started as `pyro agent`. An
agent has no web UI: the team server sends it a share of each load test on port 7979.

| | Team server | Agent |
|---|---|---|
| Command | the default, `serve --team` | `agent` |
| Port | 7878: web UI and API | 7979: agent endpoints only |
| Settings | `PYRO_PUBLIC_URL`, `PYRO_DATABASE_URL`, … | `PYRO_AGENT_TOKEN` |
| Data | needed: database, projects, plugins | optional: installed plugins only |

On each load machine, with a long random token (`openssl rand -hex 32`):

```sh
docker run -d --name pyro-agent --restart unless-stopped \
  -p 7979:7979 -e PYRO_AGENT_TOKEN=<token> ghcr.io/codeen15/pyro:latest agent
```

Or `deploy/docker-compose.agent.yml` with `deploy/agent.env.example`, or
`deploy/pyro-agent.service` without Docker. In Dokploy, deploy the agent Compose file (or an
Application from the image with the command `agent`); give it its own domain on container
port **7979** with HTTPS, and the server reaches it as `https://agent1.example.com`.

Then start the team server with the agents (add these to its command or Compose `command:`):

```sh
pyro serve --team --agent 10.0.0.5:7979,https://agent1.example.com --agent-token <token>
```

and choose **Run on: N agents** next to **Start** on a load test.

- Use the same image tag (or release) on the server and its agents.
- Open port 7979 only to the team server (firewall or private network): anyone with the
  token can make an agent generate load.
- Plugins a test uses must be installed on every agent too.

## Company sign-in

Besides email and password, people can sign in with your company's accounts. Accounts are
created the first time someone signs in this way, with their personal workspace, whatever the
sign-up policy. The directory or identity provider decides who gets in.

### LDAP / Active Directory

With LDAP set up, the sign-in form asks for **email or username**:

- Accounts that have a Pyro password sign in with it as before.
- Anyone else is checked against the directory. Pyro binds with a service account, finds the
  person, and checks their password by binding as them.

```yaml
ldap:
  url: ldaps://dc1.corp.example.com:636      # or ldap://… with startTLS: true
  bindDN: CN=pyro-svc,OU=Service,DC=corp,DC=example,DC=com
  # bindPassword: set PYRO_LDAP_BIND_PASSWORD instead
  baseDN: OU=Staff,DC=corp,DC=example,DC=com
  userFilter: (sAMAccountName={username})    # default: uid, mail or sAMAccountName
  emailAttribute: mail                       # default mail
  nameAttribute: displayName                 # default displayName, then cn
  adminGroup: CN=Pyro Admins,OU=Groups,DC=corp,DC=example,DC=com   # optional
```

- `adminGroup` makes the group's members server administrators. Membership (`memberOf`) is
  checked at every sign-in, so removing someone from the group removes the role.
- The name, email and admin role are refreshed from the directory each time.
- If the directory can't be reached, people see that rather than "wrong password". The
  server log has the details.
- The settings can also come from the environment: `PYRO_LDAP_URL`, `PYRO_LDAP_BIND_DN`,
  `PYRO_LDAP_BIND_PASSWORD`, `PYRO_LDAP_BASE_DN`, `PYRO_LDAP_USER_FILTER` and
  `PYRO_LDAP_ADMIN_GROUP`.

### Single sign-on (OpenID Connect)

Each provider adds a **Sign in with …** button.

```yaml
oidc:
  - id: entra                       # used in the redirect URI
    name: Microsoft                 # the button says "Sign in with Microsoft"
    issuer: https://login.microsoftonline.com/<tenant-id>/v2.0
    clientID: 00000000-0000-0000-0000-000000000000
    # clientSecret: set PYRO_OIDC_ENTRA_SECRET instead
    allowedDomains: [example.com]   # optional: only these email domains
    trustEmail: true                # your tenant controls the addresses
    adminGroup: <group object id>   # optional: from the "groups" claim
  - id: google
    name: Google
    issuer: https://accounts.google.com
    clientID: 1234.apps.googleusercontent.com
```

- Register Pyro with the provider as a web app, with the redirect URI
  `<public URL>/auth/oidc/<id>/callback`. `pyro serve --team` prints it for each provider.
  Set `--public-url` so it matches what people open.
- The client secret goes in `PYRO_OIDC_<ID>_SECRET`, e.g. `PYRO_OIDC_ENTRA_SECRET`.
- Sign-in uses the authorization code flow with PKCE. Pyro checks the ID token's signature,
  audience, expiry and nonce.
- **Same email, existing account.** The sign-in joins that account only if the provider
  says the email is verified, or the provider has `trustEmail: true`. Entra ID doesn't send
  `email_verified`, so set `trustEmail` for your own tenant. Otherwise the person is asked to
  sign in the way they did before.
- An invite link works with SSO too: open it, click **Sign in with …**, and you join the
  workspace.
- Keycloak, Okta, Auth0 and others work the same way with their issuer URL.

## Administration

Server administrators find **Administration** in the account menu.

- **Users:** everyone with an account and how they sign in (password, LDAP or SSO).
  - **Administrator** switch.
  - **Active** switch. Turning it off signs the person out everywhere at once and stops their
    API tokens. Their workspaces and work are kept.
  - **Reset password** for password accounts: makes a one-time link valid for 24 hours. It's
    emailed when the server can send email, and shown so you can pass it on.
  - The server always keeps at least one active administrator.
- **Server:** how the server is set up (database, sign-up, LDAP, SSO, email). Change these in
  `server.yaml`, the environment or flags, and restart.

**Forgot password?** When email is set up, the sign-in page offers **Forgot password?**. It
emails a one-hour link to password accounts, and says the same thing whether or not the
address has an account.

## Managing accounts from the command line

Run these on the server machine. They work while the server is running.

```sh
pyro admin create-user alice@example.com --name "Alice"     # asks for a password
pyro admin create-user bob@example.com --admin
pyro admin reset-password alice@example.com                 # also signs her out everywhere
pyro admin set-admin bob@example.com --revoke
pyro admin disable carol@example.com                        # signs her out; enable undoes it
pyro admin users
```

Pass the same `--data` and `--database` as the server (or run it where the server's `.env`
is). A password can also be piped in, e.g. `echo "$PW" | pyro admin create-user …`. These
commands are also how you recover if nobody can reach the admin page.

## Your account

The **account menu** (the person icon, top right) has:

- **Account**:
  - change your name or password (password accounts). Changing your password signs out your
    other sessions;
  - manage your **API tokens** (below);
  - **Sign out everywhere**: ends your sessions on every browser and app.
- **Workspaces and projects**.
- **Administration** (administrators).
- **Sign out**.

You stay signed in for 14 days from your last visit. Behind the scenes, the browser holds a
short-lived access token (a JWT, renewed every 15 minutes) and a refresh token, both in
cookies that page scripts can't read. After 10 wrong passwords in 15 minutes, sign-in pauses
for that account.

## API tokens for scripts and CI

**Personal access tokens** are the easiest way:

1. Go to **Account → API tokens → New token**.
2. Name the token (e.g. "GitLab CI") and pick an expiry (30 days, 90 days, a year, or never).
3. Copy the token. It starts with `pyro_pat_` and is shown only once.

```sh
curl -H "Authorization: Bearer $PYRO_TOKEN" https://pyro.example.com/api/v1/workspaces
```

- A token acts as you, with your roles.
- The list shows when each token was last used. **Revoke** stops one at once.
- Tokens stop working if your account is disabled. They keep working after a password change
  or "sign out everywhere", so revoke them separately if one leaks.

Scripts can also sign in with a password and get short-lived tokens:

```sh
TOKEN=$(curl -s https://pyro.example.com/api/v1/auth/token \
  -d '{"email":"ci@example.com","password":"…"}' | jq -r .accessToken)
```

- That access token lasts 15 minutes.
- The response also carries a `refreshToken`. Trade it for a new access token at
  `/api/v1/auth/refresh`, and sign it out at `/api/v1/auth/logout`.
- This works for LDAP accounts too.
- Details are in the [API reference](../api.md).

The server signs sign-in tokens with a key in `<data>/jwt.secret`, made on first start. If
several servers share one database, give them all the same key in `PYRO_JWT_SECRET` (32
characters or more). Changing the key signs everyone out. Personal access tokens aren't
affected by the key.

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
