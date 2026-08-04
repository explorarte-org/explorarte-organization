#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
MODE="${1:-all}"
case "$MODE" in
  all|tasks) ;;
  *) echo "usage: $0 [all|tasks]" >&2; exit 2 ;;
esac

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

if [[ "$MODE" == all ]]; then
  "${compose[@]}" run --rm integration-test go test -count=1 -tags=integration ./internal/platform/postgres
  "${compose[@]}" run --rm integration-test go test -count=1 -tags=integration ./internal/organization/registry
fi
"${compose[@]}" run --rm integration-test go test -count=1 -tags=integration ./internal/tasks/postgres

if [[ "$MODE" == all ]]; then
  "${compose[@]}" run --rm integration-test sh -ec '
    export ORG_DATABASE_URL="$ORG_TEST_DATABASE_URL" ORG_CANONICAL_DIR=/src/docs/canonical
    go build -buildvcs=false -trimpath -o /tmp/orgctl ./cmd/orgctl
    /tmp/orgctl migrate up
    /tmp/orgctl registry validate
    set +e
    /tmp/orgctl registry diff
    code=$?
    set -e
    if [ "$code" -eq 3 ]; then
      /tmp/orgctl registry sync --apply
    else
      test "$code" -eq 0
    fi
    /tmp/orgctl registry sync --apply
    /tmp/orgctl registry status --json >/tmp/registry-status.json
    /tmp/orgctl registry get-role ingenieria_ia/orquestador --json >/tmp/role.json
    /tmp/orgctl registry get-leader ingenieria_ia --json >/tmp/leader.json
    cat >/tmp/task.json <<JSON
{"assigned_role_id":"ingenieria_ia/qa","idempotency_key":"cli-smoke-1","title":"CLI smoke task","instructions":"Validate durable task CLI wiring.","acceptance_criteria":["task persists"]}
JSON
    /tmp/orgctl task create --file /tmp/task.json --actor-id integration --json >/tmp/task-created.json
    /tmp/orgctl task list --status ready --json >/tmp/tasks.json
    /tmp/orgctl task reconcile --batch 100 --json >/tmp/reconcile.json
    /tmp/orgctl outbox status --json >/tmp/outbox.json
    grep -Fq "\"status\": \"ready\"" /tmp/task-created.json
    grep -Fq "\"pending\"" /tmp/outbox.json
  '
fi
