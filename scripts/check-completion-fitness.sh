#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
BASE_SHA="${COMPLETION_BASE_SHA:-76ad1e04b9f06108233bc5b6b7f4f486b0a99d82}"
TIP_SHA="${COMPLETION_TIP_SHA:-292495132d69757a87e1e86aff57f62dadb72fcc}"
fail() { echo "completion fitness: $*" >&2; exit 1; }
command -v rg >/dev/null 2>&1 || fail "ripgrep is required"
git cat-file -e "${BASE_SHA}^{commit}" 2>/dev/null || fail "base commit ${BASE_SHA} is unavailable"
git cat-file -e "${TIP_SHA}^{commit}" 2>/dev/null || fail "tip commit ${TIP_SHA} is unavailable"
git merge-base --is-ancestor "$BASE_SHA" "$TIP_SHA" || fail "pinned pre-R16/R16 history is not linear"
git merge-base --is-ancestor "$TIP_SHA" HEAD || fail "pinned R16 tip is not an ancestor of HEAD"

for path in \
  internal/completion/types.go \
  internal/completion/ports.go \
  internal/completion/service.go \
  internal/completion/errors.go \
  internal/completion/postgres/store.go \
  cmd/orgctl/completion.go; do
  test -f "$path" || fail "required file missing: $path"
done

# R16 must not redefine canonical vocabulary.
mapfile -t canonical_changes < <({
  git diff --name-only "$BASE_SHA..$TIP_SHA" -- docs/canonical
  git ls-files --others --exclude-standard -- docs/canonical
} | sort -u)
if [[ ${#canonical_changes[@]} -gt 0 ]]; then
  fail "unauthorized canonical change: ${canonical_changes[*]}"
fi

# The whole point of R16 is independent re-verification: it must read
# internal/tasks, internal/staging, internal/authorization and
# internal/decisiongraph's tables directly, never trust their Go APIs (which
# only ever expose what those packages already believe about themselves).
# This must hold in non-test code, both in the domain package and at the CLI
# composition root. Integration tests are exempt: they legitimately drive
# decisiongraph's real Service to reach a genuine succeeded run for fixtures,
# the same precedent internal/decisiongraphtrace's own integration test set.
for pkg in tasks staging authorization decisiongraph; do
  if rg -l "\"github.com/Mireuz13/explorarte-organization/internal/${pkg}\"" internal/completion --glob '*.go' --glob '!**/*_test.go' 2>/dev/null | grep -q .; then
    fail "internal/completion imports internal/${pkg} directly instead of reading its tables"
  fi
  if rg -l "\"github.com/Mireuz13/explorarte-organization/internal/${pkg}\"" cmd/orgctl/completion.go 2>/dev/null | grep -q .; then
    fail "cmd/orgctl/completion.go imports internal/${pkg} directly instead of going through internal/completion"
  fi
done

# Verify() must be read-only: R16 reports a verdict, it never mutates any of
# the four systems of record it reads from.
if rg -n '\b(INSERT|UPDATE|DELETE|TRUNCATE)\b' internal/completion/postgres/store.go; then
  fail "internal/completion/postgres/store.go contains a mutating SQL statement"
fi

# No network, shell, or credential surface.
if rg -n '"net/http"|"os/exec"|exec\.Command|/bin/(sh|bash)|sh -c|bash -c' internal/completion --glob '*.go'; then
  fail "network or subprocess execution found in internal/completion"
fi
if rg -n 'internal/secrets|internal/modelruntime/adapter|openaicompat|api[_-]?key|bearer token' internal/completion --glob '*.go'; then
  fail "internal/completion crossed the provider or credential boundary"
fi

# Closed obligation vocabulary must match reasoning-assurance.yaml Phase 2's
# scope exactly — five items, no more, no less.
for literal in requirements_satisfied artifact_exists checks_passed approval_present no_rejected_branch_reused; do
  rg -q "ObligationID = \"${literal}\"" internal/completion/types.go \
    || fail "obligation missing from Go vocabulary: ${literal}"
done
rg -q '^\s*-\s*requirements_satisfied$' docs/canonical/reasoning-assurance.yaml \
  || fail "reasoning-assurance.yaml no longer lists requirements_satisfied under phase 2 scope"

# Verdict semantics: a contradicted obligation must fail the whole
# verification, never be silently downgraded to pass/inconclusive. Checked
# structurally (the switch's first case), not just by test name, since a
# regex over aggregateVerdict's body is easy to satisfy without the real
# short-circuit-on-fail behavior.
rg -qU 'case LabelContradicted:\s*\n\s*return VerdictFail' internal/completion/service.go \
  || fail "aggregateVerdict does not return VerdictFail immediately on a contradicted obligation"

for test_name in \
  TestVerifyPassesWhenEverythingIndependentlyConfirmed \
  TestVerifyFailsWhenRequiredRequirementNotSatisfied \
  TestVerifyFailsOnArtifactDigestMismatch \
  TestVerifyFailsWhenArtifactMissingFromStaging \
  TestVerifyFailsWhenCheckDidNotPass \
  TestVerifyFailsWhenApprovalNotConsumedOrDigestMismatches \
  TestVerifyFailsWhenSelectedBranchWasLaterRejected \
  TestVerifyTreatsMissingDecisionRunAsVacuouslyVerified \
  TestVerifyIsInconclusiveWhenArtifactDigestNeverRecorded; do
  rg -q "$test_name" internal/completion || fail "required test missing: $test_name"
done
rg -q 'TestCompletionStorePostgreSQL17' internal/completion/postgres || fail "required integration test missing: TestCompletionStorePostgreSQL17"

echo "completion fitness: OK"
