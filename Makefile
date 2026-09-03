# RED_MPUDP build and test targets.
# Unit / race / fuzz-smoke / bench / build never require root or ip/nft/tc.
# Only test-integration is privileged and Linux-only.

MODULE  := github.com/Realgamer7067/Red_MPUDP
BIN     := red-mpudp
GO      ?= go

# Reproducible stamps: DATE comes from the commit, never wall-clock time, so a
# rebuild from the same commit is byte-identical (see docs/development/toolchain.md).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell git log -1 --format=%cI 2>/dev/null || echo unknown)

LDFLAGS := -X $(MODULE)/internal/buildinfo.Version=$(VERSION) \
           -X $(MODULE)/internal/buildinfo.Commit=$(COMMIT) \
           -X $(MODULE)/internal/buildinfo.Date=$(DATE)

FUZZ_TIME    ?= 15s
BENCH_TIME   ?= 1x
INTEG_TAGS   ?= integration

.PHONY: help fmt vet test-unit test-race test-integration test-fuzz-smoke bench build tidy clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  %-18s %s\n", $$1, $$2}'

fmt: ## Format all Go files
	$(GO) fmt ./...

vet: ## Run go vet
	$(GO) vet ./...

test-unit: ## Run unit tests
	$(GO) test ./...

test-race: ## Run unit tests under the race detector
	$(GO) test -race ./...

test-integration: ## Run privileged namespace integration tests (Linux + root)
	@set -e; \
	if [ ! -d test ]; then \
		echo "test-integration: no test/ tree yet (added in M05)"; exit 0; fi; \
	if [ "$$(uname -s)" != "Linux" ]; then \
		echo "test-integration: requires Linux (found $$(uname -s))"; exit 1; fi; \
	if [ "$$(id -u)" != "0" ]; then \
		echo "test-integration: requires root / CAP_NET_ADMIN (run under sudo)"; exit 1; fi; \
	for t in ip nft tc; do \
		command -v $$t >/dev/null || { echo "test-integration: '$$t' not found"; exit 1; }; \
	done; \
	$(GO) test -tags $(INTEG_TAGS) ./test/...

test-fuzz-smoke: ## Run every fuzz target briefly (FUZZ_TIME each)
	@targets=$$(grep -rlE '^func Fuzz[A-Z]' --include='*_test.go' . || true); \
	if [ -z "$$targets" ]; then \
		echo "test-fuzz-smoke: no fuzz targets yet (added in M09)"; exit 0; fi; \
	for pkg in $$(echo "$$targets" | xargs -n1 dirname | sort -u); do \
		for f in $$(grep -hoE '^func (Fuzz[A-Za-z0-9_]+)' $$pkg/*_test.go | awk '{print $$2}'); do \
			echo ">> $$pkg $$f"; \
			$(GO) test "./$$pkg" -run '^$$' -fuzz "^$$f$$" -fuzztime $(FUZZ_TIME) || exit 1; \
		done; \
	done

bench: ## Run benchmarks once (BENCH_TIME)
	$(GO) test -run '^$$' -bench . -benchmem -benchtime $(BENCH_TIME) ./...

build: ## Build the reproducible release binary into bin/
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN) ./cmd/red-mpudp

tidy: ## Sync go.mod / go.sum
	$(GO) mod tidy

clean: ## Remove build output
	rm -rf bin
