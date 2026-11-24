# Deploying the team server anywhere (Docker image, GHCR, compose, systemd)

Status: approved · 2026-11-24 · size: small change

## Intent
Let anyone run the team server anywhere: any VM with Docker, Dokploy (or similar), or a VM
without Docker. Users choose to pull the published image (GHCR, built by GitHub Actions) or
build it themselves. The database is whatever `PYRO_DATABASE_URL` points at, possibly a
PostgreSQL the user already runs elsewhere; empty means SQLite on the data volume.

## Goals
- A small, non-root image of the team server; data on one volume.
- Multi-arch (amd64, arm64) images in `ghcr.io/codeen15/pyro`, built in CI.
- One generic compose file with **only the Pyro service** that can either pull the image
  or build it from source; every setting from the environment. Works on a plain VM and in
  Dokploy/Portainer/Coolify.
- A systemd unit for VMs without Docker (release binary).
- Docs: four recipes (pull, build yourself, Dokploy, no Docker) plus HTTPS and database
  notes, update and backup.

## Non-goals
- No PostgreSQL, Caddy or Traefik in the compose file (the platform or the user provides
  the proxy; the database is external and optional).
- No new Pyro features or settings: the team server already reads all its settings from
  `PYRO_*` environment variables.
- No healthcheck command (distroless has no shell/curl; Dokploy doesn't need one).

## Approaches
- **Chosen: multi-stage Dockerfile + GHCR workflow + generic compose (image + build) +
  systemd unit.** One kit for every target; Dokploy is one recipe, not a special case.
- Dokploy-only compose: dropped at the user's request (must deploy anywhere).
- Compose bundling Caddy for TLS: dropped; HTTPS depends on where it runs (Dokploy's
  Traefik, an existing proxy, or `--tls-cert`), so the docs cover the options instead.
- Compose bundling PostgreSQL: dropped at the user's request (DB may already exist).

## Design
1. `Dockerfile` (repo root), three stages:
   - `node:24` builds `web/` (`npm ci`, `npm run build`).
   - `golang` (version from go.mod) on `$BUILDPLATFORM` cross-compiles
     `CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH`, `-ldflags` as `make build`
     (`VERSION`, `COMMIT` build args).
   - `gcr.io/distroless/static-debian12:nonroot`: `/pyro`; `ENV PYRO_DATA_DIR=/data
     PYRO_PLUGINS_DIR=/data/plugins PYRO_HOST=0.0.0.0 PYRO_PORT=7878`; `VOLUME /data`
     (owned by nonroot); `EXPOSE 7878`; `ENTRYPOINT ["/pyro"]`, `CMD ["serve","--team"]`.
   - `.dockerignore` for node_modules, bin, dist, web/dist, plugins/dist, .git, .claude,
     .superpowers.
2. `.github/workflows/docker.yml`: buildx, amd64+arm64; `v*` tags → `:X.Y.Z`, `:X.Y`,
   `:latest`; `main` → `:edge`; `workflow_dispatch`; pull requests build without
   pushing. `GITHUB_TOKEN` with `packages: write`; OCI labels via metadata-action.
3. `deploy/docker-compose.yml`: one `pyro` service with both
   `image: ghcr.io/codeen15/pyro:${PYRO_IMAGE_TAG:-latest}` and `build: {context: ..}`
   (pull with `docker compose up -d`, build with `docker compose up -d --build`),
   `ports: ["${PYRO_BIND:-127.0.0.1}:${PYRO_HOST_PORT:-7878}:7878"]` (loopback by default so a
   host proxy fronts it; platforms like Dokploy route by container port), a named volume at
   `/data`, `restart: unless-stopped`, environment passed through:
   `PYRO_PUBLIC_URL`, `PYRO_DATABASE_URL` (empty = SQLite on the volume), `PYRO_JWT_SECRET`,
   `PYRO_SIGNUP`, `PYRO_SIGNUP_DOMAINS`, `PYRO_MAX_LOADS`, `PYRO_SMTP_*`, `PYRO_LDAP_*`,
   `PYRO_PLUGIN_INDEX`. Plus `deploy/.env.example` documenting each.
4. `deploy/pyro.service`: systemd unit running `/usr/local/bin/pyro serve --team` as user
   `pyro`, `EnvironmentFile=/etc/pyro/pyro.env`, `PYRO_DATA_DIR=/var/lib/pyro`,
   `PYRO_PLUGINS_DIR=/var/lib/pyro/plugins`, `StateDirectory=pyro`, restart on failure,
   basic hardening (`NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`).
5. Docs: "Deploying the team server" section in `docs/guide/team-server.md`: recipes
   (1) pull the image (`docker run`, compose), (2) build it yourself (`make docker`,
   `docker compose build`), (3) Dokploy (compose service, domain → container port 7878,
   env from `.env.example`), (4) VM without Docker (release binary + `deploy/pyro.service`);
   then HTTPS (reverse proxy with `PYRO_PUBLIC_URL=https://…`, or `--tls-cert`), external
   PostgreSQL, admin commands, updates, backups, GHCR visibility note. `docs/releasing.md`
   (image tags); `CHANGELOG.md`; `CLAUDE.md` Layout (`Dockerfile`, `deploy/`).

## Constraints
- The image must start with only `/data` writable and no env set (SQLite fallback).
- No CGO; no change to Go code.
- GHCR packages start private: docs tell the user to make it public or log in
  (`docker login ghcr.io`, or registry credentials in Dokploy).
- The systemd unit must work with the existing release archive layout (binary named
  `pyro`); check `docs/releasing.md` for the archive contents.

## Left to the builder
- Exact Go image tag (match go.mod's go version), action versions, OCI labels.
