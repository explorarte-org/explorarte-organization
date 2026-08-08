#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# R23 starts from the integrated R21/R22 main tip. New executive hardening must
# not smuggle in migrations, authority widening or provider coupling.
base=3090966772da048a6245200dcef4bcfe42c9d22c
core=(internal/executive/*.go)

fail() {
  echo "executive fitness: $*" >&2
  exit 1
}

for pattern in \
  'internal/modelruntime/adapter' \
  'alibabaclaude' \
  'openaicompat' \
  'deepseek' \
  '"net/http"' \
  '"os/exec"' \
  'exec\.Command' \
  'http\.NewRequest' \
  'sql\.Exec' \
  'pgx\.' \
  'database/sql'; do
  if rg -n "$pattern" "${core[@]}"; then
    fail "forbidden concrete provider/network/shell/SQL dependency: $pattern"
  fi
done

for field in provider model transport endpoint credential api_key reasoning_effort egress_override capability_grant authority approval_decision direct_publish direct_promote shell command sql; do
  if rg -n "json:\\?\"${field}" internal/executive/types.go internal/executive/schemas.go; then
    fail "model-controlled output field exposed: $field"
  fi
done

completed_calls="$(rg -n 'o\.tasks\.FinalizeCompleted\(' internal/executive/orchestrator.go || true)"
if [[ "$(printf '%s\n' "$completed_calls" | sed '/^$/d' | wc -l | tr -d ' ')" != 1 ]]; then
  printf '%s\n' "$completed_calls" >&2
  fail "FinalizeCompleted must have exactly one core call site behind gatedComplete"
fi
if ! rg -n 'func \(o \*Orchestrator\) gatedComplete' internal/executive/orchestrator.go >/dev/null; then
  fail "CompletionGate wrapper missing"
fi
if ! rg -n 'o\.completion\.Verify\(' internal/executive/orchestrator.go >/dev/null; then
  fail "completion verifier call missing"
fi

if ! rg -n 'case "ambiguous"' internal/executive/orchestrator.go >/dev/null || \
   ! rg -n 'ErrModelOutcomeAmbiguous' internal/executive/orchestrator.go >/dev/null; then
  fail "ambiguous model outcome is not explicitly fail-closed"
fi
if rg -n 'ambiguous.*retry|retry.*ambiguous' internal/executive --glob '*.go' --glob '!**/*_test.go'; then
  fail "ambiguous automatic retry detected"
fi

if ! rg -n 'orphaned_model_result' internal/executive/recovery.go >/dev/null || \
   ! rg -n 'findOrphanedSucceededInvocation' internal/executive/recovery.go >/dev/null; then
  fail "orphaned succeeded invocation recovery guard missing"
fi
if ! rg -n 'BudgetModels' internal/executive/bootstrap/runtime.go >/dev/null || \
   ! rg -n 'incrementInvocationBudget' internal/executive/runtimeadapter/budget_models.go >/dev/null; then
  fail "durable prospective invocation budget guard missing"
fi
if ! rg -n 'DAGTasks' internal/executive/bootstrap/runtime.go >/dev/null || \
   ! rg -n 'command\.Dependencies = append\(\[\]int64\{sourceID\}' internal/executive/runtimeadapter/dag_tasks.go >/dev/null; then
  fail "worker source dependency guard missing"
fi
if ! rg -n 'EvidenceTasks' internal/executive/bootstrap/runtime.go >/dev/null || \
   ! rg -n 'executive-evidence:' internal/executive/runtimeadapter/evidence_tasks.go >/dev/null; then
  fail "executive evidence projector missing"
fi
if ! rg -n 'strings\.HasPrefix\(e\.Reference, "executive-evidence:"\)' internal/tasks/contextprovider/provider.go >/dev/null; then
  fail "TaskContextProvider must expose metadata only for executive evidence bundles"
fi

if rg -n 'ceo_observer' internal/executive --glob '*.go' --glob '!**/*_test.go' | grep -v 'ObserverRoleID = ' | grep -v 'role.ID == ObserverRoleID'; then
  fail "productive CEO observer path detected"
fi
if rg -n 'daily[_A-Za-z]*cycle|dailyCycle|scheduler' internal/executive --glob '*.go' --glob '!**/*_test.go'; then
  fail "daily scheduler path detected"
fi
if rg -n 'internal/(memory|rag)/postgres|FROM (memory|rag_)' internal/executive --glob '*.go' --glob '!**/*_test.go'; then
  fail "direct memory/RAG persistence read detected"
fi
if rg -n 'DispatchActorRoleID:.*OwnerRoleID|ActorRoleID:.*OwnerRoleID.*model\.dispatch' internal/executive --glob '*.go'; then
  fail "raw owner impersonation detected"
fi

if git diff --name-only "$base"...HEAD -- docs/canonical/capability-matrix.yaml | grep -q .; then
  fail "capability-matrix widening/change is forbidden in R23"
fi
if git diff --name-only "$base"...HEAD -- internal/decisiongraph | grep -q .; then
  fail "R14 internals changed by R23"
fi
if git diff --name-only "$base"...HEAD -- migrations | grep -q .; then
  fail "R23 must not add or reserve persistence migrations"
fi

if ! rg -n 'TrustUntrusted' internal/tasks/contextprovider/provider.go >/dev/null || \
   ! rg -n 'MayGrantCapabilities: false' internal/tasks/contextprovider/provider.go >/dev/null; then
  fail "task/model-derived context must remain untrusted and non-authoritative"
fi

if rg -n 'ToolIntents.*execute|execute.*ToolIntents' internal/executive --glob '*.go' --glob '!**/*_test.go'; then
  fail "automatic ToolIntent execution path detected"
fi

echo "executive fitness: PASS"
