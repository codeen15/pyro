# Docker image for the team server (GHCR, Dokploy)

Status: approved · 2026-11-24 · size: small change

## Intent
Run `pyro serve --team` on the user's VPS under Dokploy (Traefik handles the domain and
HTTPS). The image is built by GitHub Actions and stored in GHCR. The database is whatever
`PYRO_DATABASE_URL` points at, possibly a PostgreSQL the user already runs elsewhere.

## Goals
- A small, non-root image of the team server; data on one volume.
- Multi-arch (amd64, arm64) images in `ghcr.io/codeen15/pyro`, built in CI.
- A Dokploy-ready compose file with **only the Pyro service**; every setting from the
  environment.
- Docs to deploy, update and back up.

## Non-goals
- No PostgreSQL, Caddy or Traefik in the compose file (Dokploy provides the proxy; the
  database is external and optional).
- No new Pyro features or settings: the team server already reads all its settings from
  `PYRO_*` environment variables.
- No healthcheck command (distroless has no shell/curl; Dokploy doesn't need one).

## Approaches
- **Chosen: multi-stage Dockerfile + GHCR workflow + Dokploy compose.** Fits Dokploy's
  compose/image deploys and its Traefik.
- Compose with Caddy for TLS: dropped, Dokploy's Traefik already does it.
- Release archives run directly on the VPS: dropped, loses Dokploy's deploy/restart/update.
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
3. `deploy/dokploy/docker-compose.yml`: one `pyro` service from
   `ghcr.io/codeen15/pyro:${PYRO_IMAGE_TAG:-latest}`, `expose: ["7878"]`, a named volume at
   `/data`, `restart: unless-stopped`, environment passed through:
   `PYRO_PUBLIC_URL`, `PYRO_DATABASE_URL` (empty = SQLite on the volume), `PYRO_JWT_SECRET`,
   `PYRO_SIGNUP`, `PYRO_SIGNUP_DOMAINS`, `PYRO_MAX_LOADS`, `PYRO_SMTP_*`, `PYRO_LDAP_*`.
   Plus `deploy/dokploy/.env.example` documenting each.
4. Docs: "Docker and Dokploy" section in `docs/guide/team-server.md` (create compose app,
   domain → port 7878, env, first account, `docker exec … /pyro admin`, update by tag,
   back up the volume, GHCR visibility note); `docs/releasing.md` (image tags);
   `CHANGELOG.md`; `CLAUDE.md` Layout (`Dockerfile`, `deploy/`).

## Constraints
- The image must start with only `/data` writable and no env set (SQLite fallback).
- No CGO; no change to Go code.
- GHCR packages start private: docs tell the user to make it public or add registry
  credentials in Dokploy.

## Left to the builder
- Exact Go image tag (match go.mod's go version), action versions, OCI labels.
