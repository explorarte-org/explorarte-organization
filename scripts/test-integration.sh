#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
MODE="${1:-all}"
case "$MODE" in
  all|tasks|staging|authorization|context|model|egress|dispatch|identity|worker|decision|trace|improvement|completion) ;;
  *) echo "usage: $0 [all|tasks|staging|authorization|context|model|egress|dispatch|identity|worker|decision|trace|improvement|completion]" >&2; exit 2 ;;
esac

export ORG_POSTGRES_ADMIN_USER=explorarte_test_admin
export ORG_POSTGRES_ADMIN_PASSWORD=integration-admin-password
export ORG_POSTGRES_DATABASE=explorarte_test
export ORG_POSTGRES_USER=explorarte_app
export ORG_POSTGRES_PASSWORD=integration-app-password

compose=(docker compose --project-name explorarte-org-integration -f compose.yaml -f compose.integration.yaml --profile integration)

cleanup() {
  "${compose[@]}" down --remove-orphans >/dev/null 2>&1 || true
  docker volume rm -f explorarte-org-integration-postgres-data >/dev/null 2>&1 || true
}

trap cleanup EXIT INT TERM
cleanup
"${compose[@]}" up -d --wait postgres

if [[ "$MODE" == all ]]; then
  timeout --foreground --signal=TERM --kill-after=30s 15m "${compose[@]}" run --rm -T integration-test go test -count=1 -tags=integration ./internal/platform/postgres
  timeout --foreground --signal=TERM --kill-after=30s 15m "${compose[@]}" run --rm -T integration-test go test -count=1 -tags=integration ./internal/organization/registry
fi
if [[ "$MODE" == all || "$MODE" == tasks ]]; then
  timeout --foreground --signal=TERM --kill-after=30s 15m "${compose[@]}" run --rm -T integration-test go test -count=1 -tags=integration ./internal/tasks/postgres
fi
if [[ "$MODE" == all || "$MODE" == staging ]]; then
  timeout --foreground --signal=TERM --kill-after=30s 20m "${compose[@]}" run --rm -T integration-test go test -count=1 -tags=integration ./internal/staging/postgres
fi
if [[ "$MODE" == all || "$MODE" == authorization ]]; then
  timeout --foreground --signal=TERM --kill-after=30s 20m "${compose[@]}" run --rm -T integration-test go test -count=1 -tags=integration ./internal/authorization/postgres
fi
if [[ "$MODE" == all || "$MODE" == context ]]; then
  timeout --foreground --signal=TERM --kill-after=30s 25m "${compose[@]}" run --rm -T integration-test go test -count=1 -tags=integration ./internal/contextengine/postgres
fi
if [[ "$MODE" == all || "$MODE" == egress ]]; then
  timeout --foreground --signal=TERM --kill-after=30s 30m "${compose[@]}" run --rm -T integration-test go test -count=1 -tags=integration ./internal/modelegress/postgres
fi
if [[ "$MODE" == all || "$MODE" == model ]]; then
  timeout --foreground --signal=TERM --kill-after=30s 30m "${compose[@]}" run --rm -T integration-test go test -count=1 -tags=integration ./internal/modelruntime/postgres
fi
if [[ "$MODE" == all || "$MODE" == dispatch ]]; then
  timeout --foreground --signal=TERM --kill-after=30s 30m "${compose[@]}" run --rm -T integration-test go test -count=1 -tags=integration ./internal/modeldispatch/postgres
fi
if [[ "$MODE" == all || "$MODE" == identity ]]; then
  timeout --foreground --signal=TERM --kill-after=30s 30m "${compose[@]}" run --rm -T integration-test go test -count=1 -tags=integration ./internal/modelidentity/postgres
fi
if [[ "$MODE" == all || "$MODE" == worker ]]; then
  timeout --foreground --signal=TERM --kill-after=30s 30m "${compose[@]}" run --rm -T integration-test go test -count=1 -tags=integration ./internal/cellworker/postgres
fi
if [[ "$MODE" == all || "$MODE" == decision ]]; then
  timeout --foreground --signal=TERM --kill-after=30s 30m "${compose[@]}" run --rm -T integration-test go test -count=1 -tags=integration ./internal/decisiongraph/postgres
