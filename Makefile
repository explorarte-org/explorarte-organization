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

.PHONY: help deps fmt fmt-check vet test test-unit test-race test-integration build run verify verify-all clean docker-build compose-up compose-down compose-logs migrate-up migrate-status

help:
	@printf '%s\n' \
	  'make deps             Download and verify Go modules' \
	  'make fmt              Format Go files' \
	  'make verify           Run formatting, vet, unit tests, and builds' \
	  'make verify-all       Run verify plus real PostgreSQL integration tests' \
	  'make test-integration Run integration tests in isolated Docker Compose' \
	  'make migrate-up       Apply PostgreSQL migrations with orgctl' \
	  'make migrate-status   Inspect PostgreSQL migration status' \
	  'make compose-up       Start orgd and internal PostgreSQL' \
	  'make compose-down     Stop containers without deleting PostgreSQL data'

deps:
	$(GO) mod download
	$(GO) mod verify

fmt:
	$(GO) fmt ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || { echo 'Go files require gofmt:'; gofmt -l .; exit 1; }

vet:
	$(GO) vet ./...

test: test-unit

test-unit:
	$(GO) test -short ./...

test-race:
	$(GO) test -race -short ./...

test-integration:
	./scripts/test-integration.sh

build:
	@mkdir -p $(BINARY_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY_DIR)/orgd ./cmd/orgd
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY_DIR)/orgctl ./cmd/orgctl

run:
	$(GO) run ./cmd/orgd

verify: fmt-check vet test-unit build

verify-all: verify test-integration

migrate-up:
	$(GO) run ./cmd/orgctl migrate up

migrate-status:
	$(GO) run ./cmd/orgctl migrate status

clean:
	rm -rf $(BINARY_DIR) dist coverage.out

docker-build:
	docker build \
	  --build-arg VERSION=$(VERSION) \
	  --build-arg COMMIT=$(COMMIT) \
	  --build-arg BUILD_TIME=$(BUILD_TIME) \
	  -t explorarte-organization:$(VERSION) .

compose-up:
	@test -f .env || { echo 'ERROR: create .env from .env.example first'; exit 1; }
	docker compose up --build -d

compose-down:
	docker compose down --remove-orphans

compose-logs:
	docker compose logs -f orgd postgres
