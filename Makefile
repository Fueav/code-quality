BIN := quality-review
PKG := ./cmd/quality-review
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64
LIVE_DATA_ROOT ?= $(HOME)/AiProject/code-quality-live
LIVE_WATCH_CRON ?= 17 2 * * *
LIVE_ADJUDICATE_CRON ?= 43 3 * * 1

.PHONY: build test live-test live-install live-uninstall dist clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

test:
	go test ./...

live-test:
	python3 -m unittest discover -s pilot/live -p 'test_*.py' -v

live-install:
	CODE_QUALITY_LIVE_ROOT="$(LIVE_DATA_ROOT)" \
	LIVE_WATCH_CRON="$(LIVE_WATCH_CRON)" \
	LIVE_ADJUDICATE_CRON="$(LIVE_ADJUDICATE_CRON)" \
	/bin/zsh pilot/live/live_watch.sh --install --data-root "$(LIVE_DATA_ROOT)"

live-uninstall:
	CODE_QUALITY_LIVE_ROOT="$(LIVE_DATA_ROOT)" \
	/bin/zsh pilot/live/live_watch.sh --uninstall --data-root "$(LIVE_DATA_ROOT)"

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
