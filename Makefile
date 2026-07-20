GO ?= go
GOFMT ?= gofmt
DIST_DIR := dist
DIST_BIN_DIR := $(DIST_DIR)/bin
DIST_ARTIFACT_DIR := $(DIST_DIR)/artifacts
BINARY := $(DIST_BIN_DIR)/stategeodb
ARTIFACT := $(DIST_ARTIFACT_DIR)/stategeodb.mmdb
FIND_GO_FILES := find . \( -path './.git' -o -path './bin' -o -path './dist' -o -path './tmp' -o -path './work' \) -prune -o -type f -name '*.go'

# Keep a command-line candidate path as data during Make and shell expansion.
override CANDIDATE := $(value CANDIDATE)
export CANDIDATE

.DEFAULT_GOAL := help

.PHONY: help fmt fmt-check mod-check test test-race vet build artifact-path publish-artifact inspect-artifact check

help:
	@printf '%-18s %s\n' \
		'help' 'List available targets' \
		'fmt' 'Format repository Go files' \
		'fmt-check' 'Report unformatted Go files without changing them' \
		'mod-check' 'Check module consistency and list the module graph' \
		'test' 'Run the uncached test suite' \
		'test-race' 'Run the uncached race-enabled test suite' \
		'vet' 'Run Go static analysis' \
		'build' 'Build the CLI executable at $(BINARY)' \
		'artifact-path' 'Print the default local artifact path' \
		'publish-artifact' 'Publish CANDIDATE from stategeodb build to the default artifact' \
		'inspect-artifact' 'Inspect metadata for the default published artifact' \
		'check' 'Run all non-destructive validation checks'

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
	@if [ -L "$(DIST_DIR)" ]; then \
		printf '%s\n' 'refusing to build through symlink: $(DIST_DIR)' >&2; \
		exit 1; \
	fi
	@if [ -L "$(DIST_BIN_DIR)" ]; then \
		printf '%s\n' 'refusing to build through symlink: $(DIST_BIN_DIR)' >&2; \
		exit 1; \
	fi
	@if [ -L "$(BINARY)" ]; then \
		printf '%s\n' 'refusing to replace symlink: $(BINARY)' >&2; \
		exit 1; \
	fi
	@mkdir -p "$(DIST_BIN_DIR)"
	@$(GO) build -o "$(BINARY)" ./cmd/stategeodb

artifact-path:
	@printf '%s\n' '$(ARTIFACT)'

publish-artifact: build
	@if [ -z "$$CANDIDATE" ]; then \
		printf '%s\n' 'stategeodb: CANDIDATE is required for publish-artifact' >&2; \
		exit 1; \
	fi
	@if [ -L "$(DIST_DIR)" ]; then \
		printf '%s\n' 'refusing to publish through symlink: $(DIST_DIR)' >&2; \
		exit 1; \
	fi
	@if [ -L "$(DIST_ARTIFACT_DIR)" ]; then \
		printf '%s\n' 'refusing to publish through symlink: $(DIST_ARTIFACT_DIR)' >&2; \
		exit 1; \
	fi
	@mkdir -p "$(DIST_ARTIFACT_DIR)"
	@"$(BINARY)" publish --candidate "$$CANDIDATE" --destination "$(ARTIFACT)"

inspect-artifact: build
	@"$(BINARY)" inspect --database "$(ARTIFACT)"

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
	@if [ -L "$(DIST_DIR)" ]; then \
		printf '%s\n' 'refusing to build through symlink: $(DIST_DIR)' >&2; \
		exit 1; \
	fi
	@if [ -L "$(DIST_BIN_DIR)" ]; then \
		printf '%s\n' 'refusing to build through symlink: $(DIST_BIN_DIR)' >&2; \
		exit 1; \
	fi
	@if [ -L "$(BINARY)" ]; then \
		printf '%s\n' 'refusing to replace symlink: $(BINARY)' >&2; \
		exit 1; \
	fi
	mkdir -p "$(DIST_BIN_DIR)"
	$(GO) build -o "$(BINARY)" ./cmd/stategeodb
