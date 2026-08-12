#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
MODE="${1:-all}"
case "$MODE" in
  all|tasks|staging|authorization|context|memory|skillregistry|rag|model|egress|dispatch|identity|worker|decision|trace|improvement|completion|shadow) ;;
  *) echo "usage: $0 [all|tasks|staging|authorization|context|memory|skillregistry|rag|model|egress|dispatch|identity|worker|decision|trace|improvement|completion|shadow]" >&2; exit 2 ;;
esac

export ORG_POSTGRES_ADMIN_USER=explorarte_test_admin
export ORG_POSTGRES_ADMIN_PASSWORD=integration-admin-password
export ORG_POSTGRES_DATABASE=explorarte_test
export ORG_POSTGRES_USER=explorarte_app
export ORG_POSTGRES_PASSWORD=integration-app-password
# R31 incident fix: internal/testdbguard.RequireDestructive requires this
# exact value before permitting TRUNCATE/migration-DownSQL. compose.integration.yaml
# also defaults it to explorarte_test on its own, so this export is
# belt-and-suspenders documentation of the same deliberate assertion, not
# strictly required for this script to keep working.
export ORG_TEST_DESTRUCTIVE_DATABASE=explorarte_test

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
if [[ "$MODE" == all || "$MODE" == memory ]]; then
  timeout --foreground --signal=TERM --kill-after=30s 20m "${compose[@]}" run --rm -T integration-test go test -count=1 -tags=integration ./internal/memory/postgres
fi
if [[ "$MODE" == all || "$MODE" == skillregistry ]]; then
  timeout --foreground --signal=TERM --kill-after=30s 20m "${compose[@]}" run --rm -T integration-test go test -count=1 -tags=integration ./internal/skillregistry/postgres
