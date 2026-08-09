#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
BASE_SHA="${DECISIONGRAPH_BASE_SHA:-3584d9e7a2e44bbe9d953556704df5e84afd8cf3}"
TIP_SHA="${DECISIONGRAPH_TIP_SHA:-1b8cbd399684f3b61af7d5f2bff15a06b83c1e75}"
fail() { echo "decisiongraph fitness: $*" >&2; exit 1; }
command -v rg >/dev/null 2>&1 || fail "ripgrep is required"
git cat-file -e "${BASE_SHA}^{commit}" 2>/dev/null || fail "base commit ${BASE_SHA} is unavailable"
git cat-file -e "${TIP_SHA}^{commit}" 2>/dev/null || fail "tip commit ${TIP_SHA} is unavailable"
git merge-base --is-ancestor "$BASE_SHA" "$TIP_SHA" || fail "pinned R13/R14 history is not linear"
git merge-base --is-ancestor "$TIP_SHA" HEAD || fail "pinned R14 tip is not an ancestor of HEAD"

for path in \
  internal/decisiongraph/types.go \
  internal/decisiongraph/graph.go \
  internal/decisiongraph/budget.go \
  internal/decisiongraph/records.go \
  internal/decisiongraph/ports.go \
  internal/decisiongraph/service.go \
  internal/decisiongraph/postgres/store.go \
  migrations/000012_create_durable_decision_graph.up.sql \
  migrations/000012_create_durable_decision_graph.down.sql \
  internal/decisiongraph/postgres/integration_test.go \
  cmd/orgctl/decision.go; do
  test -f "$path" || fail "required file missing: $path"
done

