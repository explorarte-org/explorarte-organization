#!/usr/bin/env bash
# Canonical registry immutability guard, delta-scoped.
#
# Audits ONLY the docs/canonical changes introduced by the change under
# test, relative to the base commit given as $1 (or resolved through
# resolve-task-base.sh when omitted). History that is already merged stays
# out of scope: this guard protects NEW changes, it does not re-litigate
# August.
set -euo pipefail

fail() { echo "ERROR: $*" >&2; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ $# -ge 1 && -n "${1:-}" ]]; then
  BASE_COMMIT="$1"
else
  BASE_COMMIT="$(bash "$SCRIPT_DIR/resolve-task-base.sh")"
fi
git cat-file -e "${BASE_COMMIT}^{commit}" 2>/dev/null ||
  fail "base commit $BASE_COMMIT does not exist in this repository"

mapfile -t canonical_changes < <({
  git diff --name-only "${BASE_COMMIT}" -- docs/canonical
  git ls-files --others --exclude-standard -- docs/canonical
} | sort -u)

for path in "${canonical_changes[@]}"; do
  case "$path" in
    docs/canonical/capability-matrix.yaml|docs/canonical/model-routing.yaml|docs/canonical/model-egress-policy.yaml|docs/canonical/model-execution-identity-policy.yaml) ;;
    # R30 resolves D-007 (docs/canonical/decisions-required.yaml:resolved)
    # with the owner's exact decision text and docs/adr/ADR-0006-hybrid-
    # logic-ir-shadow.md — a deliberate, documented governance action,
    # not an incidental edit. D-005 stays open/untouched in that same
    # file.
    docs/canonical/decisions-required.yaml) ;;
    *) fail "unauthorized canonical change: $path" ;;
  esac
done

echo "canonical immutability ok (base ${BASE_COMMIT:0:12}, ${#canonical_changes[@]} changed files in scope)"
