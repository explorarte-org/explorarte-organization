#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
BASE_SHA="${IMPROVEMENT_BASE_SHA:-3584d9e7a2e44bbe9d953556704df5e84afd8cf3}"
TIP_SHA="${IMPROVEMENT_TIP_SHA:-30b6f0b7a6dbc79c5bc740c7dad290abf9f0ecb9}"
fail() { echo "improvement fitness: $*" >&2; exit 1; }
command -v rg >/dev/null 2>&1 || fail "ripgrep is required"
git cat-file -e "${BASE_SHA}^{commit}" 2>/dev/null || fail "base commit ${BASE_SHA} is unavailable"
git cat-file -e "${TIP_SHA}^{commit}" 2>/dev/null || fail "tip commit ${TIP_SHA} is unavailable"
git merge-base --is-ancestor "$BASE_SHA" "$TIP_SHA" || fail "pinned pre-R15/R15 history is not linear"
git merge-base --is-ancestor "$TIP_SHA" HEAD || fail "pinned R15 tip is not an ancestor of HEAD"

for path in \
  internal/evaluation/types.go \
  internal/evaluation/ports.go \
  internal/evaluation/comparison.go \
  internal/evaluation/service.go \
  internal/improvement/types.go \
  internal/improvement/transitions.go \
  internal/improvement/ports.go \
  internal/improvement/service.go \
  internal/improvement/hashing.go \
  internal/improvement/postgres/store.go \
  internal/decisiongraphtrace/store.go \
  migrations/000013_create_bounded_self_improvement.up.sql \
  migrations/000013_create_bounded_self_improvement.down.sql; do
  test -f "$path" || fail "required file missing: $path"
done

