BIN      := bin/proxypilot
PKG      := github.com/hakanor/proxypilot
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.Date=$(DATE)

.PHONY: build web icons test lint tidy docs

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/proxypilot

# Builds the Svelte UI into internal/api/webdist.
web:
	cd web && npm ci && npm run build

# Copies the app icon (internal/brand/icon.svg) into the web UI and renders PNGs
# into docs/design/icons/png (macOS: uses qlmanage + sips).
icons:
	cp internal/brand/icon.svg web/public/icon.svg
	mkdir -p docs/design/icons/png
	qlmanage -t -s 1024 -o docs/design/icons/png internal/brand/icon.svg >/dev/null
	mv docs/design/icons/png/icon.svg.png docs/design/icons/png/icon-1024.png
	for s in 512 256 180 128 64 32 16; do sips -z $$s $$s docs/design/icons/png/icon-1024.png --out docs/design/icons/png/icon-$$s.png >/dev/null; done

test:
	go test ./...

lint:
	go vet ./...
	test -z "$$(gofmt -l .)"

tidy:
	go mod tidy

# Regenerates docs/cli.md from the built binary's --help output.
docs: build
	bash scripts/clidoc.sh > docs/cli.md
