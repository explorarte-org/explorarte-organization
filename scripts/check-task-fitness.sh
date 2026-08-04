#!/usr/bin/env bash
set -euo pipefail

command -v rg >/dev/null 2>&1 || {
  echo "ERROR: ripgrep (rg) es obligatorio para los fitness checks" >&2
  exit 1
}

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
BASE_COMMIT="${TASK_ENGINE_BASE_COMMIT:-a199d1eea1f4f28d1b9f346e9ccbd670d5e8b69a}"

if git cat-file -e "${BASE_COMMIT}^{commit}" 2>/dev/null; then
  git diff --exit-code "$BASE_COMMIT" -- docs/canonical
  git diff --exit-code "$BASE_COMMIT" -- \
    migrations/000001_create_audit_events.up.sql \
    migrations/000001_create_audit_events.down.sql \
    migrations/000002_create_organization_registry.up.sql \
    migrations/000002_create_organization_registry.down.sql
fi

go test -count=1 ./internal/tasks

outbox_block="$({
  awk '
    /outboxPayload :=/ { capture=1 }
    capture { print }
    /outboxJSON, err :=/ { capture=0 }
  ' internal/tasks/postgres/helpers.go
} || true)"

if grep -Eqi 'instructions|lease[_ ]?token|claim[_ ]?token|token_hash|LeaseToken|ClaimToken' <<<"$outbox_block"; then
  echo "ERROR: sensitive task data appears in the durable outbox payload" >&2
  printf '%s\n' "$outbox_block" >&2
  exit 1
fi

if grep -Eqi '(^|[[:space:],])(lease_token|claim_token)[[:space:]]+(text|varchar|bytea)' migrations/000003_create_durable_task_engine.up.sql; then
  echo "ERROR: a plaintext lease/claim token column exists in migration 000003" >&2
  exit 1
fi

grep -q 'token_hash TEXT NOT NULL' migrations/000003_create_durable_task_engine.up.sql
grep -q 'claim_token_hash TEXT' migrations/000003_create_durable_task_engine.up.sql

if rg -n 'time\.Now\(' internal/tasks/postgres >/dev/null; then
  echo "ERROR: durable PostgreSQL code uses application wall-clock time" >&2
  rg -n 'time\.Now\(' internal/tasks/postgres >&2
  exit 1
fi

# The state-machine tests assert that execution success only reaches
# awaiting_verification and that terminal tasks cannot reopen.
grep -q 'StatusAwaitingVerification' internal/tasks/state_machine.go
grep -q 'ErrRequirementsUnsatisfied' internal/tasks/postgres/mutate.go

echo "durable task fitness checks passed"