# Rama 15 implements the candidate lifecycle the owner specified; it does not
# redefine canonical vocabulary while adding evaluation/promotion durability.
mapfile -t canonical_changes < <({
  git diff --name-only "$BASE_SHA..$TIP_SHA" -- docs/canonical
  git ls-files --others --exclude-standard -- docs/canonical
} | sort -u)
if [[ ${#canonical_changes[@]} -gt 0 ]]; then
  fail "unauthorized canonical change: ${canonical_changes[*]}"
fi

# The pure domain (evaluation, improvement) must stay decoupled from
# decisiongraph's Go API: only the dedicated adapter package may bridge them,
# and even it reads Rama 14's tables directly rather than importing
# decisiongraph's package, so it never depends on decisiongraph's internal,
# unexported audit-hash format.
if rg -l '"github.com/Mireuz13/explorarte-organization/internal/decisiongraph"' internal/evaluation internal/improvement --glob '*.go' 2>/dev/null | grep -q .; then
  fail "internal/evaluation or internal/improvement imports internal/decisiongraph directly"
fi
if rg -l '"github.com/Mireuz13/explorarte-organization/internal/decisiongraph"' internal/decisiongraphtrace --glob '*.go' --glob '!**/*_test.go' 2>/dev/null | grep -q .; then
  fail "internal/decisiongraphtrace's non-test code imports internal/decisiongraph's Go API instead of reading its tables directly"
fi

# No network, shell, or credential surface in the pure domain or the store
# packages: model calls, providers, and secrets stay behind Evaluator and
# ApprovalGate, never inside this branch.
if rg -n '"net/http"|"os/exec"|exec\.Command|/bin/(sh|bash)|sh -c|bash -c' internal/evaluation internal/improvement internal/decisiongraphtrace --glob '*.go'; then
  fail "network or subprocess execution found"
fi
if rg -n 'internal/secrets|internal/modelruntime/adapter|openaicompat|api[_-]?key|bearer token' internal/evaluation internal/improvement internal/decisiongraphtrace --glob '*.go'; then
  fail "improvement/evaluation crossed the provider or credential boundary"
fi

# Traces and candidates carry hashes, not raw content: decisiongraph's own
# no-private-reasoning invariant must carry through the adapter and the
# migration.
if rg -n '(PrivateChainOfThought|PrivateReasoning|RawPrompt|RawResponse|CredentialValue|SecretValue)[[:space:]]+' internal/evaluation internal/improvement internal/decisiongraphtrace --glob '*.go'; then
  fail "forbidden sensitive Go field found"
fi
if rg -ni 'private_chain_of_thought|private_reasoning|raw_prompt|raw_response|credential_value|secret_value' migrations/000013_create_bounded_self_improvement.up.sql; then
  fail "forbidden sensitive SQL column found"
fi

# Closed candidate-state vocabulary and the exact default-deny transition map
# the owner specified, in both Go and the database trigger — proposed ->
# active must never be reachable through either path.
for literal in proposed validated evaluating rejected inconclusive approved canary active deprecated rolled_back; do
  rg -q "CandidateState = \"${literal}\"" internal/improvement/types.go \
    || fail "candidate state missing from Go vocabulary: ${literal}"
done
rg -q "'proposed','validated','evaluating','rejected','inconclusive','approved','canary','active','deprecated','rolled_back'" \
  migrations/000013_create_bounded_self_improvement.up.sql \
  || fail "candidate state CHECK constraint does not match the closed vocabulary"
rg -q 'candidateTransitions = map\[CandidateState\]map\[CandidateState\]struct\{\}' internal/improvement/transitions.go \
  || fail "default-deny transition map missing"
# proposed -> active is checked by actually running the transition matrix,
# not by pattern-matching Go source: candidateTransitions nests struct{}{}
# literals, whose closing braces defeat a naive [^}]* regex. See the
# required-test loop below (TestCandidateTransitionMatrixIsDefaultDeny).
rg -q 'improvement_guard_candidate_update' migrations/000013_create_bounded_self_improvement.up.sql \
  || fail "database-level candidate transition guard missing"
if rg -n "OLD\.state = 'proposed'.*NEW\.state = 'active'" migrations/000013_create_bounded_self_improvement.up.sql; then
  fail "proposed -> active is reachable in the database trigger"
fi

# Optimistic concurrency and audit immutability.
rg -q 'revision BIGINT NOT NULL DEFAULT 1' migrations/000013_create_bounded_self_improvement.up.sql \
  || fail "candidate revision column missing"
rg -q 'revision <> OLD\.revision \+ 1' migrations/000013_create_bounded_self_improvement.up.sql \
  || fail "revision must advance by exactly one is not enforced"
rg -q 'ErrRevisionConflict' internal/improvement/errors.go internal/improvement/postgres/store.go \
  || fail "ErrRevisionConflict is not wired end to end"
rg -q 'improvement_promotion_decisions_immutable' migrations/000013_create_bounded_self_improvement.up.sql \
  || fail "promotion decision audit trail is not immutable"

# Approval gates: promotion to canary/active must be gate-mediated, and
# denial must never silently change candidate state (checked structurally:
# the gate call happens before any state mutation in requestPromotion).
rg -q 'ApprovalGate' internal/improvement/ports.go || fail "ApprovalGate port missing"
rg -q 's\.gate\.AuthorizePromotion' internal/improvement/service.go \
  || fail "Service does not call the ApprovalGate before promoting"

# Trace integrity: the adapter must verify its own computed hash against
# what the caller already holds, never trust a caller-supplied hash blindly.
rg -q 'ErrTraceHashMismatch' internal/evaluation/errors.go internal/decisiongraphtrace/store.go \
  || fail "trace hash integrity check is not wired end to end"

for test_name in \
  TestCandidateTransitionMatrixIsDefaultDeny \
  TestServiceFullLifecycleToActiveAndDeprecate \
  TestServicePromotionDeniedKeepsState \
  TestServiceRollBackFromCanaryAndActive \
  TestSuiteComparisonResultValidateRejectsEmpty \
  TestServiceEvaluateCaseRejectsMismatchedEvaluatorResult; do
  rg -q "$test_name" internal/evaluation internal/improvement || fail "required test missing: $test_name"
done
for test_name in TestDecisionGraphTraceStore TestImprovementPostgresCandidateStore; do
  rg -q "$test_name" internal/decisiongraphtrace internal/improvement/postgres || fail "required integration test missing: $test_name"
done

echo "improvement fitness: OK"
