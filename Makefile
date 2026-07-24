BIN := quality-review
PKG := ./cmd/quality-review
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64

.PHONY: build test dist clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

test:
	go test ./...

# Cross-compile every release binary into dist/, write checksums, and stage
# the installer as a release asset. Run: make dist VERSION=vX.Y.Z
dist:
	rm -rf dist && mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "building $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
			go build -ldflags "$(LDFLAGS)" -o dist/$(BIN)_$${os}_$${arch} $(PKG); \
	done
	cp install.sh dist/install.sh
	cd dist && shasum -a 256 $(BIN)_* > checksums.txt
	@echo "dist ready (VERSION=$(VERSION)):" && ls dist

clean:
	rm -rf dist $(BIN)
