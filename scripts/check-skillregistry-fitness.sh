#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
fail(){ echo "skillregistry fitness: FAIL: $*" >&2; exit 1; }
require(){ local pattern="$1" path="$2"; grep -Eq "$pattern" "$path" || fail "missing pattern '$pattern' in $path"; }

require 'LifecycleDraft' internal/skillregistry/domain.go
require 'LifecycleActive' internal/skillregistry/domain.go
require 'LifecycleRetired' internal/skillregistry/domain.go
require 'OriginGitHub' internal/skillregistry/domain.go
require 'githubPinnedPattern' internal/skillregistry/validation.go
require 'legacySkillFilePattern' internal/skillregistry/validation.go
require 'CapabilityPropose' internal/skillregistry/manager.go
require 'CapabilityActivate' internal/skillregistry/manager.go
require 'AuthorizeProposal' internal/skillregistry/manager.go
require 'AuthorizeLifecycleChange' internal/skillregistry/manager.go
require 'AuthorizeAssignmentChange' internal/skillregistry/manager.go
require 'ExpectedRevision' internal/skillregistry/manager.go
require 'canonical_hash' internal/skillregistry/postgres/store.go
require 'skill_registry_lifecycle_events' internal/skillregistry/postgres/store.go
require 'FOR UPDATE' internal/skillregistry/postgres/store.go
require 'lifecycle TEXT NOT NULL CHECK' migrations/000016_create_skill_registry.up.sql
require "'draft', 'human_approved', 'candidate', 'active', 'suspended', 'retired'" migrations/000016_create_skill_registry.up.sql
require 'skill registry version content is immutable' migrations/000016_create_skill_registry.up.sql
require 'skill registry transition requires matching audit event' migrations/000016_create_skill_registry.up.sql
require 'skill_registry_assignments_active_idx' migrations/000016_create_skill_registry.up.sql
require 'organization\.propose_skill' internal/skillregistry/manager.go
require 'organization\.activate_skill' internal/skillregistry/manager.go
require 'skillregistry\.CapabilityActivate' internal/skillregistry/authz/gate.go
require 'DisallowUnknownFields' cmd/orgctl/skill.go
require 'multiple top-level JSON values are not allowed' cmd/orgctl/skill.go
require 'case "skill"' cmd/orgctl/main.go
require 'skillregistrybootstrap\.Open' cmd/orgctl/skill.go
require 'skillregistryauthz\.New' internal/skillregistry/bootstrap/bootstrap.go

if grep -R -E 'cell\.read_clinical_data|clinical_records|patient_records' internal/skillregistry --include='*.go'; then fail 'skill registry reached into a clinical source boundary'; fi
if grep -R -E 'embedding|pgvector|semantic_search|internal/rag' internal/skillregistry --include='*.go'; then fail 'R19 introduced retrieval/vector semantics that belong to a later layer'; fi
if grep -R -E "^\s*origin\s*==\s*OriginGitHub\s*$" internal/skillregistry --include='*.go'; then fail 'unexpected bare GitHub origin check without pin validation'; fi

require 'ValidateTransition' internal/skillregistry/transitions.go
require 'LifecycleSuspended: \{' internal/skillregistry/transitions.go
require 'LifecycleRetired: \{\}' internal/skillregistry/transitions.go

go test ./internal/skillregistry/... ./cmd/orgctl

echo 'skillregistry fitness: PASS'
