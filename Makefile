SHELL := /usr/bin/env bash

GO ?= go
BINARY_DIR ?= bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.buildTime=$(BUILD_TIME)

.PHONY: help fmt fmt-check vet test test-race build run verify clean docker-build compose-up compose-down compose-logs

help:
	@printf '%s\n' \
	  'make fmt          Format Go files' \
	  'make verify       Run formatting, vet, tests, and builds' \
	  'make build        Build orgd and orgctl into bin/' \
	  'make run          Run orgd locally' \
	  'make docker-build Build the runtime image' \
	  'make compose-up   Start the local container' \
	  'make compose-down Stop the local container'

fmt:
	$(GO) fmt ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || { echo 'Go files require gofmt:'; gofmt -l .; exit 1; }

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

build:
	@mkdir -p $(BINARY_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY_DIR)/orgd ./cmd/orgd
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY_DIR)/orgctl ./cmd/orgctl

run:
	$(GO) run ./cmd/orgd

verify: fmt-check vet test build

clean:
	rm -rf $(BINARY_DIR) dist coverage.out

docker-build:
	docker build \
	  --build-arg VERSION=$(VERSION) \
	  --build-arg COMMIT=$(COMMIT) \
	  --build-arg BUILD_TIME=$(BUILD_TIME) \
	  -t explorarte-organization:$(VERSION) .

compose-up:
	docker compose up --build -d

compose-down:
	docker compose down --remove-orphans

compose-logs:
	docker compose logs -f orgd
