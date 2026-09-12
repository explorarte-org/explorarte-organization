#!/usr/bin/env bash
# check-gofmt-only-protected-diff.sh
#
# Returns 0 (protected paths did not change in a prohibited way) when EITHER:
#   A. no tracked change exists under the given protected path roots relative
#      to BASE, or
#   B. every tracked change under those roots is individually provable as a
#      plain modification of an EXISTING .go file where current == gofmt(BASE
#      version of that same file), with the file mode and object type
#      unchanged.
#
# This does NOT mean "gofmt changes are globally allowed" under these roots.
# It means one specific, individually-verified, format-only modification can
# be excluded from the blanket "these paths must not change" scope lock the
# caller enforces. Any other change under the same roots -- a new file, a
# deletion, a rename, a copy, a mode/type change, or a .go file whose content
# differs from gofmt(BASE) by even one byte -- still counts as a violation.
#
# Usage: check-gofmt-only-protected-diff.sh <base-commit> <protected-path>...
# Exit 0: no violation (case A or every change is B). Exit 1: violation, or
# any internal error (fail-closed -- an error is never treated as success).
set -euo pipefail

fail_closed() {
  echo "check-gofmt-only-protected-diff: $*" >&2
  exit 1
}

if [[ $# -lt 2 ]]; then
  fail_closed "usage: $0 <base-commit> <protected-path>..."
fi

BASE="$1"
shift
PROTECTED_PATHS=("$@")

command -v gofmt >/dev/null 2>&1 || fail_closed "gofmt is required"
git cat-file -e "${BASE}^{commit}" 2>/dev/null || fail_closed "base commit ${BASE} is unavailable"

WORKDIR="$(mktemp -d)" || fail_closed "mktemp failed"
trap 'rm -rf "$WORKDIR"' EXIT

# An untracked file under a protected root is invisible to `git diff
# <commit>` (it only ever compares tracked/staged content), so it would
# otherwise slip past every check below undetected. Treat any such file as
# an unconditional violation -- exactly like a tracked add -- rather than
# silently ignoring it.
if ! untracked_output="$(git ls-files --others --exclude-standard -- "${PROTECTED_PATHS[@]}")"; then
  fail_closed "git ls-files against protected paths failed"
fi
if [[ -n "$untracked_output" ]]; then
  while IFS= read -r untracked_path; do
    [[ -z "$untracked_path" ]] && continue
    echo "check-gofmt-only-protected-diff: ${untracked_path}: untracked file under a protected path" >&2
  done <<<"$untracked_output"
  exit 1
fi

# --no-renames: a rename or copy must be judged as a delete of the old path
# plus an add of the new one -- both of which are already, unconditionally,
# never eligible for the format-only exception below. Special-casing "this
# was a rename" would only complicate the logic without changing the
# required outcome (FAIL).
if ! diff_output="$(git diff --no-renames --name-status "$BASE" -- "${PROTECTED_PATHS[@]}")"; then
  fail_closed "git diff against ${BASE} failed"
fi

if [[ -z "$diff_output" ]]; then
  # Case A: nothing changed under the protected roots at all.
  exit 0
fi

violation=0
while IFS=$'\t' read -r status path; do
  [[ -z "$path" ]] && continue

  if [[ "$status" != "M" ]]; then
    # Add / Delete / anything but a plain modification of an existing path
    # is never eligible, regardless of what it contains.
    echo "check-gofmt-only-protected-diff: ${path}: not a plain modification (status=${status})" >&2
    violation=1
    continue
  fi

  case "$path" in
  *.go) ;;
  *)
    echo "check-gofmt-only-protected-diff: ${path}: not a .go file" >&2
    violation=1
    continue
    ;;
  esac

  if ! raw_line="$(git diff --no-renames --raw "$BASE" -- "$path")" || [[ -z "$raw_line" ]]; then
    echo "check-gofmt-only-protected-diff: ${path}: unable to read raw diff metadata" >&2
    violation=1
    continue
  fi
  # Raw format: ":<old-mode> <new-mode> <old-sha> <new-sha> <status>\t<path>"
  old_mode="$(awk '{print $1}' <<<"$raw_line" | tr -d ':')"
  new_mode="$(awk '{print $2}' <<<"$raw_line")"

  if [[ -z "$old_mode" || -z "$new_mode" ]]; then
    echo "check-gofmt-only-protected-diff: ${path}: unable to parse file modes" >&2
    violation=1
    continue
  fi
  if [[ "$old_mode" != "$new_mode" ]]; then
    echo "check-gofmt-only-protected-diff: ${path}: file mode changed (${old_mode} -> ${new_mode})" >&2
    violation=1
    continue
  fi
  if [[ "$old_mode" != "100644" && "$old_mode" != "100755" ]]; then
    # Anything that is not a normal regular-file blob mode -- a symlink
    # (120000), a submodule gitlink (160000), or anything else -- is never
    # eligible, even if both sides report the same (non-blob) mode.
    echo "check-gofmt-only-protected-diff: ${path}: not a normal blob (mode=${old_mode})" >&2
    violation=1
    continue
  fi

  if [[ ! -f "$path" ]]; then
    echo "check-gofmt-only-protected-diff: ${path}: missing from current tree" >&2
    violation=1
    continue
  fi

  base_copy="$WORKDIR/$(echo "$path" | tr '/' '_')"
  if ! git show "${BASE}:${path}" >"$base_copy" 2>/dev/null; then
    echo "check-gofmt-only-protected-diff: ${path}: does not exist at ${BASE}" >&2
    violation=1
    continue
  fi

  gofmt_copy="${base_copy}.gofmt"
  if ! gofmt "$base_copy" >"$gofmt_copy" 2>/dev/null; then
    echo "check-gofmt-only-protected-diff: ${path}: gofmt failed on base version" >&2
    violation=1
    continue
  fi

  if ! cmp -s "$path" "$gofmt_copy"; then
    echo "check-gofmt-only-protected-diff: ${path}: current content is not exactly gofmt(base)" >&2
    violation=1
    continue
  fi
done <<<"$diff_output"

if [[ "$violation" -ne 0 ]]; then
  exit 1
fi

exit 0
