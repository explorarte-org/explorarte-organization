#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
fail(){ echo "rag fitness: FAIL: $*" >&2; exit 1; }
require(){ local pattern="$1" path="$2"; grep -Eq "$pattern" "$path" || fail "missing pattern '$pattern' in $path"; }

require 'DataClinical' internal/rag/domain.go
require 'DataSecret' internal/rag/domain.go
require 'AllowedInApprovedKnowledge' internal/rag/domain.go
require 'LifecycleCandidate' internal/rag/domain.go
require 'LifecycleArchived' internal/rag/domain.go
require 'NamespaceDepartment' internal/rag/domain.go
require 'NamespaceOwn' internal/rag/domain.go
require 'rag\.propose_candidate' internal/rag/manager.go
require 'rag\.publish_approved' internal/rag/manager.go
require 'rag\.read_department' internal/rag/manager.go
require 'rag\.read_own_namespace' internal/rag/manager.go
require 'ExpectedRevision' internal/rag/manager.go
require 'ChunkBody' internal/rag/manager.go
require 'ErrVersionNotApproved' internal/rag/manager.go
require 'DefaultChunkerID' internal/rag/chunking.go
require 'maxChunkBytes' internal/rag/chunking.go
require 'canonical_hash' internal/rag/postgres/store.go
require 'rag_knowledge_lifecycle_events' internal/rag/postgres/store.go
require 'FOR UPDATE' internal/rag/postgres/store.go
require 'ts_rank' internal/rag/postgres/store.go
require 'plainto_tsquery' internal/rag/postgres/store.go
require "lifecycle='approved'" internal/rag/postgres/store.go
require 'lifecycle TEXT NOT NULL CHECK' migrations/000017_create_approved_knowledge_rag.up.sql
require "'candidate', 'approved', 'rejected', 'deprecated', 'archived'" migrations/000017_create_approved_knowledge_rag.up.sql
require 'rag knowledge version content is immutable' migrations/000017_create_approved_knowledge_rag.up.sql
require 'rag knowledge transition requires matching audit event' migrations/000017_create_approved_knowledge_rag.up.sql
require 'rag_index_generations_active_idx' migrations/000017_create_approved_knowledge_rag.up.sql
require 'rag can only index approved knowledge versions' migrations/000017_create_approved_knowledge_rag.up.sql
require 'content_tsv tsvector GENERATED ALWAYS' migrations/000017_create_approved_knowledge_rag.up.sql
require 'USING GIN' migrations/000017_create_approved_knowledge_rag.up.sql
require 'SourceRAGEvidence' internal/rag/contextprovider/provider.go
require 'TierRAGEvidence' internal/rag/contextprovider/provider.go
require 'InstructionData' internal/rag/contextprovider/provider.go
require 'TrustUntrusted' internal/rag/contextprovider/provider.go
require 'MayGrantCapabilities:[[:space:]]*false' internal/rag/contextprovider/provider.go
require 'ValidateVersion' internal/rag/contextprovider/provider.go
require 'ActiveGeneration' internal/rag/contextprovider/provider.go
require 'DisallowUnknownFields' cmd/orgctl/rag.go
require 'multiple top-level JSON values are not allowed' cmd/orgctl/rag.go
require 'case "rag"' cmd/orgctl/main.go
require 'ragbootstrap\.Open' cmd/orgctl/rag.go
require 'ragauthz\.New' internal/rag/bootstrap/bootstrap.go
require 'ragbootstrap\.Open' internal/contextengine/bootstrap/bootstrap.go

if grep -R -E "publish-direct|PublishDirect" cmd/orgctl internal/rag --include='*.go'; then fail 'R20 introduced a direct-publish CLI path forbidden by organization.yaml'; fi
if grep -R -E 'cell\.read_clinical_data|clinical_records|patient_records' internal/rag --include='*.go'; then fail 'rag reached into a clinical source boundary'; fi
# pgvector was deliberately forbidden through R20-R28 (see branch-20 DESIGN.md
# Alcance D); branch-29 DESIGN.md explicitly supersedes that clause and
# introduces it as the sole vector backend. Third-party hosted vector
# databases remain forbidden — this system stays on PostgreSQL as the single
# source of truth, no external vector store dependency.
if grep -R -E 'Pinecone|Qdrant|Weaviate' internal/rag migrations --include='*.go' --include='*.sql'; then fail 'R20 introduced an external vector backend forbidden at this stage'; fi
if grep -R -E 'MayGrantCapabilities:[[:space:]]*true' internal/rag/contextprovider --include='*.go'; then fail 'approved rag evidence may grant capabilities'; fi
require 'LifecycleApproved:[[:space:]]*\{' internal/rag/transitions.go
require 'LifecycleArchived:[[:space:]]*\{\}' internal/rag/transitions.go
require 'case "rag"' cmd/orgctl/main.go

go test ./internal/rag/... ./cmd/orgctl

echo 'rag fitness: PASS'
