SHELL := /usr/bin/env bash
PATH := $(CURDIR)/.tools/bin:$(PATH)

BIN := quality-review
PKG := ./cmd/quality-review
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
VERIFY_COMPARE_REF ?=
LDFLAGS := -s -w -X main.version=$(VERSION)
LIVE_DATA_ROOT ?= $(HOME)/AiProject/code-quality-live
LIVE_WATCH_CRON ?= 17 2 * * *
LIVE_ADJUDICATE_CRON ?= 43 3 * * 1

.PHONY: build test qualification-test live-test mining-test delivery-test release-check live-install live-uninstall dist clean
.PHONY: install-tools verify-change verify-candidate verify-release verify-suite

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

test:
	scripts/with_test_resources.sh -- go test ./...
	$(MAKE) qualification-test
	$(MAKE) live-test
	$(MAKE) mining-test
	$(MAKE) delivery-test

qualification-test:
	python3 -m unittest discover -s pilot -p 'test_*.py' -v

live-test:
	python3 -m unittest discover -s pilot/live -p 'test_*.py' -v

mining-test:
	python3 -m unittest discover -s pilot/mining/tests -p 'test_*.py' -v

delivery-test:
	python3 -I -B -S -m unittest discover -s harness -p 'test_delivery_*.py' -v

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

install-tools:
	scripts/harnessctl.sh install-tools

verify-change:
	harness/repository_verification.py verify change

verify-candidate:
	harness/repository_verification.py verify candidate

verify-release:
	harness/repository_verification.py verify release

verify-suite:
	scripts/verify_suite.sh

live-install:
	CODE_QUALITY_LIVE_ROOT="$(LIVE_DATA_ROOT)" \
	LIVE_WATCH_CRON="$(LIVE_WATCH_CRON)" \
	LIVE_ADJUDICATE_CRON="$(LIVE_ADJUDICATE_CRON)" \
	/bin/zsh pilot/live/live_watch.sh --install --data-root "$(LIVE_DATA_ROOT)"

live-uninstall:
	CODE_QUALITY_LIVE_ROOT="$(LIVE_DATA_ROOT)" \
	/bin/zsh pilot/live/live_watch.sh --uninstall --data-root "$(LIVE_DATA_ROOT)"

# One complete release verification, followed by packaging the approved SHA.
# The native release-check is invoked only by the Harness release profile.
dist:
	python3 -I -B -S scripts/release.py --version "$(VERSION)" --compare-ref "$(VERIFY_COMPARE_REF)"

clean:
	rm -rf dist $(BIN)

.PHONY: verify
verify: verify-change
