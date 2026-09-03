#!/usr/bin/env bash
# Kernel governance code guard, delta-scoped.
#
# internal/missionplan denies autonomous missions the governance DATA
# (docs/canonical, migrations, scripts, deployments). This guard covers the
# governance CODE: the packages that decide what a mission may touch, who
# may do what, which provider sees which classification, and how the
# organization reads its own registry. A change to any of them is a change
# to what the organization is permitted to do, so it must carry an explicit
# owner approval trailer in the audited range:
#
#     Kernel-Governance-Change-Approved-By: empresa/human
#
# Keep KERNEL_PATHS in sync with kernelGovernancePrefixes in
# internal/missionplan/missionplan.go: that list is what an autonomous
# mission is refused at derivation time; this list is what a human PR is
# refused in CI without the trailer. Both guards protect the same surface
# from two different actors.
set -euo pipefail

fail() { echo "ERROR: $*" >&2; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TRAILER='^Kernel-Governance-Change-Approved-By: empresa/human[[:space:]]*$'

KERNEL_PATHS=(
  internal/authorization
  internal/coderunner
  internal/config
  internal/engineeringmission
  internal/missionplan
  internal/modeldispatch
  internal/modelegress
  internal/modelidentity
  internal/organization
  internal/secrets
  internal/staging
  scripts/check-canonical-immutability.sh
  scripts/check-kernel-governance-fitness.sh
)

if [[ $# -ge 1 && -n "${1:-}" ]]; then
  BASE_COMMIT="$1"
elif [[ -n "${TASK_ENGINE_BASE_COMMIT:-}" ]]; then
  BASE_COMMIT="$TASK_ENGINE_BASE_COMMIT"
else
  BASE_COMMIT="$(bash "$SCRIPT_DIR/resolve-task-base.sh")"
fi
git cat-file -e "${BASE_COMMIT}^{commit}" 2>/dev/null ||
  fail "base commit $BASE_COMMIT does not exist in this repository"

mapfile -t kernel_changes < <({
  git diff --name-only "${BASE_COMMIT}" -- "${KERNEL_PATHS[@]}"
  git ls-files --others --exclude-standard -- "${KERNEL_PATHS[@]}"
} | sort -u)

if [[ ${#kernel_changes[@]} -eq 0 ]]; then
  echo "kernel governance ok (base ${BASE_COMMIT:0:12}, no kernel governance changes in scope)"
  exit 0
fi

if ! git log --format=%B "${BASE_COMMIT}..HEAD" | grep -Eq "$TRAILER"; then
  fail "kernel governance change without owner approval trailer (Kernel-Governance-Change-Approved-By: empresa/human): ${kernel_changes[*]}"
fi

echo "kernel governance change approved by trailer (base ${BASE_COMMIT:0:12}, ${#kernel_changes[@]} changed files: ${kernel_changes[*]})"