fi
if [[ "$MODE" == all || "$MODE" == trace ]]; then
  timeout --foreground --signal=TERM --kill-after=30s 15m "${compose[@]}" run --rm -T integration-test go test -count=1 -tags=integration ./internal/decisiongraphtrace
fi
if [[ "$MODE" == all || "$MODE" == improvement ]]; then
  timeout --foreground --signal=TERM --kill-after=30s 15m "${compose[@]}" run --rm -T integration-test go test -count=1 -tags=integration ./internal/improvement/postgres
fi
if [[ "$MODE" == all || "$MODE" == completion ]]; then
  timeout --foreground --signal=TERM --kill-after=30s 15m "${compose[@]}" run --rm -T integration-test go test -count=1 -tags=integration ./internal/completion/postgres
fi

if [[ "$MODE" == all ]]; then
  timeout --foreground --signal=TERM --kill-after=30s 15m "${compose[@]}" run --rm -T integration-test sh -ec '
    export ORG_DATABASE_URL="$ORG_TEST_DATABASE_URL" ORG_CANONICAL_DIR=/src/docs/canonical
    # Model and identity CLI commands bootstrap the shared context runtime even
    # when they do not build a context. Provide the integration source root
    # before the first orgctl invocation instead of falling back to the
    # production-only /opt/explorarte/organization path.
    mkdir -p /tmp/context-source/ingenieria_ia/qa
    cat >/tmp/context-source/AGENT.md <<EOF
# Organization Agent
Follow canonical organizational policies.
EOF
    cat >/tmp/context-source/ingenieria_ia/AGENT.md <<EOF
# Engineering Agent
Follow department scope.
EOF
    cat >/tmp/context-source/ingenieria_ia/qa/PERFIL.md <<EOF
