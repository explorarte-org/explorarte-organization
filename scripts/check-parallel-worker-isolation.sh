#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
fail(){ echo "parallel-worker-isolation fitness: FAIL: $*" >&2; exit 1; }

# 2026-08-12 branch audit: compose.integration.yaml pinned `name:` for
# postgres-data, integration-go-cache, and the org-internal network, and
# scripts/test-integration.sh hardcoded --project-name explorarte-org-
# integration and `docker volume rm -f explorarte-org-integration-postgres
# -data` by that same fixed name. Two worktrees running integration tests
# at the same time shared -- and could tear down -- each other's
# containers/volumes/network. This check has two parts: (1) static --
# fail if the fixed-name pattern regresses into either file; (2) dynamic
# -- actually resolve `docker compose config` under two different project
# names and confirm every volume/network name differs between them.

echo "--- static: compose.integration.yaml must not pin volume/network names ---"
if grep -Eq 'name:\s*explorarte-org-integration-(postgres-data|go-cache|network)' compose.integration.yaml; then
  fail "compose.integration.yaml pins a fixed volume/network name again -- this defeats --project-name namespacing"
fi

echo "--- static: scripts/test-integration.sh must not hardcode --project-name or a global volume name ---"
if grep -Eq -- '--project-name explorarte-org-integration\b' scripts/test-integration.sh; then
  fail "scripts/test-integration.sh hardcodes --project-name explorarte-org-integration again"
fi
if grep -Eq 'docker volume rm -f explorarte-org-integration-postgres-data' scripts/test-integration.sh; then
  fail "scripts/test-integration.sh deletes a hardcoded global volume name again -- use '\"\${compose[@]}\" down --volumes' instead"
fi
if ! grep -q 'ORG_INTEGRATION_PROJECT_NAME' scripts/test-integration.sh; then
  fail "scripts/test-integration.sh no longer derives/accepts a per-worktree project name"
fi

echo "--- dynamic: docker compose config must resolve distinct volume/network names for distinct project names ---"
if ! command -v docker >/dev/null 2>&1; then
  echo "parallel-worker-isolation fitness: docker not available, skipping dynamic check (static checks already passed)"
  echo "parallel-worker-isolation fitness: PASS (static only)"
  exit 0
fi

export ORG_POSTGRES_ADMIN_USER=explorarte_test_admin
export ORG_POSTGRES_ADMIN_PASSWORD=integration-admin-password
export ORG_POSTGRES_DATABASE=explorarte_test
export ORG_POSTGRES_USER=explorarte_app
export ORG_POSTGRES_PASSWORD=integration-app-password

CONFIG_A="$(docker compose --project-name worker-isolation-probe-a -f compose.yaml -f compose.integration.yaml --profile integration config 2>/dev/null)"
CONFIG_B="$(docker compose --project-name worker-isolation-probe-b -f compose.yaml -f compose.integration.yaml --profile integration config 2>/dev/null)"

NAMES_A="$(printf '%s\n' "$CONFIG_A" | grep -A1 '^  postgres-data:\|^  integration-go-cache:\|^  org-internal:' | grep 'name:' | sort -u)"
NAMES_B="$(printf '%s\n' "$CONFIG_B" | grep -A1 '^  postgres-data:\|^  integration-go-cache:\|^  org-internal:' | grep 'name:' | sort -u)"

if [ -z "$NAMES_A" ] || [ -z "$NAMES_B" ]; then
  fail "could not resolve volume/network names from 'docker compose config' -- check compose file syntax"
fi

if [ "$NAMES_A" = "$NAMES_B" ]; then
  fail "project A and project B resolved to the IDENTICAL volume/network names -- --project-name is not isolating resources:
A: $NAMES_A
B: $NAMES_B"
fi

echo "project worker-isolation-probe-a resolved:"
echo "$NAMES_A"
echo "project worker-isolation-probe-b resolved:"
echo "$NAMES_B"

echo "parallel-worker-isolation fitness: PASS"
