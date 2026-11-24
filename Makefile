VERSION ?= $(shell git describe --tags --match 'v[0-9]*' --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS := -s -w -X github.com/codeen15/pyro/internal/version.Version=$(VERSION) -X github.com/codeen15/pyro/internal/version.Commit=$(COMMIT)

.PHONY: all build ui ui-deps test vet dev-api dev-ui plugins jsrt dist docker docker-smoke clean

# Docker image (see Dockerfile); IMAGE=name:tag to change the tag.
IMAGE ?= pyro:dev

all: build

# Full build: UI first (it is embedded), then the Go binary.
build: ui
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/pyro ./cmd/pyro

ui: ui-deps
	cd web && npm run build
	touch web/dist/.gitkeep

ui-deps:
	@[ -d web/node_modules ] || (cd web && npm ci)

test: vet
	go test ./...
	cd plugins/sdk/go && go test ./...
	cd plugins/runtime/js && go vet ./... && go test ./...
	cd web && npm run typecheck

vet:
	go vet ./...

# Development: run these in two terminals, then open http://localhost:5173
dev-api:
	go run ./cmd/pyro serve --no-token

dev-ui: ui-deps
	cd web && npm run dev

# Build and pack the example plugins into plugins/dist.
plugins: build
	mkdir -p plugins/dist
	for p in tcp hmac; do \
		(cd plugins/examples/$$p && GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -ldflags="-s -w" -o $$p.wasm .) && \
		./bin/pyro plugin pack plugins/examples/$$p -o plugins/dist/$$p-0.1.0.pyro-plugin; \
	done
	./bin/pyro plugin pack plugins/examples/redis-js -o plugins/dist/redis-js-0.1.0.pyro-plugin

# The JavaScript plugin runtime, embedded in the engine. The build is
# reproducible (CI checks the committed file is up to date): it uses the Go
# version named in the runtime's go.mod, no build ID, no VCS stamp (it would
# change with every commit) and a gzip without a timestamp.
jsrt:
	@set -e; tmp=$$(mktemp); trap 'rm -f "$$tmp"' EXIT; \
	gover=go$$(awk '/^go /{print $$2}' plugins/runtime/js/go.mod); \
	(cd plugins/runtime/js && GOTOOLCHAIN=$$gover CGO_ENABLED=0 GOOS=wasip1 GOARCH=wasm \
		go build -trimpath -buildvcs=false -buildmode=c-shared -ldflags="-s -w -buildid=" -o "$$tmp" .); \
	gzip -n -9 -c "$$tmp" > pkg/engine/plugin/jsrt.wasm.gz

docker:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(IMAGE) .

# Builds the image, then checks it starts, runs as nonroot and keeps its data.
docker-smoke: docker
	bash scripts/docker-smoke.sh $(IMAGE)

clean:
	rm -rf bin dist
	find web/dist -mindepth 1 ! -name .gitkeep -delete

# Release archives for every OS/arch, example plugins and SHA256SUMS in dist/.
# VERSION defaults to git describe; CI passes the tag.
dist: ui
	go run ./scripts/dist -version $(VERSION)
