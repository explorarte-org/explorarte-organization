#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

export ORG_POSTGRES_ADMIN_USER=explorarte_test_admin
export ORG_POSTGRES_ADMIN_PASSWORD=integration-admin-password
export ORG_POSTGRES_DATABASE=explorarte_test
export ORG_POSTGRES_USER=explorarte_app
export ORG_POSTGRES_PASSWORD=integration-app-password

compose=(docker compose --project-name explorarte-org-integration -f compose.yaml -f compose.integration.yaml --profile integration)
cleanup(){ "${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true; }
trap cleanup EXIT INT TERM
cleanup
"${compose[@]}" up -d --wait postgres
"${compose[@]}" run --rm integration-test go test -count=1 -tags=integration ./internal/platform/postgres
"${compose[@]}" run --rm integration-test go test -count=1 -tags=integration ./internal/organization/registry
"${compose[@]}" run --rm integration-test sh -ec '
  export ORG_DATABASE_URL="$ORG_TEST_DATABASE_URL" ORG_CANONICAL_DIR=/src/docs/canonical
  go build -buildvcs=false -trimpath -o /tmp/orgctl ./cmd/orgctl
  /tmp/orgctl migrate up
  /tmp/orgctl registry validate
  set +e
  /tmp/orgctl registry diff
  code=$?
  set -e
  test "$code" -eq 3
  /tmp/orgctl registry sync --apply
  /tmp/orgctl registry sync --apply
  /tmp/orgctl registry status
  /tmp/orgctl registry list-units --json >/tmp/units.json
  /tmp/orgctl registry list-roles --unit ingenieria_ia --json >/tmp/roles.json
  /tmp/orgctl registry get-role ingenieria_ia/orquestador --json >/tmp/role.json
  /tmp/orgctl registry get-leader ingenieria_ia --json >/tmp/leader.json
'
