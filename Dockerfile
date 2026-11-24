# Pyro: the team server (default) or a load agent (`agent`) in one small image.
#
#   docker build -t pyro .                       # or: make docker
#   docker run -v pyro-data:/data -p 7878:7878 pyro
#   docker run -p 7979:7979 -e PYRO_AGENT_TOKEN=… pyro agent
#
# See docs/guide/team-server.md, "Deploying the team server".

# The web UI, embedded into the binary.
FROM node:24-bookworm-slim AS ui
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# The binary, cross-compiled on the build machine (no emulation for arm64).
FROM --platform=$BUILDPLATFORM golang:1.27.3-bookworm AS build
ARG TARGETOS TARGETARCH VERSION=dev COMMIT=none
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X github.com/codeen15/pyro/internal/version.Version=$VERSION -X github.com/codeen15/pyro/internal/version.Commit=$COMMIT" \
      -o /out/pyro ./cmd/pyro \
 && mkdir -p /out/data/plugins

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.title="Pyro" \
      org.opencontainers.image.description="API client and load-testing tool: team server and load agent" \
      org.opencontainers.image.source="https://github.com/codeen15/pyro" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY --from=build /out/pyro /pyro
# Pre-owned by the nonroot user, so a fresh named volume mounted here is writable.
COPY --from=build --chown=65532:65532 /out/data /data
ENV PYRO_DATA_DIR=/data \
    PYRO_PLUGINS_DIR=/data/plugins \
    PYRO_HOST=0.0.0.0 \
    PYRO_PORT=7878
VOLUME /data
EXPOSE 7878 7979
ENTRYPOINT ["/pyro"]
CMD ["serve", "--team"]