fi
if [[ "$MODE" == all || "$MODE" == rag ]]; then
  timeout --foreground --signal=TERM --kill-after=30s 20m "${compose[@]}" run --rm -T integration-test go test -count=1 -tags=integration ./internal/rag/postgres
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
if [[ "$MODE" == all || "$MODE" == shadow ]]; then
  timeout --foreground --signal=TERM --kill-after=30s 15m "${compose[@]}" run --rm -T integration-test go test -count=1 -tags=integration ./internal/shadowverifier/postgres
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

    admission_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    memory_digest="$(printf %s cli-memory-evidence | sha256sum | cut -d" " -f1)"
    cat >/tmp/memory-propose.json <<JSON
{"id":"cli-memory-smoke","role_id":"ingenieria_ia/orquestador","category":"integration_learning","problem":"A simulated integration failure occurred.","correction":"Use the verified integration correction.","source_kind":"simulation","source_run_id":1,"evidence_refs":[{"reference":"integration:memory:1","digest":"$memory_digest"}],"proposed_by":"ingenieria_ia/orquestador","admission":{"data_class":"organizational","attested_by":"ingenieria_ia/orquestador","source_boundary":"organization","evidence_ref":"integration:memory:admission","attested_at":"$admission_time"},"idempotency_key":"cli-memory-smoke"}
JSON
    /tmp/orgctl memory propose --file /tmp/memory-propose.json --json >/tmp/memory-created.json
    grep -Fq "\"status\": \"candidate\"" /tmp/memory-created.json
    cat >/tmp/memory-review.json <<JSON
{"entry_id":"cli-memory-smoke","expected_revision":1,"actor_role_id":"empresa/human","reason":"CI owner reviewed evidence and admission provenance","outcome":"approve"}
JSON
    /tmp/orgctl memory review --file /tmp/memory-review.json --json >/tmp/memory-approved.json
    grep -Fq "\"status\": \"approved\"" /tmp/memory-approved.json
    /tmp/orgctl memory list --role ingenieria_ia/orquestador --status approved --json >/tmp/memory-list.json
    grep -Fq "cli-memory-smoke" /tmp/memory-list.json

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

    rag_admission_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    rag_digest="$(printf %s cli-rag-evidence | sha256sum | cut -d" " -f1)"
    cat >/tmp/rag-propose.json <<JSON
{"id":"cli-rag-smoke","document_id":"cli-rag-smoke-doc","namespace_kind":"department","namespace_id":"ingenieria_ia","version":1,"title":"CLI RAG smoke knowledge","body":"Antes de desplegar un modelo nuevo, valida la politica de egress y el owner del dataset.","source_kind":"research","source_reference":"investigacion:report:cli-smoke","evidence_refs":[{"reference":"integration:rag:1","digest":"$rag_digest"}],"proposed_by":"empresa/human","admission":{"data_class":"organizational","attested_by":"empresa/human","source_boundary":"organization","evidence_ref":"integration:rag:admission","attested_at":"$rag_admission_time"},"idempotency_key":"cli-rag-smoke"}
JSON
    /tmp/orgctl rag propose --file /tmp/rag-propose.json --json >/tmp/rag-created.json
    grep -Fq "\"lifecycle\": \"candidate\"" /tmp/rag-created.json
    rag_canonical_hash="$(grep -o "\"canonical_hash\": \"[0-9a-f]*\"" /tmp/rag-created.json | head -1 | grep -o "[0-9a-f]\{64\}")"

    /tmp/orgctl rag query --file - --json <<JSON >/tmp/rag-query-before-approval.json
{"actor_role_id":"ingenieria_ia/orquestador","scope":"department","query_text":"egress"}
JSON
    grep -Fq "[]" /tmp/rag-query-before-approval.json

    # rag.publish_approved carries approval:policy_or_human in
    # capability-matrix.yaml: it always evaluates as approval-required, even
    # for the owner, so review/reindex/deprecate must each go through a real
    # request -> decide -> evaluate-with-approval-request-id cycle. The
    # action digest must match exactly what internal/rag/manager.go computes.
    rag_review_digest="$(printf %s "rag-mutation.v1|cli-rag-smoke|$rag_canonical_hash|candidate|approved|1|empresa/human|CI owner reviewed evidence and admission provenance" | sha256sum | cut -d" " -f1)"
    /tmp/orgctl authorization request --actor-role empresa/human --capability rag.publish_approved --resource-type knowledge_version --resource-id cli-rag-smoke --action-digest "$rag_review_digest" --idempotency-key cli-rag-review-request --reason "CI owner approval" --json >/tmp/rag-review-request.json
    rag_review_request_id="$(grep -m1 "\"id\"" /tmp/rag-review-request.json | tr -cd "0-9")"
    /tmp/orgctl authorization decide "$rag_review_request_id" --decision approve --actor-role empresa/human --reason "CI owner approval" --json >/tmp/rag-review-decision.json
    cat >/tmp/rag-review.json <<JSON
{"version_id":"cli-rag-smoke","expected_revision":1,"actor_role_id":"empresa/human","reason":"CI owner reviewed evidence and admission provenance","outcome":"approve","approval_request_id":$rag_review_request_id}
JSON
    /tmp/orgctl rag review --file /tmp/rag-review.json --json >/tmp/rag-approved.json
    grep -Fq "\"lifecycle\": \"approved\"" /tmp/rag-approved.json

    rag_reindex_digest="$(printf %s "rag-reindex.v1|explorarte|department|ingenieria_ia" | sha256sum | cut -d" " -f1)"
    /tmp/orgctl authorization request --actor-role empresa/human --capability rag.publish_approved --resource-type knowledge_index --resource-id department:ingenieria_ia --action-digest "$rag_reindex_digest" --idempotency-key cli-rag-reindex-request --reason "CI owner approval" --json >/tmp/rag-reindex-request.json
    rag_reindex_request_id="$(grep -m1 "\"id\"" /tmp/rag-reindex-request.json | tr -cd "0-9")"
    /tmp/orgctl authorization decide "$rag_reindex_request_id" --decision approve --actor-role empresa/human --reason "CI owner approval" --json >/tmp/rag-reindex-decision.json
    cat >/tmp/rag-reindex.json <<JSON
{"namespace_kind":"department","namespace_id":"ingenieria_ia","actor_role_id":"empresa/human","approval_request_id":$rag_reindex_request_id}
JSON
    /tmp/orgctl rag reindex --file /tmp/rag-reindex.json --json >/tmp/rag-generation.json
    grep -Fq "\"status\": \"active\"" /tmp/rag-generation.json

    /tmp/orgctl rag query --file - --json <<JSON >/tmp/rag-query-after-approval.json
{"actor_role_id":"ingenieria_ia/orquestador","scope":"department","query_text":"egress"}
JSON
    grep -Fq "cli-rag-smoke-doc" /tmp/rag-query-after-approval.json

    set +e
    /tmp/orgctl rag query --file - --json <<JSON >/tmp/rag-query-denied.out 2>/tmp/rag-query-denied.err
{"actor_role_id":"creativo/copywriter","scope":"department","query_text":"egress"}
JSON
    rag_denied_code=$?
    set -e
    test "$rag_denied_code" -eq 6

    mkdir -p /tmp/context-source/ingenieria_ia/orquestador
    cat >/tmp/context-source/ingenieria_ia/orquestador/PERFIL.md <<EOF
