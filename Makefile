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

.PHONY: help deps fmt fmt-check vet test test-unit test-race test-integration test-task-integration test-task-fitness test-staging-integration test-staging-fitness test-authorization-integration test-authorization-fitness test-context-integration test-context-fitness test-model-runtime-integration test-model-runtime-fitness test-model-egress-integration test-model-egress-fitness test-model-identity-integration test-model-identity-fitness test-model-provider-fitness build build-cross run verify verify-all clean docker-build compose-up compose-down compose-logs migrate-up migrate-status registry-validate registry-diff registry-sync registry-status task-reconcile outbox-status

help:
	@printf '%s\n' \
	  'make deps              Download and verify Go modules' \
	  'make verify            Run formatting, vet, unit tests, and native builds' \
	  'make build-cross       Build orgd/orgctl for linux/arm64 and linux/amd64' \
	  'make test-integration  Run isolated PostgreSQL 17 integration and CLI smoke tests' \
	  'make test-task-fitness Validate durable completion/token/outbox invariants' \
	  'make test-staging-fitness Validate staging security and immutable-promotion invariants' \
	  'make test-authorization-fitness Validate capability policy and durable approval invariants' \
	  'make test-authorization-integration Run PostgreSQL 17 authorization integration tests' \
	  'make test-context-fitness Validate deterministic context, policy, and provenance invariants' \
	  'make test-context-integration Run PostgreSQL 17 context-engine integration tests' \
	  'make test-model-runtime-fitness Validate model routing, privacy, and one-shot invariants' \
	  'make test-model-runtime-integration Run PostgreSQL 17 model-runtime integration tests' \
	  'make test-model-egress-fitness Validate default-deny model egress and pre-send invariants' \
	  'make test-model-egress-integration Run PostgreSQL 17 model-egress integration tests' \
	  'make test-model-identity-fitness Validate cryptographic execution identity invariants' \
	  'make test-model-identity-integration Run PostgreSQL 17 execution identity integration tests' \
	  'make test-model-provider-fitness Validate real provider boundary and durable transport evidence' \
	  'make test-staging-integration Run PostgreSQL 17 and real Git staging integration tests' \
	  'make verify-all        Run verify, cross-build, canonical validation, and integration tests' \
	  'make registry-validate Validate docs/canonical without PostgreSQL writes' \
	  'make registry-diff     Compare docs/canonical with PostgreSQL' \
	  'make registry-sync     Apply an explicit canonical registry revision' \
	  'make registry-status   Show current materialized registry status' \
	  'make migrate-up        Apply PostgreSQL migrations with orgctl' \
	  'make migrate-status    Inspect PostgreSQL migration status'

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

test-task-integration:
	./scripts/test-integration.sh tasks

test-task-fitness:
	./scripts/check-task-fitness.sh

test-staging-fitness:
	./scripts/check-staging-fitness.sh

test-staging-integration:
	./scripts/test-integration.sh staging

test-authorization-fitness:
	./scripts/check-authorization-fitness.sh

test-authorization-integration:
	./scripts/test-integration.sh authorization

test-context-fitness:
	./scripts/check-context-fitness.sh

test-context-integration:
	./scripts/test-integration.sh context

test-model-runtime-fitness:
	./scripts/check-model-runtime-fitness.sh

test-model-runtime-integration:
	./scripts/test-integration.sh model

test-model-egress-fitness:
	./scripts/check-model-egress-fitness.sh

test-model-egress-integration:
	./scripts/test-integration.sh egress

test-model-dispatch-fitness:
	./scripts/check-model-dispatch-fitness.sh

test-model-dispatch-integration:
	./scripts/test-integration.sh dispatch

test-model-identity-fitness:
	./scripts/check-model-identity-fitness.sh

test-model-identity-integration:
	./scripts/test-integration.sh identity

test-model-provider-fitness:
	./scripts/check-model-provider-fitness.sh

build:
	@mkdir -p $(BINARY_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY_DIR)/orgd ./cmd/orgd
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY_DIR)/orgctl ./cmd/orgctl

build-cross:
	@mkdir -p $(BINARY_DIR)/linux-arm64 $(BINARY_DIR)/linux-amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY_DIR)/linux-arm64/orgd ./cmd/orgd
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY_DIR)/linux-arm64/orgctl ./cmd/orgctl
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY_DIR)/linux-amd64/orgd ./cmd/orgd
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY_DIR)/linux-amd64/orgctl ./cmd/orgctl

run:
	$(GO) run ./cmd/orgd

verify: fmt-check vet test-unit test-task-fitness test-staging-fitness test-authorization-fitness test-context-fitness test-model-runtime-fitness test-model-egress-fitness test-model-dispatch-fitness test-model-identity-fitness test-model-provider-fitness build

verify-all: verify build-cross registry-validate test-integration test-context-integration test-model-runtime-integration test-model-egress-integration test-model-dispatch-integration test-model-identity-integration test-authorization-integration test-staging-integration

migrate-up:
	$(GO) run ./cmd/orgctl migrate up

migrate-status:
	$(GO) run ./cmd/orgctl migrate status

registry-validate:
	$(GO) run ./cmd/orgctl registry validate

registry-diff:
	$(GO) run ./cmd/orgctl registry diff

registry-sync:
	$(GO) run ./cmd/orgctl registry sync --apply

registry-status:
	$(GO) run ./cmd/orgctl registry status

task-reconcile:
	$(GO) run ./cmd/orgctl task reconcile --json

outbox-status:
	$(GO) run ./cmd/orgctl outbox status --json

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
