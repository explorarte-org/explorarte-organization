#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
BASE_SHA="${MODEL_IDENTITY_BASE_SHA:-f1a9ffacd8b3401ed56fd4e95ba95520b28b025a}"

fail() { echo "model-identity fitness: $*" >&2; exit 1; }
command -v rg >/dev/null 2>&1 || fail "ripgrep is required"
git cat-file -e "${BASE_SHA}^{commit}" 2>/dev/null || fail "base commit ${BASE_SHA} is unavailable"

test -f docs/canonical/model-execution-identity-policy.yaml || fail "canonical identity policy is missing"
test -f migrations/000010_create_model_execution_identity.up.sql || fail "migration 000010 up is missing"
test -f migrations/000010_create_model_execution_identity.down.sql || fail "migration 000010 down is missing"

mapfile -t canonical_changes < <({
  git diff --name-only "$BASE_SHA" -- docs/canonical
  git ls-files --others --exclude-standard -- docs/canonical
} | sort -u)
for path in "${canonical_changes[@]}"; do
  case "$path" in
    docs/canonical/capability-matrix.yaml|docs/canonical/model-execution-identity-policy.yaml) ;;
    *) fail "unauthorized canonical change: $path" ;;
  esac
done

git diff --exit-code "$BASE_SHA" -- \
  migrations/000001\* migrations/000002\* migrations/000003\* migrations/000004\* \
  migrations/000005\* migrations/000006\* migrations/000007\* migrations/000008\* migrations/000009\* >/dev/null \
  || fail "migration 000001-000009 changed"

rg -q '^default_action: deny$' docs/canonical/model-execution-identity-policy.yaml || fail "identity policy is not default-deny"
rg -q '^algorithm: ed25519$' docs/canonical/model-execution-identity-policy.yaml || fail "identity policy is not Ed25519-only"
rg -q 'crypto/ed25519' internal/modelidentity internal/modelruntime || fail "Ed25519 verification is missing"
if rg -n 'crypto/rsa|crypto/dsa|crypto/ecdsa|crypto/hmac' internal/modelidentity internal/modelruntime; then fail "unapproved identity algorithm detected"; fi
if rg -n '"net/http"|"os/exec"|exec\.Command|/bin/(sh|bash)|sh -c|bash -c' internal/modelidentity internal/secrets; then fail "network or process execution is forbidden"; fi

grant_block="$(sed -n '/^grants:/,/^hard_denies:/p' docs/canonical/capability-matrix.yaml)"
hard_deny_block="$(sed -n '/^  execution_service:/,/^  [a-z_*]/p' docs/canonical/capability-matrix.yaml | tail -n +1)"
for capability in model.execution_identity_key.register model.execution_identity_key.rotate model.execution_identity_key.retire model.execution_identity_key.revoke; do
  rg -Fq -- "- id: ${capability}" docs/canonical/capability-matrix.yaml || fail "capability missing: ${capability}"
  if grep -Fq -- "- ${capability}" <<<"$grant_block"; then fail "identity administration capability was granted: ${capability}"; fi
  rg -Fq -- "  - ${capability}" <<<"$hard_deny_block" || fail "execution_service hard deny missing: ${capability}"
done

rg -q 'nonce_hash' migrations/000010_create_model_execution_identity.up.sql || fail "nonce hash persistence is missing"
rg -q 'model_execution_identity_challenges_one_open_idx' migrations/000010_create_model_execution_identity.up.sql || fail "single open challenge constraint is missing"
rg -q 'protect_model_execution_identity_policy_versions' migrations/000010_create_model_execution_identity.up.sql || fail "identity policy immutable-field guard is missing"
rg -q 'protect_model_execution_identity_keys' migrations/000010_create_model_execution_identity.up.sql || fail "identity key immutable-field guard is missing"
rg -q 'protect_model_execution_identity_challenges' migrations/000010_create_model_execution_identity.up.sql || fail "identity challenge immutable-field guard is missing"
rg -q 'reject_model_execution_identity_assertion_mutation' migrations/000010_create_model_execution_identity.up.sql || fail "identity assertion immutable ledger guard is missing"
if rg -n 'raw_nonce|private_key|signature[[:space:]]+(BYTEA|TEXT)' migrations/000010_create_model_execution_identity.up.sql; then fail "raw nonce, private key, or signature persistence detected"; fi
if rg -n 'ORG_MODEL_EXECUTION_PRIVATE_KEY|ORG_MODEL_EXECUTION_IDENTITY_PRIVATE_KEY' . --glob '!scripts/check-model-identity-fitness.sh'; then fail "raw private key environment variable detected"; fi
rg -q 'ORG_MODEL_EXECUTION_IDENTITY_KEY_FILE' internal/modelruntime/config.go || fail "key file configuration is missing"
rg -q 'permissions must not grant group or other access' internal/secrets/file_resolver.go || fail "private key permission guard is missing"
rg -q 'ClaimInvocationAuthenticated' internal/modelruntime/dispatch_service.go internal/modelruntime/postgres/claims.go || fail "authenticated claim boundary is missing"
rg -q 'model_execution_identity_challenges' internal/modelruntime/postgres/claims.go || fail "challenge consumption is not in the claim transaction"
rg -q 'consumed_at=clock_timestamp\(\)' internal/modelruntime/postgres/claims.go || fail "one-use challenge consumption is missing"
rg -q 'FOR UPDATE OF i,a,ia,ik' internal/modelruntime/postgres/presend.go || fail "pre-send identity/key lock is missing"
rg -q 'ErrExecutionIdentityUnpinned' internal/modelruntime/dispatch_service.go || fail "legacy identity pin guard is missing"

if rg -n --glob '!**/*_test.go' 'HiddenReasoning|RenderedContext|rendered_context|provider_payload|api[_-]?key|password' internal/modelidentity migrations/000010_create_model_execution_identity.up.sql; then
  fail "sensitive model content or provider credential persistence detected"
fi

echo "model-identity fitness: OK"