---
departamento: ingenieria_ia
rol: qa
dominio_memoria: ingenieria_ia
agente_base: true
---
# QA profile
Verify artifacts and evidence.
EOF
    export ORG_CONTEXT_SOURCE_ROOT=/tmp/context-source
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
    /tmp/orgctl model registry validate --json >/tmp/model-registry-validate.json
    set +e
    /tmp/orgctl model registry diff --json >/tmp/model-registry-diff.json
    model_code=$?
    set -e
    if [ "$model_code" -eq 3 ]; then
      /tmp/orgctl model registry sync --apply --json >/tmp/model-registry-sync.json
    else
      test "$model_code" -eq 0
    fi
    /tmp/orgctl model registry sync --apply --json >/tmp/model-registry-sync-noop.json
    /tmp/orgctl model registry status --json >/tmp/model-registry-status.json
    /tmp/orgctl model egress validate --json >/tmp/model-egress-validate.json
    set +e
    /tmp/orgctl model egress diff --json >/tmp/model-egress-diff.json
    egress_code=$?
    set -e
    if [ "$egress_code" -eq 3 ]; then
      /tmp/orgctl model egress sync --apply --json >/tmp/model-egress-sync.json
    else
      test "$egress_code" -eq 0
    fi
    /tmp/orgctl model egress sync --apply --json >/tmp/model-egress-sync-noop.json
    /tmp/orgctl model egress status --json >/tmp/model-egress-status.json
    grep -Fq "\"synchronized\": true" /tmp/model-egress-status.json
    /tmp/orgctl model identity policy validate --json >/tmp/model-identity-policy-validate.json
    set +e
    /tmp/orgctl model identity policy diff --json >/tmp/model-identity-policy-diff.json
    identity_code=$?
    set -e
    if [ "$identity_code" -eq 3 ]; then
      /tmp/orgctl model identity policy sync --apply --json >/tmp/model-identity-policy-sync.json
    else
      test "$identity_code" -eq 0
    fi
    /tmp/orgctl model identity policy sync --apply --json >/tmp/model-identity-policy-sync-noop.json
    /tmp/orgctl model identity policy status --json >/tmp/model-identity-policy-status.json
    grep -Fq "\"synchronized\": true" /tmp/model-identity-policy-status.json
    /tmp/orgctl decision recover --limit 1 --json >/tmp/decision-recover.json
    grep -Fq "\"recovered\": 0" /tmp/decision-recover.json
    for forbidden in provider policy policy-id policy-version transport classifications effect url api-key; do
      set +e
      /tmp/orgctl model egress validate --"$forbidden" forbidden >/tmp/model-egress-forbidden.out 2>/tmp/model-egress-forbidden.err
      forbidden_code=$?
      set -e
      test "$forbidden_code" -eq 2
    done
    /tmp/orgctl registry get-role ingenieria_ia/orquestador --json >/tmp/role.json
    /tmp/orgctl registry get-leader ingenieria_ia --json >/tmp/leader.json
    cat >/tmp/task.json <<JSON
{"assigned_role_id":"ingenieria_ia/qa","idempotency_key":"cli-smoke-1","title":"CLI smoke task","instructions":"Validate durable task CLI wiring.","acceptance_criteria":["task persists"]}
JSON
    /tmp/orgctl task create --file /tmp/task.json --actor-id integration --json >/tmp/task-created.json
    /tmp/orgctl task list --status ready --json >/tmp/tasks.json
    /tmp/orgctl task reconcile --batch 100 --json >/tmp/reconcile.json
    /tmp/orgctl outbox status --json >/tmp/outbox.json
    action_digest="$(printf %s authorization-cli-smoke | sha256sum | cut -d" " -f1)"
    /tmp/orgctl authorization evaluate --actor-role ingenieria_ia/code-runner --capability code.commit --resource-type code --resource-id cli-smoke --action-digest "$action_digest" --json >/tmp/authorization-evaluate.json
    /tmp/orgctl authorization request --actor-role creativo/copywriter --capability rag.publish_approved --resource-type rag_candidate --resource-id cli-smoke --action-digest "$action_digest" --idempotency-key cli-authorization-smoke --reason "CI one-time approval" --json >/tmp/authorization-request.json
    request_id="$(grep -m1 "\"id\"" /tmp/authorization-request.json | tr -cd "0-9")"
    /tmp/orgctl authorization decide "$request_id" --decision approve --actor-role empresa/human --reason "CI owner approval" --json >/tmp/authorization-decision.json
    /tmp/orgctl authorization consume "$request_id" --actor-role creativo/copywriter --action-digest "$action_digest" --json >/tmp/authorization-consume.json
    grep -Fq "\"status\": \"ready\"" /tmp/task-created.json
    grep -Fq "\"pending\"" /tmp/outbox.json
    /tmp/orgctl context build --actor-role ingenieria_ia/qa --purpose "CLI context smoke" --idempotency-key cli-context-smoke --json >/tmp/context-created.json
    context_id="$(grep -m1 "\"id\"" /tmp/context-created.json | tr -cd "0-9")"
    /tmp/orgctl context get "$context_id" --json >/tmp/context-get.json
    /tmp/orgctl context validate "$context_id" --json >/tmp/context-validation.json
    /tmp/orgctl context render "$context_id" --output /tmp/context-render.json
    cat >/tmp/principal.json <<JSON
{"organization_id":"explorarte","principal_key":"ci/model-runtime-smoke","dispatch_actor_role_id":"ingenieria_ia/code-runner","principal_kind":"local_process","idempotency_key":"cli-principal-smoke"}
JSON
    /tmp/orgctl model principal register --file /tmp/principal.json --actor-role empresa/human --json >/tmp/principal-created.json
    principal_id="$(grep -m1 "\"id\"" /tmp/principal-created.json | tr -cd "0-9")"
    /tmp/orgctl model principal get "$principal_id" --json >/tmp/principal-get.json
    /tmp/orgctl model principal list --organization-id explorarte --json >/tmp/principal-list.json
    grep -Fq "\"status\": \"active\"" /tmp/principal-get.json
    set +e
    /tmp/orgctl model principal register --file /tmp/principal.json --actor-role ingenieria_ia/code-runner --json >/tmp/principal-denied.out 2>/tmp/principal-denied.err
    denied_code=$?
    set -e
    test "$denied_code" -ne 0
    for forbidden in claimed-by principal assignment; do
      set +e
      /tmp/orgctl model invocation dispatch 1 --"$forbidden" x >/tmp/dispatch-forbidden.out 2>/tmp/dispatch-forbidden.err
      forbidden_code=$?
      set -e
      test "$forbidden_code" -eq 2
    done
  '
fi
