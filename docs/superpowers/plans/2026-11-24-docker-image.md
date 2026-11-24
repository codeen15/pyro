# Docker Image Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let anyone deploy `pyro serve --team` anywhere: pull a small multi-arch image from `ghcr.io/codeen15/pyro` or build it themselves, with one compose file (pull or build), a systemd unit for VMs without Docker, and everything (including an optional external PostgreSQL) configured from the environment.

**Architecture:** A three-stage Dockerfile (Node builds the UI, Go cross-compiles a static binary, distroless runs it as non-root with data on `/data`). A GitHub Actions workflow builds amd64+arm64 with buildx and pushes to GHCR. A one-service compose file that can pull or build, a systemd unit for the release binary, and a guide with four recipes (pull, build, Dokploy, no Docker).

**Tech Stack:** Docker/buildx, distroless static, GitHub Actions (docker/* actions), Docker Compose.

**Spec:** `docs/superpowers/specs/2026-11-24-docker-image-design.md`

## Global Constraints
- No Go code changes; no CGO (`CGO_ENABLED=0`); no new Go dependencies.
- Final base: `gcr.io/distroless/static-debian12:nonroot` (uid 65532).
- Image env: `PYRO_DATA_DIR=/data`, `PYRO_PLUGINS_DIR=/data/plugins`, `PYRO_HOST=0.0.0.0`, `PYRO_PORT=7878`; `VOLUME /data`; `EXPOSE 7878`; `ENTRYPOINT ["/pyro"]`; `CMD ["serve","--team"]`.
- Image name `ghcr.io/codeen15/pyro`; tags: `v*` → `X.Y.Z`, `X.Y`, `latest`; `main` → `edge`; PRs build only.
- Compose: only the `pyro` service, with both `image:` and `build:`; `PYRO_DATABASE_URL` empty means SQLite on the volume; host port bound to `127.0.0.1` by default.
- Docs updated in the same change (CLAUDE.md docs rule).

## Review Focus
1. **First start on an empty named volume** (Docker creates it root-owned unless the image path is pre-owned): the non-root process must be able to write `/data`. Test in Task 1 (fresh volume → `/data/pyro.db` and `/data/jwt.secret` created).
2. **Upgrading the container keeps data and plugins**: plugins must live under `/data`. Test in Task 1 (`PYRO_PLUGINS_DIR` resolves under the volume: restart with the same volume keeps `jwt.secret` byte-identical).
3. **Running admin commands in a shell-less image**: `docker exec <c> /pyro admin …` must work against the same data dir and database. Test in Task 1 (`docker exec … /pyro admin users` or the equivalent list command exits 0).
4. **arm64 VPS**: the arm64 image must start, not just build. Test in Task 2 (buildx `--platform linux/arm64 --load`, then `docker run --platform linux/arm64 … /pyro version` under QEMU if available; otherwise `docker image inspect` shows `arm64` and the binary's ELF header is aarch64).
5. **Compose without a database URL**: the stack must start on SQLite. Test in Task 3 (`docker compose up` with only `PYRO_PUBLIC_URL` set → health ok).

---

### Task 1: Dockerfile and .dockerignore, with a smoke test

**Files:**
- Create: `Dockerfile`, `.dockerignore`, `scripts/docker-smoke.sh`
- Modify: `Makefile` (targets `docker` and `docker-smoke`)

**Interfaces:**
- Produces: build args `VERSION`, `COMMIT` (same meaning as the Makefile's); `make docker IMAGE=pyro:dev` builds `pyro:dev` for the host platform with VERSION/COMMIT from the Makefile; `scripts/docker-smoke.sh <image>` exits 0 when all checks pass.

- [ ] **Step 1: Write `scripts/docker-smoke.sh <image>`** (bash, `set -euo pipefail`, cleans up its container and volume on exit). Checks, each printing `ok: …` or failing with a message:
  - starts the image with a fresh named volume at `/data` and `-p 127.0.0.1:<free port>:7878`, polls `GET /api/v1/health` up to 30 s; body contains `"mode":"team"` and `"version"` is not empty and not `dev`
  - `docker exec <c> /pyro version` exits 0
  - container user is `65532` (`docker inspect -f '{{.Config.User}}'` is `nonroot` or `65532`, and `/data/pyro.db` and `/data/jwt.secret` exist: check with `docker run --rm -v <vol>:/data busybox ls -ln /data`, owner 65532)
  - restart the container with the same volume: `jwt.secret` sha256 unchanged (read via busybox)
  - `docker exec <c> /pyro admin --help` exits 0 (admin reaches the same env)
  - image size under 80 MB (`docker image inspect -f '{{.Size}}'`)
- [ ] **Step 2: Run it against a nonexistent image** — `bash scripts/docker-smoke.sh pyro:none` fails at the start step.
- [ ] **Step 3: Write `Dockerfile`** per Global Constraints and spec §1:
  - stage `ui`: `node:24-bookworm-slim`, `WORKDIR /src/web`, copy `web/package*.json` first, `npm ci`, copy `web/`, `npm run build` (output `web/dist`).
  - stage `build`: `golang:<go.mod version>-bookworm` with `--platform=$BUILDPLATFORM`; `ARG TARGETOS TARGETARCH VERSION=dev COMMIT=none`; copy `go.mod go.sum` and `go mod download` first; copy the repo; copy `web/dist` from `ui`; `CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X github.com/codeen15/pyro/internal/version.Version=$VERSION -X github.com/codeen15/pyro/internal/version.Commit=$COMMIT" -o /out/pyro ./cmd/pyro`; `mkdir -p /out/data/plugins`.
  - final: distroless static nonroot; `COPY --from=build /out/pyro /pyro`; `COPY --from=build --chown=65532:65532 /out/data /data` (so a fresh named volume is initialised non-root-owned); env/volume/expose/entrypoint/cmd per Global Constraints; OCI labels `org.opencontainers.image.source=https://github.com/codeen15/pyro`, `title`, `description`, `licenses` (from the repo's LICENSE).
  - `.dockerignore`: `.git`, `**/node_modules`, `bin`, `dist`, `web/dist`, `plugins/dist`, `.claude`, `.superpowers`, `**/*.test`, `web/test-results`, `web/playwright-report`.
  - Makefile: `docker:` → `docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(IMAGE) .` with `IMAGE ?= pyro:dev`; `docker-smoke: docker` → `bash scripts/docker-smoke.sh $(IMAGE)`. Add both to `.PHONY`.
- [ ] **Step 4: Run** `make docker-smoke` — all checks `ok`, exit 0. Note image size in the commit message body.
- [ ] **Step 5: Commit** `git commit -m "Docker image for the team server"`

### Task 2: GHCR workflow

**Files:**
- Create: `.github/workflows/docker.yml`

**Interfaces:**
- Consumes: Task 1's Dockerfile and build args; `scripts/docker-smoke.sh`.

- [ ] **Step 1: Write `.github/workflows/docker.yml`**:
  - `on`: `push` to `main` and tags `v*`; `pull_request`; `workflow_dispatch`.
  - `permissions: contents: read, packages: write`.
  - one job on `ubuntu-24.04`: checkout (`fetch-depth: 0` so `git describe` works); `docker/setup-qemu-action` (for the arm64 smoke run only); `docker/setup-buildx-action`; `docker/login-action` to `ghcr.io` with `${{ github.actor }}` / `${{ secrets.GITHUB_TOKEN }}`, skipped on `pull_request`; `docker/metadata-action` with images `ghcr.io/${{ github.repository }}` (lowercase) and tags `type=semver,pattern={{version}}`, `type=semver,pattern={{major}}.{{minor}}`, `type=raw,value=latest,enable=${{ startsWith(github.ref, 'refs/tags/v') }}`, `type=edge,branch=main`, `type=ref,event=pr`.
  - compute `VERSION` (`git describe --tags --match 'v[0-9]*' --always`) and `COMMIT` (`git rev-parse --short HEAD`) into step outputs.
  - smoke first: buildx `--load` for `linux/amd64` tagged `pyro:ci`, run `bash scripts/docker-smoke.sh pyro:ci`.
  - then `docker/build-push-action`: platforms `linux/amd64,linux/arm64`, build args, `push: ${{ github.event_name != 'pull_request' }}`, tags/labels from metadata, cache `type=gha` (from and to, `mode=max`).
- [ ] **Step 2: Validate**: `actionlint .github/workflows/docker.yml` if available (`go run github.com/rhysd/actionlint/cmd/actionlint@latest` is acceptable as a one-off, do not add it to go.mod); otherwise parse the YAML (`python3 -c 'import yaml,sys; yaml.safe_load(open(sys.argv[1]))'`).
- [ ] **Step 3: Verify arm64 locally**: `docker buildx build --platform linux/arm64 -t pyro:arm64 --load .` succeeds; `docker run --rm --platform linux/arm64 pyro:arm64 version` prints the version if QEMU binfmt is installed, otherwise `docker image inspect -f '{{.Architecture}}' pyro:arm64` is `arm64` (report which).
- [ ] **Step 4: Commit** `git commit -m "Publish the image to GHCR"`

### Task 3: Deploy kit (compose, env example, systemd) and docs

**Files:**
- Create: `deploy/docker-compose.yml`, `deploy/.env.example`, `deploy/pyro.service`, `deploy/docker-compose.agent.yml`, `deploy/agent.env.example`, `deploy/pyro-agent.service`
- Modify: `docs/guide/team-server.md` (new section "Deploying the team server" after "HTTPS"), `docs/releasing.md`, `CHANGELOG.md`, `CLAUDE.md` (Layout: `Dockerfile`, `deploy/`; Commands: `make docker`, `make docker-smoke`)

**Interfaces:**
- Consumes: the image name/tags (Task 2), env defaults and `/data` layout (Task 1).

- [ ] **Step 1: Write `deploy/docker-compose.yml`**: one service `pyro` with BOTH `image: ghcr.io/codeen15/pyro:${PYRO_IMAGE_TAG:-latest}` and `build: {context: .., args: {VERSION: "${PYRO_VERSION:-dev}"}}` (so `docker compose up -d` pulls and `docker compose up -d --build` builds from the checkout); `restart: unless-stopped`; `ports: ["${PYRO_BIND:-127.0.0.1}:${PYRO_HOST_PORT:-7878}:7878"]`; volume `pyro-data:/data`; `environment` passing through with `${VAR:-}` defaults: `PYRO_PUBLIC_URL`, `PYRO_DATABASE_URL`, `PYRO_JWT_SECRET`, `PYRO_SIGNUP`, `PYRO_SIGNUP_DOMAINS`, `PYRO_MAX_LOADS`, `PYRO_SMTP_HOST`, `PYRO_SMTP_PORT`, `PYRO_SMTP_USERNAME`, `PYRO_SMTP_PASSWORD`, `PYRO_SMTP_FROM`, `PYRO_LDAP_URL`, `PYRO_LDAP_BIND_DN`, `PYRO_LDAP_BIND_PASSWORD`, `PYRO_LDAP_BASE_DN`, `PYRO_LDAP_USER_FILTER`, `PYRO_LDAP_ADMIN_GROUP`, `PYRO_PLUGIN_INDEX`. Named volume `pyro-data`. A header comment: pull vs build commands, and that platforms like Dokploy route by container port 7878 (the host port mapping can be removed there).
- [ ] **Step 2: Write `deploy/.env.example`**: every variable above (plus `PYRO_IMAGE_TAG`, `PYRO_BIND`, `PYRO_HOST_PORT`) with a one-line comment; `PYRO_PUBLIC_URL=https://pyro.example.com`; `PYRO_DATABASE_URL=` with a comment showing `postgres://user:pass@host:5432/pyro?sslmode=require` and "empty = SQLite on the volume"; `PYRO_JWT_SECRET=` with "at least 32 bytes; `openssl rand -hex 32`; empty = generated and kept in /data"; `PYRO_BIND=127.0.0.1` with "0.0.0.0 to expose directly (then use HTTPS: see the guide)".
- [ ] **Step 3: Write `deploy/pyro.service`** (systemd): `User=pyro`, `Group=pyro`, `ExecStart=/usr/local/bin/pyro serve --team`, `EnvironmentFile=-/etc/pyro/pyro.env`, `Environment=PYRO_DATA_DIR=/var/lib/pyro PYRO_PLUGINS_DIR=/var/lib/pyro/plugins PYRO_HOST=127.0.0.1 PYRO_PORT=7878`, `StateDirectory=pyro`, `Restart=on-failure`, `NoNewPrivileges=yes`, `ProtectSystem=strict`, `ProtectHome=yes`, `PrivateTmp=yes`, `ReadWritePaths=/var/lib/pyro`, `[Install] WantedBy=multi-user.target`; header comment with the install commands (useradd, copy binary from the release archive, copy unit, create `/etc/pyro/pyro.env` from `.env.example`, `systemctl enable --now pyro`). Check the release archive layout in `docs/releasing.md` / `scripts/dist` so the copy command names the right file.
- [ ] **Step 3b: Agent role**: `deploy/docker-compose.agent.yml`: service `pyro-agent`, same `image:`/`build:` as the server, `command: ["agent"]`, `restart: unless-stopped`, `ports: ["${PYRO_AGENT_BIND:-0.0.0.0}:${PYRO_AGENT_PORT:-7979}:7979"]`, env `PYRO_AGENT_TOKEN` (required: `${PYRO_AGENT_TOKEN:?set PYRO_AGENT_TOKEN}`), optional volume `pyro-agent-data:/data` (plugins live in `/data/plugins`). Header comment: Dokploy can route it through Traefik instead (domain → container port 7979, then remove `ports`). `deploy/agent.env.example`: `PYRO_AGENT_TOKEN` ("long random; `openssl rand -hex 32`; give the same token to the server's `--agent-token`"), `PYRO_IMAGE_TAG` ("match the server's tag"), `PYRO_AGENT_BIND`, `PYRO_AGENT_PORT`. `deploy/pyro-agent.service`: like `pyro.service` but `ExecStart=/usr/local/bin/pyro agent`, `EnvironmentFile=-/etc/pyro/agent.env`, `StateDirectory=pyro-agent`, `PYRO_PLUGINS_DIR=/var/lib/pyro-agent/plugins`.
- [ ] **Step 4: Verify**:
  - `docker compose -f deploy/docker-compose.yml config` succeeds with no env set.
  - Pull path (simulated with the local image): `docker tag pyro:dev ghcr.io/codeen15/pyro:local`; `PYRO_IMAGE_TAG=local PYRO_HOST_PORT=<free> docker compose -p pyro-smoke -f deploy/docker-compose.yml up -d --no-build`; `curl -fsS http://127.0.0.1:<free>/api/v1/health` shows `"mode":"team"` (Review Focus #5: no database URL → SQLite); `docker compose -p pyro-smoke -f deploy/docker-compose.yml down -v`.
  - Build path: `PYRO_IMAGE_TAG=built docker compose -p pyro-smoke -f deploy/docker-compose.yml build` succeeds (no need to run it).
  - Agent: `PYRO_AGENT_TOKEN=t PYRO_IMAGE_TAG=local PYRO_AGENT_PORT=<free> docker compose -p pyro-agent-smoke -f deploy/docker-compose.agent.yml up -d --no-build`; `curl -fsS -H 'Authorization: Bearer t' -H 'X-Pyro-Agent-Protocol: <value from pkg/engine/agent>' http://127.0.0.1:<free>/agent/v1/info` returns JSON (check the exact protocol header name/value in pkg/engine/agent/server.go); then a real distributed run: `./bin/pyro load` (or the e2e-style sample workspace) with `--agent 127.0.0.1:<free> --agent-token t` against a local httptest-like target (e.g. a `python3 -m http.server` on loopback) completes. Then down -v.
  - Agent behind Traefik (proves the Dokploy route): a throwaway compose in the scratchpad with `traefik:v3` (docker provider, entrypoint :<free2>) and the agent labelled with a Host rule (`agent.localhost`) → port 7979; `pyro load … --agent http://agent.localhost:<free2> --agent-token t` completes (WebSocket job through Traefik). Report the result; if it fails, document the direct-port route as the supported one and say why.
  - systemd units: `systemd-analyze verify deploy/pyro.service deploy/pyro-agent.service` if available (warnings about the missing binary/user are acceptable; report output); otherwise state it was not verifiable.
- [ ] **Step 5: Write the docs**: team-server.md section "Deploying the team server" with four recipes, each a few lines:
  1. **Pull the image**: image name and tags (`latest`, `X.Y.Z`, `X.Y`, `edge`); minimal `docker run -d --name pyro -v pyro-data:/data -p 127.0.0.1:7878:7878 -e PYRO_PUBLIC_URL=https://… ghcr.io/codeen15/pyro:latest`; or `cp deploy/.env.example deploy/.env`, edit, `docker compose -f deploy/docker-compose.yml up -d`.
  2. **Build it yourself**: `make docker` (or `docker build -t pyro .`), or `docker compose -f deploy/docker-compose.yml up -d --build`.
  3. **Dokploy** (and similar platforms): create a Compose service from the repo (`deploy/docker-compose.yml`) or the image; Environment tab from `.env.example`; Domains: host, container port 7878, HTTPS on; Deploy; registry credentials if the GHCR package is private.
  4. **A VM without Docker**: download the release archive, install the binary, `deploy/pyro.service`, `/etc/pyro/pyro.env`.
  Then "Load agents on other machines": same image, command `agent` (table: server vs agent: command, port, settings, volume, database, proxy); Docker `docker run … agent`, `deploy/docker-compose.agent.yml`, Dokploy (Compose with the agent file, or Application with command override `agent`; via a domain on container port 7979 with HTTPS, listed as `https://agent1.example.com`, or a published port firewalled to the server), systemd `deploy/pyro-agent.service`; on the server `PYRO`-side `--agent …,… --agent-token …` (note: managing agents from the UI is planned); keep image tags matched; plugins must be installed on agents; security (token, firewall 7979 to the server only).
  Then shared subsections: first account; HTTPS (reverse proxy with `PYRO_PUBLIC_URL=https://…`, e.g. Caddy one-liner `reverse_proxy 127.0.0.1:7878`, nginx/Traefik; or `PYRO_TLS_CERT`/`PYRO_TLS_KEY` with `PYRO_BIND=0.0.0.0`); external PostgreSQL via `PYRO_DATABASE_URL` (reachable from the container; `sslmode`); admin commands (`docker exec pyro /pyro admin …` / `sudo -u pyro pyro admin …` with the same env); updating (new tag / `docker compose pull && up -d` / replace binary + restart); backups (the `pyro-data` volume or `/var/lib/pyro`, plus `pg_dump`); GHCR visibility (private by default: make public or `docker login ghcr.io`). `docs/releasing.md`: the docker workflow and image tags. `CHANGELOG.md` Unreleased: one line. `CLAUDE.md` as listed.
- [ ] **Step 6: Run** `make test` and commit `git commit -m "Deploy kit (compose, systemd) and deployment docs"`
