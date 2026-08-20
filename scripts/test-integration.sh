#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
MODE="${1:-all}"
case "$MODE" in
  all|tasks|staging|authorization|context|memory|skillregistry|rag|model|egress|dispatch|identity|worker|decision|trace|improvement|completion|composition|shadow|messaging|executive) ;;
  *) echo "usage: $0 [all|tasks|staging|authorization|context|memory|skillregistry|rag|model|egress|dispatch|identity|worker|decision|trace|improvement|completion|composition|shadow|messaging|executive]" >&2; exit 2 ;;
esac

# R31 audit fix (parallel worker isolation): a hardcoded project name meant
# two worktrees running this script at the same time shared -- and could
# tear down -- the exact same Compose project, containers, volumes, and
# network. ORG_INTEGRATION_PROJECT_NAME lets a caller pin an explicit name
# (e.g. CI matrix jobs); the default is derived from this worktree's own
# absolute path, so distinct worktrees of the same repo always get
# distinct, stable-per-worktree project names without any manual setup,
# while re-running in the SAME worktree keeps cleaning up its own prior
# run. Verified by scripts/check-parallel-worker-isolation.sh.
PROJECT_NAME_DEFAULT="explorarte-org-integration-$(printf '%s' "$ROOT" | sha256sum | cut -c1-12)"
PROJECT_NAME="${ORG_INTEGRATION_PROJECT_NAME:-$PROJECT_NAME_DEFAULT}"

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

compose=(docker compose --project-name "$PROJECT_NAME" -f compose.yaml -f compose.integration.yaml --profile integration)

cleanup() {
  # down --volumes removes exactly the volumes Compose created for THIS
  # project (tracked by Compose's own project label) -- never a hardcoded
  # global name, so it can never reach into another worktree's project.
  "${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
}

trap cleanup EXIT INT TERM
cleanup

# The CLI smoke tests, preserved verbatim from the previous harness and
# registered as a single observable unit in scripts/integration-suites.tsv.
run_cli_smoke() {
  compose_cmd run --rm -T integration-test sh -ec '
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
    /tmp/orgctl authorization request --actor-role negocio/copywriter --capability rag.publish_approved --resource-type rag_candidate --resource-id cli-smoke --action-digest "$action_digest" --idempotency-key cli-authorization-smoke --reason "CI one-time approval" --json >/tmp/authorization-request.json
    request_id="$(grep -m1 "\"id\"" /tmp/authorization-request.json | tr -cd "0-9")"
    /tmp/orgctl authorization decide "$request_id" --decision approve --actor-role empresa/human --reason "CI owner approval" --json >/tmp/authorization-decision.json
    /tmp/orgctl authorization consume "$request_id" --actor-role negocio/copywriter --action-digest "$action_digest" --json >/tmp/authorization-consume.json
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
    /tmp/orgctl memory list --actor ingenieria_ia/orquestador --status approved --json >/tmp/memory-list.json
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
{"actor_role_id":"negocio/copywriter","scope":"department","query_text":"egress"}
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
}

# =====================================================================
# Evidence model
# =====================================================================
#
# The harness distinguishes execution failure from evidence incompleteness.
#
# A run that stops at the first failing suite and a run that observes every
# suite and finds one failure both used to exit non-zero, and were therefore
# indistinguishable to every consumer. They are not the same claim: the first
# says nothing about the suites it never reached. That gap has already cost
# us a real regression -- a CLI contract changed, the smoke test that would
# have caught it never ran because an unrelated package failed earlier, and
# the change reached main under an evidence set nobody could tell was partial.
#
# COMPLETE_GREEN therefore means all of the following, never merely "exit 0":
#   - every critical precondition passed;
#   - every applicable unit in the manifest was observed;
#   - no unit ended UNKNOWN and none was BLOCKED by a failed dependency;
#   - no unit failed.
#
# Accounting completeness and evidence completeness are tracked separately.
# Knowing what happened to all twenty units (accounting) is not the same as
# having behavioural evidence from all twenty (evidence). Four units BLOCKED
# by a failed dependency are fully accounted for and still leave us without
# evidence about them, so that run is INCOMPLETE_RUN, not
# COMPLETE_WITH_FAILURES. A unit SKIPPED_NOT_APPLICABLE because the caller
# asked for a single mode is different: nobody expected evidence from it.