---
departamento: ingenieria_ia
rol: orquestador
dominio_memoria: ingenieria_ia
agente_base: true
---
# Orquestador profile
Coordinate ingenieria_ia work.
EOF
    /tmp/orgctl context build --actor-role ingenieria_ia/orquestador --purpose "egress" --idempotency-key cli-rag-context-smoke --json >/tmp/rag-context-created.json
    rag_context_id="$(grep -m1 "\"id\"" /tmp/rag-context-created.json | tr -cd "0-9")"
    /tmp/orgctl context get "$rag_context_id" --json >/tmp/rag-context-get.json
    /tmp/orgctl context render "$rag_context_id" --output /tmp/rag-context-render.json
    grep -Fq "\"source_kind\":\"rag_evidence\"" /tmp/rag-context-render.json
    grep -Fq "\"trust_class\":\"untrusted\"" /tmp/rag-context-render.json
    grep -Fq "\"may_grant_capabilities\":false" /tmp/rag-context-render.json

    rag_deprecate_digest="$(printf %s "rag-mutation.v1|cli-rag-smoke|$rag_canonical_hash|approved|deprecated|2|empresa/human|superseded for CLI smoke" | sha256sum | cut -d" " -f1)"
    /tmp/orgctl authorization request --actor-role empresa/human --capability rag.publish_approved --resource-type knowledge_version --resource-id cli-rag-smoke --action-digest "$rag_deprecate_digest" --idempotency-key cli-rag-deprecate-request --reason "CI owner approval" --json >/tmp/rag-deprecate-request.json
    rag_deprecate_request_id="$(grep -m1 "\"id\"" /tmp/rag-deprecate-request.json | tr -cd "0-9")"
    /tmp/orgctl authorization decide "$rag_deprecate_request_id" --decision approve --actor-role empresa/human --reason "CI owner approval" --json >/tmp/rag-deprecate-decision.json
    cat >/tmp/rag-deprecate.json <<JSON
{"version_id":"cli-rag-smoke","expected_revision":2,"actor_role_id":"empresa/human","reason":"superseded for CLI smoke","outcome":"deprecate","approval_request_id":$rag_deprecate_request_id}
JSON
    /tmp/orgctl rag review --file /tmp/rag-deprecate.json --json >/tmp/rag-deprecated.json
    grep -Fq "\"lifecycle\": \"deprecated\"" /tmp/rag-deprecated.json

    /tmp/orgctl rag query --file - --json <<JSON >/tmp/rag-query-after-deprecation.json
{"actor_role_id":"ingenieria_ia/orquestador","scope":"department","query_text":"egress"}
JSON
    grep -Fq "[]" /tmp/rag-query-after-deprecation.json

    set +e
    /tmp/orgctl context validate "$rag_context_id" --json >/tmp/rag-context-validate.json 2>/tmp/rag-context-validate.err
    rag_validate_code=$?
    set -e
    test "$rag_validate_code" -ne 0
  '
fi
