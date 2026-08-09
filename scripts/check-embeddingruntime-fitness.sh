#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
fail(){ echo "embeddingruntime fitness: FAIL: $*" >&2; exit 1; }
require(){ local pattern="$1" path="$2"; grep -Eq "$pattern" "$path" || fail "missing pattern '$pattern' in $path"; }

# R30 hardening invariants for the local BGE-M3 sidecar adapter.
require 'ProviderID = "bge-m3-local"' internal/embeddingruntime/adapter/bgem3/config.go
require 'ExpectedDimension' internal/embeddingruntime/adapter/bgem3/config.go
require 'ArtifactSHA256' internal/embeddingruntime/adapter/bgem3/config.go
require 'validateLoopbackURL' internal/embeddingruntime/adapter/bgem3/config.go
require 'isLoopbackHost' internal/embeddingruntime/adapter/bgem3/config.go
require 'MaxConcurrency' internal/embeddingruntime/adapter/bgem3/config.go
require 'MaxQueueDepth' internal/embeddingruntime/adapter/bgem3/config.go
require 'ErrQueueFull' internal/embeddingruntime/adapter/bgem3/errors.go
require 'ErrModelIdentityDrift' internal/embeddingruntime/adapter/bgem3/errors.go
require 'ErrInvalidVector' internal/embeddingruntime/adapter/bgem3/errors.go
require 'func validateVector' internal/embeddingruntime/adapter/bgem3/adapter.go
require 'math\.IsNaN' internal/embeddingruntime/adapter/bgem3/adapter.go
require 'math\.IsInf' internal/embeddingruntime/adapter/bgem3/adapter.go
require 'InputHash' internal/embeddingruntime/adapter/bgem3/wire.go
require 'IdempotencyKey' internal/embeddingruntime/adapter/bgem3/wire.go
require 'func \(a \*Adapter\) Healthy' internal/embeddingruntime/adapter/bgem3/health.go
require 'ErrModelIdentityDrift' internal/embeddingruntime/adapter/bgem3/health.go
require 'PeakRSSBytes' internal/embeddingruntime/adapter/bgem3/wire.go
require 'CPUTimeMS' internal/embeddingruntime/adapter/bgem3/wire.go

# R30.1-6: Embed must verify the sidecar's artifact hash and prompt
# template on every call, not just model_revision/dimension — a matching
# revision string is not proof of matching weights, and a matching
# revision+hash says nothing about which prompt template produced the
# vectors.
require 'ArtifactSHA256' internal/embeddingruntime/adapter/bgem3/wire.go
require 'PromptTemplateVersion' internal/embeddingruntime/adapter/bgem3/wire.go
require 'decoded\.ArtifactSHA256' internal/embeddingruntime/adapter/bgem3/adapter.go
require 'decoded\.PromptTemplateVersion' internal/embeddingruntime/adapter/bgem3/adapter.go

# R30.1-6: readiness is mandatory at startup — Healthy must actually be
# called from the productive bootstrap path for both rag and memory, not
# merely exist and go unconnected.
require 'adapter\.Healthy\(healthCtx\)' internal/rag/bootstrap/bootstrap.go
require 'adapter\.Healthy\(healthCtx\)' internal/memory/bootstrap/bootstrap.go

# No subprocess/exec anywhere in internal/embeddingruntime: BGE-M3 is a
# separate, independently hardened process this package only ever talks to
# over HTTP (loopback or Unix socket) — orgd must never spawn or embed the
# sidecar's own runtime (no Python inside orgd).
if grep -R -E '("os/exec"|exec\.Command|syscall\.|/bin/(ba)?sh|sh -c)' internal/embeddingruntime --include='*.go'; then
  fail 'subprocess or shell execution found in embeddingruntime'
fi

# The network client stays isolated to the two approved provider adapters.
if grep -R -n --include='*.go' '"net/http"' internal/embeddingruntime | grep -v -E 'internal/embeddingruntime/adapter/(gemini|bgem3)/'; then
  fail 'network client found outside the approved gemini or bge-m3 adapters'
fi

# gemini stays a remote, HTTPS-only, billed provider; bge-m3 stays a local,
# loopback-only, unbilled provider. Neither adapter should reference the
# other's provider id or default endpoint pattern — a copy-paste mistake
# here would be exactly the kind of accidental cross-wiring R30's "never
# mixed" requirement forbids.
if grep -R -n 'ProviderID = "gemini"' internal/embeddingruntime/adapter/bgem3 --include='*.go' | grep -v '_test.go'; then
  fail 'bge-m3 adapter references the gemini provider id'
fi
if grep -R -n 'ProviderID = "bge-m3-local"' internal/embeddingruntime/adapter/gemini --include='*.go' | grep -v '_test.go'; then
  fail 'gemini adapter references the bge-m3 provider id'
fi

# Raw provider credentials stay out of source, same discipline as
# internal/modelruntime.
if grep -R -n --include='*.go' -E '(API[_-]?KEY|ACCESS[_-]?TOKEN|PROVIDER[_-]?TOKEN)' internal/embeddingruntime 2>/dev/null; then
  fail 'raw provider credential found in embeddingruntime'
fi

echo "embeddingruntime fitness: PASS"
