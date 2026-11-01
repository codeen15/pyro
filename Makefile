VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS := -s -w -X github.com/pyrohq/pyro/internal/version.Version=$(VERSION) -X github.com/pyrohq/pyro/internal/version.Commit=$(COMMIT)

.PHONY: all build ui ui-deps test vet dev-api dev-ui clean

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
	cd web && npm run typecheck

vet:
	go vet ./...

# Development: run these in two terminals, then open http://localhost:5173
dev-api:
	go run ./cmd/pyro serve --no-token

dev-ui: ui-deps
	cd web && npm run dev

clean:
	rm -rf bin
	find web/dist -mindepth 1 ! -name .gitkeep -delete
