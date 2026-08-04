#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
BASE_COMMIT="${STAGING_ENGINE_BASE_COMMIT:-de5ac792cb67489b9f35e3b52cbec9ca8f63c7c7}"

if git cat-file -e "${BASE_COMMIT}^{commit}" 2>/dev/null; then
  git diff --exit-code "$BASE_COMMIT" -- docs/canonical
  git diff --exit-code "$BASE_COMMIT" -- \
    migrations/000001_create_audit_events.up.sql \
    migrations/000001_create_audit_events.down.sql \
    migrations/000002_create_organization_registry.up.sql \
    migrations/000002_create_organization_registry.down.sql \
    migrations/000003_create_durable_task_engine.up.sql \
    migrations/000003_create_durable_task_engine.down.sql
fi

for pattern in \
  'exec\\.CommandContext\\([^,]+,[[:space:]]*"(sh|bash)"[[:space:]]*,[[:space:]]*"-c"' \
  'git[[:space:]]+(merge|rebase|cherry-pick|push|fetch)' \
  'CommandContext\\([^)]*"(merge|rebase|cherry-pick|push|fetch)"'; do
  if rg -n --glob '*.go' --glob '*.sh' "$pattern" internal/staging cmd/orgctl >/tmp/staging-fitness-match 2>/dev/null; then
    echo "ERROR: forbidden staging operation detected: $pattern" >&2
    cat /tmp/staging-fitness-match >&2
    exit 1
  fi
done

if rg -n --glob '*.go' -- '--lease-token(=|[[:space:]])' cmd/orgctl internal/staging >/tmp/staging-token-flags 2>/dev/null; then
  echo 'ERROR: plaintext lease token flag detected' >&2
  cat /tmp/staging-token-flags >&2
  exit 1
fi

grep -q 'lease-token-stdin' cmd/orgctl/staging.go
grep -q '"update-ref"' internal/staging/gitexec/backend.go
grep -q 'PromotionApplied' internal/staging/state_machine.go
grep -q 'WorkspaceCleaned' internal/staging/state_machine.go
grep -q 'ORG_STAGING_ENABLED.*false' .env.example
grep -q '127.0.0.1:.*:8080' compose.yaml
! grep -Eq '5432:5432|0\.0\.0\.0:5432' compose.yaml
! grep -q '/var/run/docker.sock' compose.yaml compose.integration.yaml

audit_block="$(awk '/func appendEvent\(/,/^}/' internal/staging/postgres/helpers.go)"
if grep -Eqi 'workspace_path|repository_path|absolute_path|instructions|diff_content|lease_token|token_hash' <<<"$audit_block"; then
  echo 'ERROR: sensitive staging data appears in event/outbox builder' >&2
  printf '%s\n' "$audit_block" >&2
  exit 1
fi

if rg -n 'time\.Now\(' internal/staging/postgres >/tmp/staging-db-clock 2>/dev/null; then
  echo 'ERROR: durable staging PostgreSQL code uses application wall-clock time' >&2
  cat /tmp/staging-db-clock >&2
  exit 1
fi

go test -count=1 ./internal/staging/... ./internal/authorization

echo 'staging fitness checks passed'
