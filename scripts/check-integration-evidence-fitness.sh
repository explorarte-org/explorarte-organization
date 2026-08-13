#!/usr/bin/env bash
set -euo pipefail

# Behavioural proof that the integration harness collects evidence rather
# than merely reporting a single exit code.
#
# Every check here has two halves: the property must hold, and neutralising
# the code that provides it must break the check. A fitness test that passes
# both before and after removing the thing it guards is decoration, and this
# project has already been bitten by controls that were only ever asserted,
# never exercised.
#
# The synthetic manifests keep these checks fast and Docker-free. That is not
# a shortcut: the accounting logic under test is exactly the same code the
# real run uses, and driving it with trivial commands is what lets us inject
# an early failure, a late failure, and an unobserved unit deterministically.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

HARNESS="scripts/test-integration.sh"
WORK="$(mktemp -d)"
# The mutated copy must live inside the repo so the harness resolves $ROOT
# to this worktree and finds its own manifests.
MUTANT="scripts/.fitness-harness.sh"
trap 'rm -rf "$WORK"; rm -f "$MUTANT"' EXIT

fail() { echo "integration-evidence fitness: FAIL: $*" >&2; exit 1; }
note() { echo "  $*"; }

field() { python3 -c "import json,sys;print(json.load(open(sys.argv[1]))[sys.argv[2]])" "$1" "$2"; }
suite_status() {
  python3 -c "
import json,sys
d=json.load(open(sys.argv[1]))
print(next((s['status'] for s in d['suites'] if s['id']==sys.argv[2]), 'ABSENT'))" "$1" "$2"
}

write_manifests() {
  printf 'id\tclass\tcommand\nalways-ok\tSAFETY\ttrue\n' > "$WORK/pre-ok.tsv"
  printf 'id\tclass\tcommand\nbroken-guard\tSAFETY\tfalse\n' > "$WORK/pre-unsafe.tsv"
  {
    printf 'id\tmodes\tkind\ttimeout\tdepends_on\tcommand\n'
    printf 'unit-a\tall\tintegration\t30s\t-\tfalse\n'
    printf 'unit-b\tall\tintegration\t30s\t-\ttrue\n'
    printf 'unit-c\tall\tintegration\t30s\t-\ttrue\n'
    printf 'unit-d\tall\tintegration\t30s\t-\tfalse\n'
  } > "$WORK/suites-mixed.tsv"
  {
    printf 'id\tmodes\tkind\ttimeout\tdepends_on\tcommand\n'
    printf 'unit-a\tall\tintegration\t30s\t-\ttrue\n'
    printf 'unit-b\tall\tintegration\t30s\t-\ttrue\n'
    printf 'unit-c\tall\tintegration\t30s\t-\ttrue\n'
  } > "$WORK/suites-green.tsv"
}

run_harness() {
  local script="$1" pre="$2" suites="$3" manifest="$4"
  set +e
  ORG_INTEGRATION_PRECONDITIONS_FILE="$pre" \
  ORG_INTEGRATION_SUITES_FILE="$suites" \
  ORG_INTEGRATION_MANIFEST="$manifest" \
    "$script" all > "$WORK/stdout.txt" 2>&1
  local rc=$?
  set -e
  printf '%s' "$rc"
}

write_manifests

# ---------------------------------------------------------------------
echo "--- fitness 1: independent failures are aggregated, not truncated ---"
# ---------------------------------------------------------------------
rc="$(run_harness "$HARNESS" "$WORK/pre-ok.tsv" "$WORK/suites-mixed.tsv" "$WORK/agg.json")"
[[ "$rc" != "0" ]] || fail "a run with two failing units exited 0"
[[ "$(suite_status "$WORK/agg.json" unit-a)" == FAIL ]] || fail "early failure not recorded"
[[ "$(suite_status "$WORK/agg.json" unit-d)" == FAIL ]] || fail "late failure not recorded: the run stopped at the first failure"
[[ "$(suite_status "$WORK/agg.json" unit-b)" == PASS ]] || fail "unit after the early failure did not run"
[[ "$(field "$WORK/agg.json" final_status)" == COMPLETE_WITH_FAILURES ]] || fail "expected COMPLETE_WITH_FAILURES"
[[ "$(field "$WORK/agg.json" evidence_complete)" == True ]] || fail "every unit was observed, evidence should be complete"
note "both the early and the late failure are present; status COMPLETE_WITH_FAILURES"

# Mutation: restore fail-fast by aborting the suite loop on the first
# non-zero result. The late failure then becomes unobservable.
cp "$HARNESS" "$MUTANT"
python3 - "$MUTANT" <<'PY'
import sys
p = sys.argv[1]; s = open(p).read()
old = '''    if [[ $rc -eq 0 ]]; then
      UNIT_STATUS["$id"]="PASS"
    else
      UNIT_STATUS["$id"]="FAIL"
    fi'''
assert s.count(old) == 1, "mutation anchor not found"
new = old.replace('      UNIT_STATUS["$id"]="FAIL"',
                  '      UNIT_STATUS["$id"]="FAIL"\n      return 0  # MUTATION: fail-fast')
open(p, "w").write(s.replace(old, new))
PY
chmod +x "$MUTANT"
run_harness "$MUTANT" "$WORK/pre-ok.tsv" "$WORK/suites-mixed.tsv" "$WORK/agg-mut.json" >/dev/null
if [[ "$(suite_status "$WORK/agg-mut.json" unit-d)" == FAIL ]]; then
  fail "mutation restored fail-fast yet the late failure was still reported; fitness 1 proves nothing"
