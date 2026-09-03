#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# R23 starts from the integrated R21/R22 main tip. New executive hardening must
# not smuggle in migrations, authority widening or provider coupling.
base=3090966772da048a6245200dcef4bcfe42c9d22c
tip=f19c2b4bede1b255e05a71f9de62093eb078b68e
core=(internal/executive/*.go)

fail() {
  echo "executive fitness: $*" >&2
  exit 1
}

git cat-file -e "${base}^{commit}" 2>/dev/null || fail "R23 base commit is unavailable"
git cat-file -e "${tip}^{commit}" 2>/dev/null || fail "R23 tip commit is unavailable"
git merge-base --is-ancestor "$base" "$tip" || fail "pinned pre-R23/R23 history is not linear"
git merge-base --is-ancestor "$tip" HEAD || fail "pinned R23 tip is not an ancestor of HEAD"

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

if ! rg -n 'invocation\.Status (!=|==) "ambiguous"' internal/executive/orchestrator.go >/dev/null || \
   ! rg -n 'ErrModelOutcomeAmbiguous' internal/executive/orchestrator.go >/dev/null; then
  fail "ambiguous model outcome is not explicitly fail-closed"
fi
# Pure-model ambiguity recovery is an explicit, host-owned policy: it writes a
# per-invocation resolution and only then permits the normal task engine to
# create a fresh attempt. The old text search rejected explanatory comments
# and the auditable resolution reason as if they were an execution path. Keep
# the safety boundary structural instead: the policy must fence unknown
# effects, emit the durable resolution, and must not directly claim/create an
# attempt or record a retry from the ambiguity resolver itself.
ambiguity_source="internal/executive/ambiguity_resolution.go"
if ! rg -n 'class != EffectPureModel' "$ambiguity_source" >/dev/null; then
  fail "ambiguous recovery is not fenced to pure-model effects"
fi
if ! rg -n 'EffectUnknown' "$ambiguity_source" >/dev/null; then
  fail "ambiguous recovery has no explicit unknown-effect class"
fi
if ! rg -n 'AmbiguityDispositionRetryAuthorized' "$ambiguity_source" >/dev/null; then
  fail "ambiguous recovery has no durable retry-resolution disposition"
fi
if rg -n 'RecordAttemptFailed|ClaimTask|CreateTask|CreateAttempt' "$ambiguity_source"; then
  fail "ambiguity resolver directly creates or records a retry attempt"
fi

if ! rg -n 'orphaned_model_result' internal/executive/recovery.go >/dev/null || \
   ! rg -n 'findOrphanedSucceededInvocation' internal/executive/recovery.go >/dev/null; then
  fail "orphaned succeeded invocation recovery guard missing"
fi
if ! rg -n 'ModelCallBudget' internal/executive/bootstrap/runtime.go >/dev/null || \
   ! rg -n 'incrementInvocationBudget' internal/executive/runtimeadapter/model_budget.go >/dev/null; then
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

if git diff --name-only "$base..$tip" -- docs/canonical/capability-matrix.yaml | grep -q .; then
  fail "capability-matrix widening/change is forbidden in R23"
fi
if git diff --name-only "$base..$tip" -- internal/decisiongraph | grep -q .; then
  fail "R14 internals changed by R23"
fi
if git diff --name-only "$base..$tip" -- migrations | grep -q .; then
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
