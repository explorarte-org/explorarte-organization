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

.PHONY: help deps fmt fmt-check vet test test-unit test-race test-integration check-integration-evidence check-egress-restorability test-task-integration test-task-fitness test-staging-integration test-staging-fitness test-authorization-integration test-authorization-fitness test-context-integration test-context-fitness test-memory-integration test-memory-fitness test-skillregistry-integration test-skillregistry-fitness test-rag-integration test-rag-fitness test-model-runtime-integration test-model-runtime-fitness test-model-egress-integration test-model-egress-fitness test-model-identity-integration test-model-identity-fitness test-model-provider-fitness test-alibaba-cli-fitness test-cellworker-integration test-cellworker-fitness test-decisiongraph-integration test-decisiongraph-fitness test-decisiongraphtrace-integration test-improvement-integration test-improvement-fitness test-completion-integration test-completion-fitness test-composition-integration test-mission-integration test-executive-integration test-executive-fitness test-embeddingruntime-fitness test-webevidence-fitness build build-cross run verify verify-all clean docker-build compose-up compose-down compose-logs migrate-up migrate-status registry-validate registry-diff registry-sync registry-status task-reconcile outbox-status

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
	  'make test-memory-fitness Validate organizational-memory admission, review, audit, and context invariants' \
	  'make test-memory-integration Run PostgreSQL 17 organizational-memory integration tests' \
	  'make test-skillregistry-fitness Validate skill lifecycle, governance, and immutability invariants' \
	  'make test-skillregistry-integration Run PostgreSQL 17 skill registry integration tests' \
	  'make test-rag-fitness Validate approved knowledge lifecycle, admission, namespace, and chunk invariants' \
	  'make test-rag-integration Run PostgreSQL 17 approved knowledge and RAG integration tests' \
	  'make test-model-runtime-fitness Validate model routing, privacy, and one-shot invariants' \
	  'make test-model-runtime-integration Run PostgreSQL 17 model-runtime integration tests' \
	  'make test-model-egress-fitness Validate default-deny model egress and pre-send invariants' \
	  'make test-model-egress-integration Run PostgreSQL 17 model-egress integration tests' \
	  'make test-model-identity-fitness Validate cryptographic execution identity invariants' \
	  'make test-model-identity-integration Run PostgreSQL 17 execution identity integration tests' \
	  'make test-model-provider-fitness Validate real provider boundary and durable transport evidence' \
	  'make test-alibaba-cli-fitness Validate Alibaba Claude Code CLI sandbox and transport invariants' \
	  'make test-decisiongraph-fitness Validate durable DAG, budget, privacy, and claim invariants' \
	  'make test-decisiongraph-integration Run PostgreSQL 17 decision-graph integration tests' \
	  'make test-executive-fitness Validate executive orchestration authority, completion, and transport boundaries' \
	  'make test-executive-integration Run PostgreSQL 17 executive orchestration integration tests' \
	  'make test-embeddingruntime-fitness Validate embedding adapter isolation, loopback-only bge-m3, and pinned model identity invariants' \
	  'make test-webevidence-fitness Validate web evidence stays untrusted data, never an instruction, never promoted to RAG/Memory' \
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

check-integration-evidence:
	./scripts/check-integration-evidence-fitness.sh

check-egress-restorability:
	./scripts/check-egress-restorability-fitness.sh

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

test-memory-fitness:
	bash ./scripts/check-memory-fitness.sh

test-memory-integration:
	./scripts/test-integration.sh memory

test-skillregistry-fitness:
	bash ./scripts/check-skillregistry-fitness.sh

test-skillregistry-integration:
	./scripts/test-integration.sh skillregistry

test-rag-fitness:
	bash ./scripts/check-rag-fitness.sh

test-rag-integration:
	./scripts/test-integration.sh rag

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

test-alibaba-cli-fitness:
	bash ./scripts/check-alibaba-cli-fitness.sh

test-embeddingruntime-fitness:
	bash ./scripts/check-embeddingruntime-fitness.sh

test-webevidence-fitness:
	bash ./scripts/check-webevidence-fitness.sh

test-cellworker-fitness:
	./scripts/check-cellworker-fitness.sh

test-cellworker-integration:
	./scripts/test-integration.sh worker

test-decisiongraph-fitness:
	./scripts/check-decisiongraph-fitness.sh

test-decisiongraph-integration:
	./scripts/test-integration.sh decision

test-decisiongraphtrace-integration:
	./scripts/test-integration.sh trace

test-improvement-fitness:
	./scripts/check-improvement-fitness.sh

test-improvement-integration:
	./scripts/test-integration.sh improvement

test-completion-fitness:
	./scripts/check-completion-fitness.sh

test-completion-integration:
	./scripts/test-integration.sh completion

test-composition-integration:
	./scripts/test-integration.sh composition

test-mission-integration:
	./scripts/test-integration.sh mission

test-executive-fitness:
	./scripts/check-executive-fitness.sh

test-executive-integration:
	./scripts/test-executive-integration.sh

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

verify: fmt-check vet test-unit test-task-fitness test-staging-fitness test-authorization-fitness test-context-fitness test-memory-fitness test-skillregistry-fitness test-rag-fitness test-model-runtime-fitness test-model-egress-fitness test-model-dispatch-fitness test-model-identity-fitness test-model-provider-fitness test-alibaba-cli-fitness test-cellworker-fitness test-decisiongraph-fitness test-improvement-fitness test-completion-fitness test-executive-fitness test-embeddingruntime-fitness test-webevidence-fitness build

verify-all: verify build-cross registry-validate test-integration test-context-integration test-memory-integration test-skillregistry-integration test-rag-integration test-model-runtime-integration test-model-egress-integration test-model-dispatch-integration test-model-identity-integration test-cellworker-integration test-decisiongraph-integration test-decisiongraphtrace-integration test-improvement-integration test-authorization-integration test-staging-integration test-completion-integration test-executive-integration

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
