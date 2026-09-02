#!/usr/bin/env bash
# build-deployment-image.sh (G6-002): computes VERSION/COMMIT/BUILD_TIME
# from `git rev-parse HEAD` in the given worktree directly -- never an
# operator-typed string -- and refuses to build a dirty or unexpected
# checkout, so a deployed image's provenance is provable rather than
# resting on undocumented operator discipline.
#
# Background: every deploy this session set --build-arg VERSION by hand
# from a manually-copy-pasted `git rev-parse --short HEAD`, and never set
# COMMIT or BUILD_TIME at all -- every running container's own /version
# endpoint and startup log has said commit=unknown its entire history.
# The production checkout is also routinely left detached at a stale
# commit between deploys, so "the worktree I'm building from" and "the
# commit I intend to ship" are two facts a human was trusting matched,
# not something checked.
#
# Usage:
#   build-deployment-image.sh <worktree-dir> [--expect-commit <sha>] [--target <name>] [--tag-prefix <name>]
#
#   worktree-dir     path to the git worktree/checkout to build from
#   --expect-commit  if given, refuse to build unless HEAD is exactly this
#                     (full or abbreviated) SHA -- catches "I meant to
#                     build the commit I just merged, but this checkout
#                     is actually still on the previous one"
#   --target         Dockerfile build target (default: unset -- the
#                     Dockerfile's default final stage, orgd)
#   --tag-prefix     image name prefix (default: explorarte-organization)
#
# On success, prints the resolved image tag to stdout as the last line
# (nothing else goes to stdout), so a caller can capture it directly:
#   TAG=$(build-deployment-image.sh /path/to/worktree)
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <worktree-dir> [--expect-commit <sha>] [--target <name>] [--tag-prefix <name>]" >&2
  exit 2
fi

worktree="$1"
shift
expect_commit=""
target=""
tag_prefix="explorarte-organization"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --expect-commit) expect_commit="$2"; shift 2 ;;
    --target) target="$2"; shift 2 ;;
    --tag-prefix) tag_prefix="$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

if [[ ! -d "$worktree/.git" && ! -f "$worktree/.git" ]]; then
  echo "refusing: $worktree is not a git worktree" >&2
  exit 2
fi

if [[ -n "$(git -C "$worktree" status --porcelain)" ]]; then
  echo "refusing: $worktree has uncommitted changes -- the image built from a dirty worktree would not correspond to any real commit" >&2
  git -C "$worktree" status --short >&2
  exit 1
fi

commit="$(git -C "$worktree" rev-parse HEAD)"
version="$(git -C "$worktree" rev-parse --short HEAD)"
build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

if [[ -n "$expect_commit" ]]; then
  case "$commit" in
    "$expect_commit"*) ;;
    *)
      echo "refusing: expected HEAD to be $expect_commit, but $worktree is actually at $commit" >&2
      exit 1
      ;;
  esac
fi

tag="${tag_prefix}:${version}"
build_args=(--build-arg "VERSION=${version}" --build-arg "COMMIT=${commit}" --build-arg "BUILD_TIME=${build_time}")
if [[ -n "$target" ]]; then
  build_args+=(--target "$target")
  tag="${tag_prefix}-${target}:${version}"
fi

docker build "${build_args[@]}" -t "$tag" "$worktree" >&2

echo "$tag"