# Capa 14 implements the vocabulary already approved in reasoning-assurance;
# it does not rewrite canonical authority while adding its durable ledger.
mapfile -t canonical_changes < <({
  git diff --name-only "$BASE_SHA..$TIP_SHA" -- docs/canonical
  git ls-files --others --exclude-standard -- docs/canonical
} | sort -u)
if [[ ${#canonical_changes[@]} -gt 0 ]]; then
  fail "unauthorized canonical change: ${canonical_changes[*]}"
fi

# The graph ledger coordinates existing runtime invocations; it must not own
# network transport, provider credentials, shell execution, or provider choice.
if rg -n '"net/http"|"os/exec"|exec\.Command|/bin/(sh|bash)|sh -c|bash -c' internal/decisiongraph --glob '*.go'; then
  fail "network or subprocess execution found in internal/decisiongraph"
fi
if rg -n 'internal/(secrets|modelruntime/adapter)|openaicompat|api[_-]?key|bearer token' internal/decisiongraph --glob '*.go'; then
  fail "decision graph crossed the provider or credential boundary"
fi

# The durable trace is structured and hash-based. Documentation may state the
# privacy rule, but executable structs and SQL columns must not define fields
# for private reasoning, raw prompts/responses, or credential material.
if rg -n '(PrivateChainOfThought|PrivateReasoning|RawPrompt|RawResponse|CredentialValue|SecretValue)[[:space:]]+' internal/decisiongraph --glob '*.go'; then
  fail "forbidden sensitive Go field found"
fi
if rg -ni 'private_chain_of_thought|private_reasoning|raw_prompt|raw_response|credential_value|secret_value' migrations/000012_create_durable_decision_graph.up.sql; then
  fail "forbidden sensitive SQL column found"
fi
if rg -n 'claim_token[[:space:]]+TEXT' migrations/000012_create_durable_decision_graph.up.sql; then
  fail "raw claim token column found"
fi
rg -q 'claim_token_hash TEXT NOT NULL' migrations/000012_create_durable_decision_graph.up.sql \
  || fail "hashed claim token column missing"
rg -q 'sha256\.Sum256\(\[\]byte\(token\)\)' internal/decisiongraph/postgres/store.go \
  || fail "claim tokens are not hashed before persistence"

# Canonical closed vocabulary and scheduler semantics.
for literal in goal requirement constraint hypothesis candidate_action evidence verification decision; do
  rg -q "NodeType = \"${literal}\"" internal/decisiongraph/types.go || fail "node type missing: ${literal}"
done
for literal in depends_on supports contradicts satisfies prunes selected_from; do
  rg -q "EdgeType = \"${literal}\"" internal/decisiongraph/types.go || fail "edge type missing: ${literal}"
done
rg -q 'if g\.hasDependencyCycle\(\)' internal/decisiongraph/graph.go || fail "dependency cycle validation missing"
rg -q 'decision_graph_edges_cycle_guard' migrations/000012_create_durable_decision_graph.up.sql || fail "database cycle guard missing"
rg -q 'TransitionBranch' internal/decisiongraph/ports.go internal/decisiongraph/service.go internal/decisiongraph/postgres/store.go || fail "durable branch transition port missing"
rg -q 'decision_branch_events' migrations/000012_create_durable_decision_graph.up.sql internal/decisiongraph/postgres/store.go || fail "branch transition ledger missing"
rg -q 'string_agg' internal/decisiongraph/postgres/store.go || fail "TraceRef does not commit to the structured trace ledger"
rg -q "r.status='succeeded'" internal/decisiongraph/postgres/store.go || fail "TraceRef is not restricted to terminal successful runs"

# Claims and budgets are durable, concurrency-safe, and consumed before work.
store=internal/decisiongraph/postgres/store.go
rg -q 'FOR UPDATE OF n SKIP LOCKED' "$store" || fail "ready-node claim is not SKIP LOCKED"
rg -q 'active_parallel_nodes=active_parallel_nodes\+1' "$store" || fail "parallel budget is not reserved at claim"
rg -q 'used_model_calls=used_model_calls\+1' "$store" || fail "model-call budget is not reserved at claim"
rg -q 'decision_budget_events' "$store" || fail "append-only budget event ledger missing"
rg -q 'used_wall_time_ms=used_wall_time_ms' "$store" || fail "wall-time budget is not consumed"
rg -q 'wall_time_ms_delta' "$store" || fail "wall-time budget events are missing"
rg -q 'case "decision"' cmd/orgctl/main.go || fail "orgctl decision command is not wired"
rg -q 'DisallowUnknownFields' cmd/orgctl/decision.go || fail "decision JSON input is not strict"
rg -q 'readSecretToken\(os\.Stdin\)' cmd/orgctl/decision.go || fail "decision finish token is not read from stdin"
if rg -n 'json:"claim_token"' cmd/orgctl/decision.go; then
  fail "decision claim token must not be accepted from a JSON file"
fi
rg -q 'ExecutionAmbiguous' internal/decisiongraph/transitions.go || fail "ambiguous terminal state missing"
if rg -n 'ExecutionAmbiguous.*ExecutionReady|status=.requested.*ambiguous|retry.*ambiguous' internal/decisiongraph --glob '*.go' --glob '!**/*_test.go'; then
  fail "ambiguous outcomes appear retryable"
fi

# Decision graph completion never completes the parent task and never performs
# tool intents; terminal task authority remains in the durable task engine.
if rg -ni 'UPDATE[[:space:]]+tasks|status[[:space:]]*=[[:space:]]*.completed.|tool_intent' internal/decisiongraph --glob '*.go' --glob '!**/*_test.go'; then
  fail "decision graph attempted task completion or tool execution"
fi

for test_name in \
  TestGraphRejectsDependencyCycle \
  TestReadyNodeIDsRequireSucceededDependencies \
  TestRejectedBranchRequiresNewEvidenceToReopen \
  TestAmbiguousExecutionIsTerminal \
  TestBudgetReserveIsAtomic \
  TestDecisionGraphPostgresLedger; do
  rg -q "$test_name" internal/decisiongraph || fail "required test missing: $test_name"
done

echo "decisiongraph fitness: OK"
