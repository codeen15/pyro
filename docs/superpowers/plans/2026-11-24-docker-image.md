# Docker Image Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `pyro serve --team` as a small multi-arch image in `ghcr.io/codeen15/pyro`, with a Dokploy compose file that configures everything (including an optional external PostgreSQL) from the environment.

**Architecture:** A three-stage Dockerfile (Node builds the UI, Go cross-compiles a static binary, distroless runs it as non-root with data on `/data`). A GitHub Actions workflow builds amd64+arm64 with buildx and pushes to GHCR. A one-service compose file for Dokploy, whose Traefik handles the domain and HTTPS.

**Tech Stack:** Docker/buildx, distroless static, GitHub Actions (docker/* actions), Docker Compose.

**Spec:** `docs/superpowers/specs/2026-11-24-docker-image-design.md`

## Global Constraints
- No Go code changes; no CGO (`CGO_ENABLED=0`); no new Go dependencies.
- Final base: `gcr.io/distroless/static-debian12:nonroot` (uid 65532).
- Image env: `PYRO_DATA_DIR=/data`, `PYRO_PLUGINS_DIR=/data/plugins`, `PYRO_HOST=0.0.0.0`, `PYRO_PORT=7878`; `VOLUME /data`; `EXPOSE 7878`; `ENTRYPOINT ["/pyro"]`; `CMD ["serve","--team"]`.
- Image name `ghcr.io/codeen15/pyro`; tags: `v*` → `X.Y.Z`, `X.Y`, `latest`; `main` → `edge`; PRs build only.
- Compose: only the `pyro` service; `PYRO_DATABASE_URL` empty means SQLite on the volume.
- Docs updated in the same change (CLAUDE.md docs rule).

## Review Focus
1. **First start on an empty named volume** (Docker creates it root-owned unless the image path is pre-owned): the non-root process must be able to write `/data`. Test in Task 1 (fresh volume → `/data/pyro.db` and `/data/jwt.secret` created).
2. **Upgrading the container keeps data and plugins**: plugins must live under `/data`. Test in Task 1 (`PYRO_PLUGINS_DIR` resolves under the volume: restart with the same volume keeps `jwt.secret` byte-identical).
3. **Running admin commands in a shell-less image**: `docker exec <c> /pyro admin …` must work against the same data dir and database. Test in Task 1 (`docker exec … /pyro admin users` or the equivalent list command exits 0).
4. **arm64 VPS**: the arm64 image must start, not just build. Test in Task 2 (buildx `--platform linux/arm64 --load`, then `docker run --platform linux/arm64 … /pyro version` under QEMU if available; otherwise `docker image inspect` shows `arm64` and the binary's ELF header is aarch64).
5. **Dokploy compose without a database URL**: the stack must start on SQLite. Test in Task 3 (`docker compose up` with only `PYRO_PUBLIC_URL` set → health ok).

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

### Task 3: Dokploy compose, env example, docs

**Files:**
- Create: `deploy/dokploy/docker-compose.yml`, `deploy/dokploy/.env.example`
- Modify: `docs/guide/team-server.md` (new section "Docker and Dokploy" after "HTTPS"), `docs/releasing.md`, `CHANGELOG.md`, `CLAUDE.md` (Layout: `Dockerfile`, `deploy/dokploy`; Commands: `make docker`, `make docker-smoke`)

**Interfaces:**
- Consumes: the image name/tags (Task 2), env defaults (Task 1).

- [ ] **Step 1: Write `deploy/dokploy/docker-compose.yml`**: one service `pyro`, `image: ghcr.io/codeen15/pyro:${PYRO_IMAGE_TAG:-latest}`, `restart: unless-stopped`, `expose: ["7878"]`, volume `pyro-data:/data`, `environment` passing through (with `${VAR:-}` defaults so unset is empty): `PYRO_PUBLIC_URL`, `PYRO_DATABASE_URL`, `PYRO_JWT_SECRET`, `PYRO_SIGNUP`, `PYRO_SIGNUP_DOMAINS`, `PYRO_MAX_LOADS`, `PYRO_SMTP_HOST`, `PYRO_SMTP_PORT`, `PYRO_SMTP_USERNAME`, `PYRO_SMTP_PASSWORD` (check the exact SMTP password variable name in cmd/pyro/team.go and docs/guide/team-server.md; use what Pyro reads), `PYRO_SMTP_FROM`, `PYRO_LDAP_*` (the names Pyro reads), `PYRO_PLUGIN_INDEX`. Named volume `pyro-data`. No ports, no networks (Dokploy attaches its own).
- [ ] **Step 2: Write `deploy/dokploy/.env.example`**: every variable above with a one-line comment; `PYRO_PUBLIC_URL=https://pyro.example.com`; `PYRO_DATABASE_URL=` with a comment showing `postgres://user:pass@host:5432/pyro?sslmode=require` and "empty = SQLite on the volume"; `PYRO_JWT_SECRET=` with "at least 32 bytes; `openssl rand -hex 32`; empty = generated and kept in /data".
- [ ] **Step 3: Verify**: `docker compose -f deploy/dokploy/docker-compose.yml config` succeeds with no env set; then with `PYRO_IMAGE_TAG` pointing at the locally built image (`docker tag pyro:dev ghcr.io/codeen15/pyro:local` and `PYRO_IMAGE_TAG=local`), `docker compose -p pyro-smoke -f deploy/dokploy/docker-compose.yml up -d`, health check from a throwaway container on the project network (`docker run --rm --network pyro-smoke_default curlimages/curl -fsS http://pyro:7878/api/v1/health` shows `"mode":"team"`), then `docker compose -p pyro-smoke down -v`.
- [ ] **Step 4: Write the docs**: team-server.md section "Docker and Dokploy": image name and tags; minimal `docker run` (`-v pyro-data:/data -p 7878:7878 -e PYRO_PUBLIC_URL=…`); Dokploy steps (new Compose service → paste or point at `deploy/dokploy/docker-compose.yml` → Environment tab from `.env.example` → Domains: host, container port 7878, HTTPS on → Deploy → open the URL and create the first account); external PostgreSQL via `PYRO_DATABASE_URL` (reachable from the container; `sslmode`); admin commands `docker exec <container> /pyro admin …`; update by changing `PYRO_IMAGE_TAG` or redeploying `latest`; back up the `pyro-data` volume (and pg_dump when using PostgreSQL); GHCR visibility (package private by default → make public, or add registry credentials in Dokploy). `docs/releasing.md`: the docker workflow and tags. `CHANGELOG.md` Unreleased: one line. `CLAUDE.md` as listed.
- [ ] **Step 5: Run** `make test` (docs-only Go impact; confirms nothing else broke) and commit `git commit -m "Dokploy compose and Docker docs"`
