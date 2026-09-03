#!/usr/bin/env bash
# Canonical registry change guard, delta-scoped.
#
# Every change under docs/canonical is a governance action, never an
# incidental edit. This guard accepts one ONLY when a commit in the audited
# range carries an explicit approval trailer naming the owner role:
#
#     Canonical-Change-Approved-By: empresa/human
#
# Before this revision the guard exempted capability-matrix.yaml,
# model-routing.yaml, model-egress-policy.yaml and
# model-execution-identity-policy.yaml -- the tier-2 documents that grant
# authority and route models -- while freezing the organigram. That was
# inverted: the documents a self-modifying organization must never change
# silently are precisely the ones that widen what it may do. No file is
# exempt now; the approval trailer is the only door.
#
# Audits ONLY the docs/canonical changes introduced by the change under
# test, relative to the base commit given as $1 (or resolved through
# resolve-task-base.sh when omitted). Merged history stays out of scope.
#
# When merging with squash, the trailer must survive in the squash message;
# a PR whose approval lives only in a squashed-away commit fails on main.
set -euo pipefail

fail() { echo "ERROR: $*" >&2; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TRAILER='^Canonical-Change-Approved-By: empresa/human[[:space:]]*$'

if [[ $# -ge 1 && -n "${1:-}" ]]; then
  BASE_COMMIT="$1"
elif [[ -n "${TASK_ENGINE_BASE_COMMIT:-}" ]]; then
  BASE_COMMIT="$TASK_ENGINE_BASE_COMMIT"
else
  BASE_COMMIT="$(bash "$SCRIPT_DIR/resolve-task-base.sh")"
fi
git cat-file -e "${BASE_COMMIT}^{commit}" 2>/dev/null ||
  fail "base commit $BASE_COMMIT does not exist in this repository"

mapfile -t canonical_changes < <({
  git diff --name-only "${BASE_COMMIT}" -- docs/canonical
  git ls-files --others --exclude-standard -- docs/canonical
} | sort -u)

if [[ ${#canonical_changes[@]} -eq 0 ]]; then
  echo "canonical immutability ok (base ${BASE_COMMIT:0:12}, no canonical changes in scope)"
  exit 0
fi

if ! git log --format=%B "${BASE_COMMIT}..HEAD" | grep -Eq "$TRAILER"; then
  fail "canonical change without owner approval trailer (Canonical-Change-Approved-By: empresa/human): ${canonical_changes[*]}"
fi

echo "canonical change approved by trailer (base ${BASE_COMMIT:0:12}, ${#canonical_changes[@]} changed files: ${canonical_changes[*]})"
