SHELL := /bin/bash

VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -ldflags "-X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildDate=$(BUILD_DATE)"

GO       ?= go
BIN_DIR  := bin
PKG      := ./...

.PHONY: all
all: check

.PHONY: fmt
fmt:
	$(GO) fmt $(PKG)

.PHONY: vet
vet:
	$(GO) vet $(PKG)

.PHONY: lint
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { echo >&2 "golangci-lint not found. Install: https://golangci-lint.run/"; exit 1; }
	golangci-lint run $(PKG)

.PHONY: test
test:
	$(GO) test -race -count=1 $(PKG)

.PHONY: cover
cover:
	$(GO) test -race -count=1 -coverprofile=coverage.out $(PKG)
	$(GO) tool cover -func=coverage.out | tail -n 1

.PHONY: integration
integration:
	$(GO) test -race -count=1 -tags=integration $(PKG)

.PHONY: build
build:
	mkdir -p $(BIN_DIR)
	$(GO) build $(LDFLAGS) -o $(BIN_DIR)/praxis ./cmd/praxis

.PHONY: install
install:
	$(GO) install $(LDFLAGS) ./cmd/praxis

.PHONY: sqlc
sqlc:
	@command -v sqlc >/dev/null 2>&1 || { echo >&2 "sqlc not found. Install: https://docs.sqlc.dev/en/stable/overview/install.html"; exit 1; }
	sqlc generate

.PHONY: check
check: fmt vet test build

.PHONY: release-check
release-check: fmt vet lint test build

.PHONY: clean
clean:
	rm -rf $(BIN_DIR) coverage.out coverage.html
