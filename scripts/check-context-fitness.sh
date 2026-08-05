#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
command -v rg >/dev/null || { echo 'ERROR: ripgrep (rg) is required' >&2; exit 1; }
BASE_SHA="${CONTEXT_BASE_SHA:-bfe21f34d1da3f2d51eb520c4deef635c7f49cab}"

fail() { echo "ERROR: $*" >&2; exit 1; }
require() { rg -q "$1" "$2" || fail "$3"; }
forbid() { if rg -n "$1" "$2"; then fail "$3"; fi; }

forbid 'internal/(staging|tasks)(/|\")' internal/contextengine 'contextengine imports staging or tasks'
forbid 'internal/.+(model|adapter)' internal/contextengine 'contextengine imports model adapters'
if rg -n '(openai|anthropic|qwen|deepseek|ollama|llm).*\(' internal/contextengine --glob '*.go'; then fail 'contextengine appears to call an LLM'; fi
if rg -n 'time\.Now\(' internal/contextengine --glob '*.go' --glob '!clock.go' --glob '!*_test.go'; then fail 'time.Now appears outside the injected clock adapter'; fi
forbid 'json\.Marshal\([^)]*map\[' internal/contextengine/hashing.go 'hashing serializes a Go map directly'

require 'CREATE TABLE context_snapshots' migrations/000006_create_context_engine.up.sql 'context_snapshots migration missing'
require 'CREATE TABLE context_segments' migrations/000006_create_context_engine.up.sql 'context_segments migration missing'
require 'UNIQUE \(organization_id, idempotency_key\)' migrations/000006_create_context_engine.up.sql 'context idempotency uniqueness missing'
require 'context_snapshots_actor_role_fk' migrations/000006_create_context_engine.up.sql 'composite actor-role FK missing'
require 'context_segments_snapshot_fk' migrations/000006_create_context_engine.up.sql 'composite segment FK missing'
require "data_class IN \('public','organizational','sanitized'\)" migrations/000006_create_context_engine.up.sql 'data class constraint missing'
require 'audit_events' internal/contextengine/postgres/store.go 'context audit integration missing'
require 'outbox_events' internal/contextengine/postgres/store.go 'context outbox integration missing'
require 'TestLoaderRejectsPathTraversal' internal/contextengine/document/loader_test.go 'path traversal test missing'
require 'TestLoaderRejectsSymlinkEscape' internal/contextengine/document/loader_test.go 'symlink escape test missing'
require 'TestDigestBuildRequestIsDeterministic' internal/contextengine/hashing_test.go 'deterministic hash test missing'
require 'TestImportedSkillsAreNotActive' internal/contextengine/canonical/provider_test.go 'draft skill exclusion test missing'
require 'ReasonClinicalDataRejected' internal/contextengine/validation_test.go 'clinical rejection test missing'
require 'TestAssemblerAuthorityIsSeparateFromRenderOrder' internal/contextengine/assembler_test.go 'authority/render-order test missing'
require 'concurrent creation exactly once' internal/contextengine/postgres/integration_test.go 'concurrent idempotency integration missing'
require 'TestServiceValidateDetectsProfileAndCanonicalDrift' internal/contextengine/service_test.go 'policy/source drift validation missing'
require 'source_kind NOT IN.*approved_memory' migrations/000006_create_context_engine.up.sql 'memory/RAG/task capability constraint missing'

if git cat-file -e "$BASE_SHA^{commit}" 2>/dev/null; then
  git diff --exit-code "$BASE_SHA" -- docs/canonical >/dev/null || fail 'docs/canonical changed from Branch 07 base'
elif [[ "${CONTEXT_ALLOW_MISSING_BASE:-0}" != "1" ]]; then
  fail "canonical base commit $BASE_SHA is unavailable"
fi

echo 'context fitness checks passed'