SUITES_FILE="${ORG_INTEGRATION_SUITES_FILE:-$ROOT/scripts/integration-suites.tsv}"
PRECONDITIONS_FILE="${ORG_INTEGRATION_PRECONDITIONS_FILE:-$ROOT/scripts/integration-preconditions.tsv}"
MANIFEST_PATH="${ORG_INTEGRATION_MANIFEST:-$ROOT/integration-evidence.json}"
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)-$$"
STARTED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
GIT_SHA="$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo unknown)"

declare -a UNIT_IDS=()
declare -A UNIT_MODES=() UNIT_KIND=() UNIT_TIMEOUT=() UNIT_DEPS=() UNIT_CMD=()
declare -A UNIT_STATUS=() UNIT_EXIT=() UNIT_DURATION=() UNIT_NOTE=()
declare -a PRECOND_IDS=()
declare -A PRECOND_CLASS=() PRECOND_CMD=() PRECOND_STATUS=() PRECOND_MSG=()
ABORT_CLASS=""
ABORT_REASON=""

# compose_run is the single indirection every suite command goes through.
# Keeping it a function rather than an expanded string is what lets the
# fitness manifests substitute trivial commands and exercise the accounting
# logic without Docker.
compose_cmd() { docker compose --project-name "$PROJECT_NAME" -f compose.yaml -f compose.integration.yaml --profile integration "$@"; }
compose_run() { compose_cmd run --rm -T integration-test "$@"; }
export -f compose_cmd compose_run run_cli_smoke
export PROJECT_NAME

