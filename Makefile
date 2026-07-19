GO ?= go
GOFMT ?= gofmt
BINARY_DIR := bin
BINARY := $(BINARY_DIR)/stategeodb
FIND_GO_FILES := find . \( -path './.git' -o -path './bin' -o -path './dist' -o -path './tmp' -o -path './work' \) -prune -o -type f -name '*.go'

.DEFAULT_GOAL := help

.PHONY: help fmt fmt-check mod-check test test-race vet build check

help:
	@printf '%-12s %s\n' \
		'help' 'List available targets' \
		'fmt' 'Format repository Go files' \
		'fmt-check' 'Report unformatted Go files without changing them' \
		'mod-check' 'Check module consistency and list the module graph' \
		'test' 'Run the uncached test suite' \
		'test-race' 'Run the uncached race-enabled test suite' \
		'vet' 'Run Go static analysis' \
		'build' 'Build bin/stategeodb' \
		'check' 'Run all non-destructive foundation checks'

fmt:
	$(FIND_GO_FILES) -exec "$(GOFMT)" -w {} +

fmt-check:
	@files="$$($(FIND_GO_FILES) -exec "$(GOFMT)" -l {} +)" || exit $$?; \
	if [ -n "$$files" ]; then \
		printf '%s\n' "$$files"; \
		exit 1; \
	fi

mod-check:
	$(GO) mod tidy -diff
	$(GO) list -m all

test:
	$(GO) test -count=1 ./...

test-race:
	$(GO) test -race -count=1 ./...

vet:
	$(GO) vet ./...

build:
	@if [ -L "$(BINARY_DIR)" ]; then \
		printf '%s\n' 'refusing to build through symlink: $(BINARY_DIR)' >&2; \
		exit 1; \
	fi
	@if [ -L "$(BINARY)" ]; then \
		printf '%s\n' 'refusing to replace symlink: $(BINARY)' >&2; \
		exit 1; \
	fi
	mkdir -p "$(BINARY_DIR)"
	$(GO) build -o "$(BINARY)" ./cmd/stategeodb

check:
	@files="$$($(FIND_GO_FILES) -exec "$(GOFMT)" -l {} +)" || exit $$?; \
	if [ -n "$$files" ]; then \
		printf '%s\n' "$$files"; \
		exit 1; \
	fi
	$(GO) mod tidy -diff
	$(GO) list -m all
	$(GO) test -count=1 ./...
	$(GO) test -race -count=1 ./...
	$(GO) vet ./...
	@if [ -L "$(BINARY_DIR)" ]; then \
		printf '%s\n' 'refusing to build through symlink: $(BINARY_DIR)' >&2; \
		exit 1; \
	fi
	@if [ -L "$(BINARY)" ]; then \
		printf '%s\n' 'refusing to replace symlink: $(BINARY)' >&2; \
		exit 1; \
	fi
	mkdir -p "$(BINARY_DIR)"
	$(GO) build -o "$(BINARY)" ./cmd/stategeodb
