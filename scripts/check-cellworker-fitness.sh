#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
BASE_SHA="${CELLWORKER_BASE_SHA:-73b2b612e002b90f04ef3c4230a22560d65ef0ca}"
fail() { echo "cellworker fitness: $*" >&2; exit 1; }
command -v rg >/dev/null 2>&1 || fail "ripgrep is required"
git cat-file -e "${BASE_SHA}^{commit}" 2>/dev/null || fail "base commit ${BASE_SHA} is unavailable"

for path in \
  internal/cellworker/interfaces.go \
  internal/cellworker/worker.go \
  internal/cellworker/backoff.go \
  internal/cellworker/config.go \
  internal/cellworker/env.go \
  internal/cellworker/postgres/store.go \
  cmd/orgctl/worker.go; do
  test -f "$path" || fail "required file missing: $path"
done

# No new canonical documents, no migration: the worker adds no durable state
# of its own. Eligibility, claims and dispatch quota stay owned by Ramas
# 08-11's tables; this branch only reads them.
mapfile -t canonical_changes < <({
  git diff --name-only "$BASE_SHA" -- docs/canonical
  git ls-files --others --exclude-standard -- docs/canonical
} | sort -u)
if [[ ${#canonical_changes[@]} -gt 0 ]]; then
  fail "unauthorized canonical change: ${canonical_changes[*]}"
fi
if git diff --name-only "$BASE_SHA" -- migrations | grep -q . || git ls-files --others --exclude-standard -- migrations | grep -q .; then
  fail "unauthorized migration change: the persistent worker requires no new durable state"
fi

# The composition root and the real provider adapter stay untouched — this is
# a separate, explicitly-launched CLI process (orgctl model worker run), not
# a change to orgd's always-on loop or Rama 12's adapter boundary.
git diff --exit-code "$BASE_SHA" -- cmd/orgd internal/app internal/modelruntime/adapter >/dev/null \
  || fail "orgd, application composition, or the provider adapter changed"

# The worker package must stay behind the Dispatcher/WorkSource ports: no
# direct network, shell, or provider-specific knowledge.
if rg -n '"net/http"' internal/cellworker --glob '*.go'; then
  fail "HTTP client found in internal/cellworker"
fi
if rg -n '"os/exec"|exec\.Command|/bin/(sh|bash)|sh -c|bash -c' internal/cellworker; then
  fail "shell or subprocess execution found in internal/cellworker"
fi
if rg -n '(openaicompat|deepseek|alibaba_token_plan|claude_code)' internal/cellworker --glob '*.go'; then
  fail "internal/cellworker must not know about a specific provider"
fi
if rg -n '"github.com/Mireuz13/explorarte-organization/internal/secrets"' internal/cellworker --glob '*.go'; then
  fail "internal/cellworker must not touch credential material directly"
fi

# Statelessness: the worker package must not persist anything itself between
# Run calls (no database/sql driver imports outside the postgres WorkSource,
# no package-level mutable state).
if rg -n '"github.com/jackc/pgx' internal/cellworker/interfaces.go internal/cellworker/worker.go internal/cellworker/backoff.go internal/cellworker/config.go internal/cellworker/env.go 2>/dev/null; then
  fail "the pure worker core must not depend on pgx directly"
fi

# The Dispatcher port must match modelruntime.DispatchService.Dispatch
# exactly, so any *modelruntime.DispatchService satisfies it with no adapter
# shim.
rg -q 'Dispatch\(ctx context\.Context, invocationID int64\) \(modelruntime\.DispatchResult, error\)' internal/cellworker/interfaces.go \
  || fail "Dispatcher signature drifted from modelruntime.DispatchService.Dispatch"

# The postgres WorkSource must never treat a legacy unpinned invocation as
# eligible for any principal, and must scope by organization and active
# principal status.
store=internal/cellworker/postgres/store.go
rg -q "p.status = 'active'" "$store" || fail "WorkSource does not require an active principal"
rg -q "mi.organization_id = \\\$1" "$store" || fail "WorkSource is not organization-scoped"
rg -q "p\.id = mi\.execution_principal_id" "$store" || fail "WorkSource does not join on the pinned execution principal"

for test_name in \
  TestWorkerDispatchesEligibleInvocations \
  TestWorkerRespectsConcurrencyLimit \
  TestWorkerGracefulShutdownDrainsInFlight \
  TestWorkerRecoveryAfterRestartIsStateless \
  TestCellWorkerPostgresWorkSource; do
  rg -q "$test_name" internal/cellworker internal/cellworker/postgres || fail "required test missing: $test_name"
done

echo "cellworker fitness: OK"