read_manifest() {
  local file="$1" kind="$2" line id a b c d e
  [[ -r "$file" ]] || { echo "harness: cannot read $kind manifest: $file" >&2; exit 70; }
  while IFS=$'\t' read -r id a b c d e; do
    [[ -z "$id" || "$id" == \#* || "$id" == "id" ]] && continue
    if [[ "$kind" == suites ]]; then
      UNIT_IDS+=("$id")
      UNIT_MODES["$id"]="$a"; UNIT_KIND["$id"]="$b"; UNIT_TIMEOUT["$id"]="$c"
      UNIT_DEPS["$id"]="$d";  UNIT_CMD["$id"]="$e"
      # Every declared unit starts UNKNOWN. A unit the runner never reaches
      # keeps that status, which is precisely how an unobserved suite becomes
      # visible instead of silently vanishing from the summary.
      UNIT_STATUS["$id"]="UNKNOWN"; UNIT_EXIT["$id"]=""; UNIT_DURATION["$id"]=0
      UNIT_NOTE["$id"]=""
    else
      PRECOND_IDS+=("$id"); PRECOND_CLASS["$id"]="$a"; PRECOND_CMD["$id"]="$b"
      PRECOND_STATUS["$id"]="UNKNOWN"; PRECOND_MSG["$id"]=""
    fi
  done < "$file"
}

applies_to_mode() {
  local modes="$1" candidate
  IFS=',' read -ra candidate <<< "$modes"
  for m in "${candidate[@]}"; do [[ "$m" == "$MODE" ]] && return 0; done
  return 1
}

# ---------------------------------------------------------------------
# Preconditions
# ---------------------------------------------------------------------

assert_compose_isolation() {
  # Two worktrees must never share a Compose project, or one can tear down
  # the other's database mid-run. The project name is derived from this
  # worktree's own path; a fixed name would be the regression this guards.
  [[ -n "$PROJECT_NAME" ]] || { echo "project name is empty"; return 1; }
  if [[ "$PROJECT_NAME" != *"$(printf '%s' "$ROOT" | sha256sum | cut -c1-12)"* ]] \
     && [[ -z "${ORG_INTEGRATION_PROJECT_NAME:-}" ]]; then
    echo "project name is not derived from this worktree and was not set explicitly"
    return 1
  fi
  return 0
}

assert_destructive_authorization() {
  # internal/testdbguard refuses destructive operations unless this sentinel
  # names the canonical disposable database. Asserting it here, before any
  # suite runs, turns a per-test guard into a run-level precondition.
  [[ "${ORG_TEST_DESTRUCTIVE_DATABASE:-}" == "explorarte_test" ]] || {
    echo "ORG_TEST_DESTRUCTIVE_DATABASE is '${ORG_TEST_DESTRUCTIVE_DATABASE:-unset}', expected explorarte_test"
    return 1
  }
  return 0
}

assert_postgres_healthy() {
  "${compose[@]}" up -d --wait postgres
}

assert_disposable_database() {
  # The database the suites will destroy must be the disposable one, checked
  # against the live server rather than against the DSN string alone.
  local actual
  actual="$("${compose[@]}" exec -T postgres psql -U "$ORG_POSTGRES_USER" -d "$ORG_POSTGRES_DATABASE" -tAc 'select current_database()' 2>/dev/null | tr -d '[:space:]')"
  [[ "$actual" == "explorarte_test" ]] || {
    echo "connected database is '${actual:-unreachable}', refusing to run destructive suites"
    return 1
  }
  return 0
}

assert_schema_bootstrap() {
  # Prove the schema can be built from scratch before any suite runs. This
  # is what lets platform/postgres be an ordinary suite again: it used to be
  # the de-facto bootstrap purely because it ran first, which made its
  # failure look like everyone else's problem.
  compose_run sh -ec 'export ORG_DATABASE_URL="$ORG_TEST_DATABASE_URL"; go run ./cmd/orgctl migrate up >/dev/null'
}

run_preconditions() {
  local id class out rc
  echo "--- preconditions ---"
  for id in "${PRECOND_IDS[@]}"; do
    class="${PRECOND_CLASS[$id]}"
    set +e
    out="$(eval "${PRECOND_CMD[$id]}" 2>&1)"
    rc=$?
    set -e
    if [[ $rc -eq 0 ]]; then
      PRECOND_STATUS["$id"]="PASS"
      printf '  %-28s %s\n' "$id" "PASS"
    else
      PRECOND_STATUS["$id"]="FAIL"
      PRECOND_MSG["$id"]="$(printf '%s' "$out" | tail -3 | tr '\n' ' ')"
      printf '  %-28s %s (%s)\n' "$id" "FAIL" "$class"
      printf '      %s\n' "${PRECOND_MSG[$id]}"
      if [[ "$class" == SAFETY ]]; then
        ABORT_CLASS="SAFETY_ABORT"
      else
        ABORT_CLASS="INFRASTRUCTURE_ABORT"
      fi
      ABORT_REASON="precondition $id failed"
      return 1
    fi
  done
  return 0
}

# ---------------------------------------------------------------------
# Suites
# ---------------------------------------------------------------------

blocked_by() {
  # Returns the first declared dependency that did not pass, or nothing.
  local deps="$1" dep
  [[ "$deps" == "-" || -z "$deps" ]] && return 0
  IFS=',' read -ra dep <<< "$deps"
  for d in "${dep[@]}"; do
    [[ "${UNIT_STATUS[$d]:-UNKNOWN}" == "PASS" ]] || { printf '%s' "$d"; return 0; }
  done
  return 0
}

run_suites() {
  local id started ended rc blocker
  echo "--- suites ---"
  for id in "${UNIT_IDS[@]}"; do
    if ! applies_to_mode "${UNIT_MODES[$id]}"; then
      UNIT_STATUS["$id"]="SKIPPED_NOT_APPLICABLE"
      UNIT_NOTE["$id"]="mode $MODE does not select this unit"
      continue
    fi
    blocker="$(blocked_by "${UNIT_DEPS[$id]}")"
    if [[ -n "$blocker" ]]; then
      UNIT_STATUS["$id"]="BLOCKED"
      UNIT_NOTE["$id"]="dependency $blocker did not pass"
      printf '  %-28s %s (dependency %s)\n' "$id" "BLOCKED" "$blocker"
      continue
    fi
    started=$(date +%s)
    set +e
    timeout --foreground --signal=TERM --kill-after=30s "${UNIT_TIMEOUT[$id]}" \
      bash -c "${UNIT_CMD[$id]}"
    rc=$?
    set -e
    ended=$(date +%s)
    UNIT_EXIT["$id"]="$rc"
    UNIT_DURATION["$id"]=$(( ended - started ))
    if [[ $rc -eq 0 ]]; then
      UNIT_STATUS["$id"]="PASS"
    else
      UNIT_STATUS["$id"]="FAIL"
    fi
    printf '  %-28s %s (exit %d, %ds)\n' "$id" "${UNIT_STATUS[$id]}" "$rc" "${UNIT_DURATION[$id]}"
  done
}

# ---------------------------------------------------------------------
# Accounting and evidence manifest
# ---------------------------------------------------------------------

emit_evidence() {
  local finished expected=0 passed=0 failed=0 blocked=0 skipped=0 unknown=0 accounted=0
  local accounting_complete evidence_complete final_status id first
  finished="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

  for id in "${UNIT_IDS[@]}"; do
    expected=$(( expected + 1 ))
    case "${UNIT_STATUS[$id]}" in
      PASS) passed=$(( passed + 1 ));;
      FAIL) failed=$(( failed + 1 ));;
      BLOCKED) blocked=$(( blocked + 1 ));;
      SKIPPED_NOT_APPLICABLE) skipped=$(( skipped + 1 ));;
      *) unknown=$(( unknown + 1 ));;
    esac
  done
  accounted=$(( passed + failed + blocked + skipped + unknown ))

  if [[ $accounted -eq $expected ]]; then accounting_complete=true; else accounting_complete=false; fi
  # SKIPPED_NOT_APPLICABLE does not break evidence completeness: nobody
  # expected evidence from a unit the requested mode excludes. UNKNOWN and
  # BLOCKED do, because those are units we expected to observe and did not.
  if [[ "$accounting_complete" == true && $unknown -eq 0 && $blocked -eq 0 ]]; then
    evidence_complete=true
  else
    evidence_complete=false
  fi

  if [[ -n "$ABORT_CLASS" ]]; then
    final_status="$ABORT_CLASS"
    evidence_complete=false
  elif [[ "$evidence_complete" != true ]]; then
    final_status="INCOMPLETE_RUN"
  elif [[ $failed -gt 0 ]]; then
    final_status="COMPLETE_WITH_FAILURES"
  else
    final_status="COMPLETE_GREEN"
  fi

  {
    printf '{\n  "run_id": "%s",\n  "started_at": "%s",\n  "finished_at": "%s",\n' "$RUN_ID" "$STARTED_AT" "$finished"
    printf '  "git_sha": "%s",\n  "mode": "%s",\n  "project_name": "%s",\n' "$GIT_SHA" "$MODE" "$PROJECT_NAME"
    printf '  "preconditions": [\n'
    first=1
    for id in "${PRECOND_IDS[@]}"; do
      [[ $first -eq 0 ]] && printf ',\n'; first=0
      printf '    {"id": "%s", "class": "%s", "status": "%s", "message": "%s"}' \
        "$id" "${PRECOND_CLASS[$id]}" "${PRECOND_STATUS[$id]}" "${PRECOND_MSG[$id]//\"/\\\"}"
    done
    printf '\n  ],\n  "suites": [\n'
    first=1
    for id in "${UNIT_IDS[@]}"; do
      [[ $first -eq 0 ]] && printf ',\n'; first=0
      printf '    {"id": "%s", "kind": "%s", "status": "%s", "exit_code": %s, "duration_seconds": %s, "note": "%s"}' \
        "$id" "${UNIT_KIND[$id]}" "${UNIT_STATUS[$id]}" "${UNIT_EXIT[$id]:-null}" "${UNIT_DURATION[$id]}" "${UNIT_NOTE[$id]}"
    done
    printf '\n  ],\n  "summary": {\n'
    printf '    "expected": %d, "accounted": %d, "passed": %d, "failed": %d, "blocked": %d, "skipped_not_applicable": %d, "unknown": %d\n' \
      "$expected" "$accounted" "$passed" "$failed" "$blocked" "$skipped" "$unknown"
    printf '  },\n  "accounting_complete": %s,\n  "evidence_complete": %s,\n' "$accounting_complete" "$evidence_complete"
    printf '  "final_status": "%s",\n  "abort_reason": "%s"\n}\n' "$final_status" "$ABORT_REASON"
  } > "$MANIFEST_PATH"

  echo
  echo "--- evidence ---"
  printf '  expected   %d\n  accounted  %d\n' "$expected" "$accounted"
  printf '  passed     %d\n  failed     %d\n  blocked    %d\n  skipped    %d\n  unknown    %d\n' \
    "$passed" "$failed" "$blocked" "$skipped" "$unknown"
  printf '  accounting_complete  %s\n  evidence_complete    %s\n' "$accounting_complete" "$evidence_complete"
  printf '  FINAL STATUS         %s\n' "$final_status"
  [[ -n "$ABORT_REASON" ]] && printf '  abort_reason         %s\n' "$ABORT_REASON"
  printf '  manifest             %s\n' "$MANIFEST_PATH"

  # Exit 0 is reserved for COMPLETE_GREEN. Every other state -- failures,
  # aborts, or an incomplete evidence set -- is non-zero, so existing
  # consumers (Makefile, ci.yml) keep working unchanged while the richer
  # semantics live in the manifest.
  [[ "$final_status" == "COMPLETE_GREEN" ]]
}

read_manifest "$PRECONDITIONS_FILE" preconditions
read_manifest "$SUITES_FILE" suites

if run_preconditions; then
  run_suites
fi
emit_evidence
