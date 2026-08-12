#!/usr/bin/env bash
# Fitness check for the immutable Context Engine document source baked into
# the orgd image (ORG_CONTEXT_SOURCE_ROOT=/opt/explorarte/context-source).
# Verifies, from the built image alone (no running container needed):
#   1. the context source root exists;
#   2. every organization/department AGENT.md required by organization.yaml
#      is present;
#   3. .env is absent from the entire image;
#   4. .git is absent from the entire image;
#   5. no app-level data/backups/artifacts/tmp directory leaked into the
#      image (the base distroless image's own empty /tmp, /var/tmp,
#      /var/backups are expected and excluded from the check).
#
# Usage: scripts/verify-context-source-image.sh [image[:tag]]
set -euo pipefail

IMAGE="${1:-explorarte-organization:r31-baseline}"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

cid=$(docker create "$IMAGE")
docker export "$cid" > "$WORKDIR/image.tar"
docker rm "$cid" >/dev/null

listing="$WORKDIR/listing.txt"
tar -tf "$WORKDIR/image.tar" > "$listing"

fail=0

check_present() {
  if ! grep -qx "$1" "$listing"; then
    echo "FAIL: missing required path: $1"
    fail=1
  fi
}

check_absent_pattern() {
  local pattern="$1" label="$2"
  local hits
  hits=$(grep -iE "$pattern" "$listing" || true)
  if [ -n "$hits" ]; then
    echo "FAIL: forbidden path present ($label):"
    echo "$hits"
    fail=1
  fi
}

echo "== 1. context source root exists =="
check_present "opt/explorarte/context-source/"

echo "== 2. required organization/department AGENT.md present =="
check_present "opt/explorarte/context-source/AGENT.md"
for unit in negocio ingenieria_ia recursos_agenticos servicios empresa investigacion; do
  check_present "opt/explorarte/context-source/${unit}/AGENT.md"
done

echo "== 3. .env absent from image =="
check_absent_pattern '(^|/)\.env($|\.)' ".env"

echo "== 4. .git absent from image =="
check_absent_pattern '(^|/)\.git(/|$)' ".git"

echo "== 5. no app-level data/backups/artifacts/tmp leaked (scoped to opt/explorarte, excludes base-image system paths) =="
check_absent_pattern '^opt/explorarte/.*(^|/)(data|backups|artifacts|tmp)/' "app data/backups/artifacts/tmp dir under opt/explorarte"

if [ "$fail" -ne 0 ]; then
  echo "RESULT: FAIL"
  exit 1
fi
echo "RESULT: PASS"
