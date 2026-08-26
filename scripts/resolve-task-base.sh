#!/usr/bin/env bash
# Resolves the base commit a task/change is audited against.
#
# Precedence:
#   1. TASK_ENGINE_BASE_COMMIT, when explicitly configured (must exist).
#   2. pull_request context: merge-base of HEAD with the PR's target branch,
#      which is exactly what GitHub's own "Files changed" view diffs against.
#   3. Otherwise the merge-base with origin/main; when HEAD already sits on
#     that mainline (running ON main), the base is HEAD itself -- the
#     current state is the baseline, and history must NOT be re-audited as
#     if it belonged to the present task.
#   4. No origin/main resolvable: HEAD (nothing to audit).
#
# The printed commit always exists in this repository.
set -euo pipefail

fail() { echo "ERROR: $*" >&2; exit 1; }

have_commit() { git cat-file -e "$1^{commit}" 2>/dev/null; }

if [[ -n "${TASK_ENGINE_BASE_COMMIT:-}" ]]; then
  have_commit "$TASK_ENGINE_BASE_COMMIT" ||
    fail "TASK_ENGINE_BASE_COMMIT=$TASK_ENGINE_BASE_COMMIT does not exist in this repository"
  echo "$TASK_ENGINE_BASE_COMMIT"
  exit 0
fi

base_ref=""
if [[ -n "${GITHUB_BASE_REF:-}" ]]; then
  base_ref="origin/${GITHUB_BASE_REF}"
elif git rev-parse --verify -q origin/main >/dev/null; then
  base_ref="origin/main"
fi

if [[ -n "$base_ref" ]] && git rev-parse --verify -q "$base_ref" >/dev/null; then
  if merge_base="$(git merge-base HEAD "$base_ref" 2>/dev/null)" && [[ -n "$merge_base" ]]; then
    if [[ "$merge_base" == "$(git rev-parse HEAD)" ]]; then
      # On the mainline: audit the current state against itself, never the
      # whole history since August.
      echo "$(git rev-parse HEAD)"
      exit 0
    fi
    echo "$merge_base"
    exit 0
  fi
fi

echo "$(git rev-parse HEAD)"
