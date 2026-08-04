BIN := quality-review
PKG := ./cmd/quality-review
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
VERIFY_COMPARE_REF ?= $(shell git describe --tags --abbrev=0 HEAD^ 2>/dev/null)
LDFLAGS := -s -w -X main.version=$(VERSION)
PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64
RELEASE_BINARIES := $(foreach platform,$(PLATFORMS),$(BIN)_$(subst /,_,$(platform)))
LIVE_DATA_ROOT ?= $(HOME)/AiProject/code-quality-live
LIVE_WATCH_CRON ?= 17 2 * * *
LIVE_ADJUDICATE_CRON ?= 43 3 * * 1

.PHONY: build test qualification-test live-test mining-test release-check live-install live-uninstall dist clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

test:
	go test ./...
	$(MAKE) qualification-test
	$(MAKE) live-test
	$(MAKE) mining-test

qualification-test:
	python3 -m unittest discover -s pilot -p 'test_*.py' -v

live-test:
	python3 -m unittest discover -s pilot/live -p 'test_*.py' -v

mining-test:
	python3 -m unittest discover -s pilot/mining/tests -p 'test_*.py' -v

release-check: export CODE_QUALITY_RELEASE_TAG := $(VERSION)
release-check: test
	go vet ./...
	sh -n install.sh
	sh -n plugins/code-quality/scripts/bootstrap.sh
	@unformatted="$$(git ls-files '*.go' | xargs gofmt -l)"; \
		if [ -n "$$unformatted" ]; then echo "gofmt required:"; echo "$$unformatted"; exit 1; fi
	git diff --check
	git diff --cached --check
	@if [ -n "$(VERIFY_COMPARE_REF)" ]; then git diff --check "$(VERIFY_COMPARE_REF)..HEAD"; fi

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
dist: release-check
	rm -rf dist && mkdir -p dist
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "building $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
			go build -ldflags "$(LDFLAGS)" -o dist/$(BIN)_$${os}_$${arch} $(PKG); \
	done
	cp install.sh dist/install.sh
	cp plugins/code-quality/scripts/bootstrap.sh dist/bootstrap.sh
	cd dist && shasum -a 256 $(RELEASE_BINARIES) install.sh bootstrap.sh > checksums.txt
	@echo "dist ready (VERSION=$(VERSION)):" && ls dist

clean:
	rm -rf dist $(BIN)