fi
[[ "$(field "$WORK/agg-mut.json" final_status)" != COMPLETE_WITH_FAILURES ]] \
  || fail "a truncated run must not be reported as COMPLETE_WITH_FAILURES"
note "mutation (fail-fast restored) is detected: late failure disappears, status degrades"
rm -f "$MUTANT"

# ---------------------------------------------------------------------
echo "--- fitness 2: a failed safety precondition stops everything ---"
# ---------------------------------------------------------------------
rc="$(run_harness "$HARNESS" "$WORK/pre-unsafe.tsv" "$WORK/suites-green.tsv" "$WORK/safe.json")"
[[ "$rc" != "0" ]] || fail "a safety abort exited 0"
[[ "$(field "$WORK/safe.json" final_status)" == SAFETY_ABORT ]] || fail "expected SAFETY_ABORT"
[[ "$(field "$WORK/safe.json" evidence_complete)" == False ]] || fail "an aborted run must not claim complete evidence"
for unit in unit-a unit-b unit-c; do
  [[ "$(suite_status "$WORK/safe.json" "$unit")" == UNKNOWN ]] \
    || fail "$unit ran after a safety precondition failed"
done
note "no suite was attempted; status SAFETY_ABORT, evidence_complete=false"

# Mutation: let the run continue after a failed safety precondition.
cp "$HARNESS" "$MUTANT"
python3 - "$MUTANT" <<'PY'
import sys
p = sys.argv[1]; s = open(p).read()
old = '''if run_preconditions; then
  run_suites
fi'''
assert s.count(old) == 1, "mutation anchor not found"
open(p, "w").write(s.replace(old, 'run_preconditions || true  # MUTATION: no hard abort\nrun_suites'))
PY
chmod +x "$MUTANT"
run_harness "$MUTANT" "$WORK/pre-unsafe.tsv" "$WORK/suites-green.tsv" "$WORK/safe-mut.json" >/dev/null
if [[ "$(suite_status "$WORK/safe-mut.json" unit-a)" == UNKNOWN ]]; then
  fail "mutation removed the hard abort yet no suite ran; fitness 2 proves nothing"
fi
note "mutation (abort removed) is detected: suites run after an unsafe environment"
rm -f "$MUTANT"

# ---------------------------------------------------------------------
echo "--- fitness 3: an unobserved unit cannot be reported as green ---"
# ---------------------------------------------------------------------
# This is the epistemological core. Every executed unit passes and the loop
# itself has nothing to complain about, but one expected unit was never
# observed. Trusting the runner's own exit code here is exactly the mistake
# that let a partial trunk view look complete.
cp "$HARNESS" "$MUTANT"
python3 - "$MUTANT" <<'PY'
import sys
p = sys.argv[1]; s = open(p).read()
old = '''  for id in "${UNIT_IDS[@]}"; do
    if ! applies_to_mode "${UNIT_MODES[$id]}"; then'''
assert s.count(old) == 1, "mutation anchor not found"
new = '''  for id in "${UNIT_IDS[@]}"; do
    [[ "$id" == unit-b ]] && continue  # MUTATION: silently unobserved
    if ! applies_to_mode "${UNIT_MODES[$id]}"; then'''
open(p, "w").write(s.replace(old, new))
PY
chmod +x "$MUTANT"
rc="$(run_harness "$MUTANT" "$WORK/pre-ok.tsv" "$WORK/suites-green.tsv" "$WORK/incomplete.json")"
[[ "$(suite_status "$WORK/incomplete.json" unit-b)" == UNKNOWN ]] \
  || fail "a unit that was never attempted must remain UNKNOWN"
[[ "$(field "$WORK/incomplete.json" final_status)" != COMPLETE_GREEN ]] \
  || fail "a run missing one expected unit was reported COMPLETE_GREEN"
[[ "$(field "$WORK/incomplete.json" final_status)" == INCOMPLETE_RUN ]] \
  || fail "expected INCOMPLETE_RUN"
[[ "$(field "$WORK/incomplete.json" evidence_complete)" == False ]] || fail "evidence must not be claimed complete"
[[ "$(field "$WORK/incomplete.json" accounting_complete)" == True ]] \
  || fail "the unit is still accounted for; accounting completeness is a separate claim"
[[ "$rc" != "0" ]] || fail "an incomplete run exited 0"
note "unobserved unit stays UNKNOWN; status INCOMPLETE_RUN with accounting_complete=true"
rm -f "$MUTANT"

# Control: the same green manifest, unmutated, must reach COMPLETE_GREEN --
# otherwise fitness 3 would pass simply because nothing can ever be green.
rc="$(run_harness "$HARNESS" "$WORK/pre-ok.tsv" "$WORK/suites-green.tsv" "$WORK/green.json")"
[[ "$rc" == "0" ]] || fail "an all-passing observed run did not exit 0"
[[ "$(field "$WORK/green.json" final_status)" == COMPLETE_GREEN ]] || fail "expected COMPLETE_GREEN"
[[ "$(field "$WORK/green.json" evidence_complete)" == True ]] || fail "expected complete evidence"
note "control: the same manifest without the mutation does reach COMPLETE_GREEN"

echo "integration-evidence fitness: PASS"
