#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
fail(){ echo "memory fitness: FAIL: $*" >&2; exit 1; }
require(){ local pattern="$1" path="$2"; grep -Eq "$pattern" "$path" || fail "missing pattern '$pattern' in $path"; }

require 'DataClinical' internal/memory/types.go
require 'DataSecret' internal/memory/types.go
require 'AllowedInOrganizationalMemory' internal/memory/types.go
require 'SanitizationEvidenceRef' internal/memory/types.go
require 'SourceOperational' internal/memory/types.go
require 'SourceSimulation' internal/memory/types.go
require 'SourceSyntheticTest' internal/memory/types.go
require 'SourceKind' internal/memory/hashing.go
require 'memory\.propose' internal/memory/manager.go
require 'memory\.approve' internal/memory/manager.go
require 'AuthorizationGate' internal/memory/manager.go
require 'ExpectedRevision' internal/memory/manager.go
require 'canonicalHash' internal/memory/postgres/store.go
require 'organizational_memory_state_events' internal/memory/postgres/store.go
require 'FOR UPDATE OF e' internal/memory/postgres/store.go
require 'source_kind TEXT NOT NULL' migrations/000015_create_organizational_memory.up.sql
require "'operational', 'simulation', 'synthetic_test'" migrations/000015_create_organizational_memory.up.sql
require 'data_class TEXT NOT NULL' migrations/000015_create_organizational_memory.up.sql
require "'public', 'organizational', 'sanitized'" migrations/000015_create_organizational_memory.up.sql
require 'transition requires matching audit event' migrations/000015_create_organizational_memory.up.sql
require 'review provenance is immutable after review' migrations/000015_create_organizational_memory.up.sql
require 'organizational memory audit/content rows are immutable' migrations/000015_create_organizational_memory.up.sql
require 'SourceApprovedMemory' internal/memory/contextprovider/provider.go
require 'TierApprovedMemory' internal/memory/contextprovider/provider.go
require 'InstructionData' internal/memory/contextprovider/provider.go
require 'TrustUntrusted' internal/memory/contextprovider/provider.go
require 'MayGrantCapabilities:[[:space:]]*false' internal/memory/contextprovider/provider.go
require 'SourceKind' internal/memory/contextprovider/provider.go
require 'ValidateVersion' internal/memory/contextprovider/provider.go
require 'DisallowUnknownFields' cmd/orgctl/memory.go
require 'multiple top-level JSON values are not allowed' cmd/orgctl/memory.go
require 'memoryProvider' internal/contextengine/bootstrap/bootstrap.go

if grep -R -E 'cell\.read_clinical_data|clinical_records|patient_records' internal/memory --include='*.go'; then fail 'organizational memory reached into a clinical source boundary'; fi
# embedding/pgvector/semantic_search were deliberately forbidden through
# R18-R28 (see branch-18 INTEGRATION.md); branch-29 DESIGN.md explicitly
# supersedes that clause and introduces organizational_memory_embeddings as a
# derived table. internal/memory must still never import internal/rag
# directly — they stay independent domains, wired together only through
# internal/contextengine.
if grep -R -E 'internal/rag' internal/memory --include='*.go'; then fail 'R18 introduced a direct dependency on internal/rag forbidden by domain boundaries'; fi
if grep -R -E 'MayGrantCapabilities:[[:space:]]*true' internal/memory/contextprovider --include='*.go'; then fail 'approved memory may grant capabilities'; fi
require 'StatusCandidate:[[:space:]]*\{' internal/memory/transitions.go
require 'StatusArchived:[[:space:]]*\{\}' internal/memory/transitions.go
require 'case "memory"' cmd/orgctl/main.go
require 'memorybootstrap\.Open' cmd/orgctl/memory.go
require 'memoryauthz\.New' internal/memory/bootstrap/bootstrap.go

go test ./internal/memory/... ./cmd/orgctl

echo 'memory fitness: PASS'
