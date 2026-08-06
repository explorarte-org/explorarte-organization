#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
BASE_SHA="${MODEL_DISPATCH_BASE_SHA:-822010fa7426150e624beb0d10bfaf520b66ca8f}"

fail() {
  echo "model-dispatch fitness: $*" >&2
  exit 1
}

command -v rg >/dev/null 2>&1 || fail "ripgrep is required"
git cat-file -e "${BASE_SHA}^{commit}" 2>/dev/null || fail "base commit ${BASE_SHA} is unavailable"

test -f migrations/000009_create_model_dispatcher_assignments.up.sql || fail "migration 000009 up is missing"
test -f migrations/000009_create_model_dispatcher_assignments.down.sql || fail "migration 000009 down is missing"

mapfile -t canonical_changes < <({
  git diff --name-only "$BASE_SHA" -- docs/canonical
  git ls-files --others --exclude-standard -- docs/canonical
} | sort -u)
for path in "${canonical_changes[@]}"; do
  case "$path" in
    docs/canonical/capability-matrix.yaml) ;;
    *) fail "unauthorized canonical change: $path" ;;
  esac
done

git diff --exit-code "$BASE_SHA" -- \
  migrations/000001\* migrations/000002\* migrations/000003\* migrations/000004\* \
  migrations/000005\* migrations/000006\* migrations/000007\* migrations/000008\* >/dev/null \
  || fail "migration 000001-000008 changed"
git diff --exit-code "$BASE_SHA" -- docs/canonical/model-routing.yaml >/dev/null || fail "model-routing.yaml changed"
git diff --exit-code "$BASE_SHA" -- docs/canonical/model-egress-policy.yaml >/dev/null || fail "model-egress-policy.yaml changed"
git diff --exit-code "$BASE_SHA" -- docs/canonical/role-catalog.yaml >/dev/null || fail "role-catalog.yaml changed"
git diff --exit-code "$BASE_SHA" -- docs/canonical/leader-worker-map.yaml >/dev/null || fail "leader-worker-map.yaml changed"
git diff --exit-code "$BASE_SHA" -- docs/canonical/organization.yaml >/dev/null || fail "organization.yaml changed"

if rg -n '"net/http"' internal/modeldispatch; then fail "net/http is forbidden in modeldispatch"; fi
if rg -n '"os/exec"|exec\.Command|/bin/(sh|bash)|sh -c|bash -c' internal/modeldispatch internal/modelruntime internal/modelegress; then
  fail "process or shell execution is forbidden"
fi
if rg -n --glob '!**/*_test.go' 'context_snapshots|context_segments' internal/modeldispatch; then fail "modeldispatch may not access contextengine tables directly"; fi

for capability in model.execution_principal.register model.execution_principal.disable model.dispatch_assignment.create model.dispatch_assignment.revoke; do
  rg -Fq -- "- id: ${capability}" docs/canonical/capability-matrix.yaml || fail "capability missing: ${capability}"
done

# Isolate the top-level grants: and hard_denies: blocks before checking any
# per-authority sub-block, since both blocks reuse the same authority-class keys.
extract_top_level_block() {
  awk -v key="$1" '
    $0 == key ":" {inside=1; next}
    inside && $0 ~ /^[a-zA-Z_][a-zA-Z0-9_.-]*:$/ {inside=0}
    inside {print}
  ' docs/canonical/capability-matrix.yaml
}
grants_block="$(extract_top_level_block grants)"
hard_denies_block="$(extract_top_level_block hard_denies)"

extract_authority_block() {
  awk -v authority="$1" '
    $0 == "  " authority ":" {inside=1; next}
    inside && $0 ~ /^  [a-zA-Z0-9_*.-]+:$/ {inside=0}
    inside {print}
  ' <<<"$2"
}

for authority in executive department_leadership specialist execution_service transversal_audit research_execution assurance; do
  authority_grants="$(extract_authority_block "$authority" "$grants_block")"
  for capability in model.execution_principal.register model.execution_principal.disable model.dispatch_assignment.create model.dispatch_assignment.revoke; do
    if grep -qFx "  - ${capability}" <<<"$authority_grants"; then
      fail "${capability} granted to ${authority} (owner-only via wildcard)"
    fi
  done
done

execution_service_denies="$(extract_authority_block execution_service "$hard_denies_block")"
for capability in model.execution_principal.register model.execution_principal.disable model.dispatch_assignment.create model.dispatch_assignment.revoke; do
  grep -qFx "  - ${capability}" <<<"$execution_service_denies" || fail "execution_service is missing hard deny for ${capability}"
done

if rg -Fq -- '- id: role-dispatcher' docs/canonical/role-catalog.yaml; then fail "a dedicated model_dispatcher role was created"; fi
rg -U -q 'id:[[:space:]]*ingenieria_ia/code-runner(?:.|\n)*?model_policy:[[:space:]]*null' docs/canonical/role-catalog.yaml || fail "code-runner model_policy is no longer null"

if rg -n --glob '!**/*_test.go' 'DispatchActorRoleID' internal/modelruntime/domain.go | rg -q 'CreateInvocationCommand'; then
  fail "CreateInvocationCommand still exposes dispatch_actor_role_id"
fi
if rg -n --glob '!**/*_test.go' -A3 'type CreateInvocationCommand struct' internal/modelruntime/domain.go | rg -q 'dispatch_actor_role_id|execution_principal_id|dispatcher_assignment_id'; then
  fail "CreateInvocationCommand exposes a dispatcher/principal/assignment field"
fi
if rg -q -- '\.String\("claimed-by"|\.String\("principal"|\.String\("assignment"' cmd/orgctl/models.go; then
  fail "dispatch CLI still accepts a free-form dispatcher identity flag"
fi
rg -Fq 'for _, forbidden := range []string{"--claimed-by", "--principal", "--assignment"}' cmd/orgctl/models.go \
  || fail "dispatch CLI no longer rejects free-form dispatcher identity flags"
rg -q 'ORG_MODEL_EXECUTION_PRINCIPAL_KEY' internal/modelruntime/config.go || fail "dispatch identity is not sourced from ORG_MODEL_EXECUTION_PRINCIPAL_KEY"

rg -q 'dispatcher_assignment_unpinned' internal/modelruntime/dispatch_service.go || fail "legacy unpinned dispatch guard is missing"
rg -q 'model_dispatcher_assignments_one_active_idx' migrations/000009_create_model_dispatcher_assignments.up.sql || fail "one-active-assignment-per-attempt constraint is missing"
rg -q 'UNIQUE \(invocation_id\)' migrations/000009_create_model_dispatcher_assignments.up.sql || fail "one-use-per-invocation constraint is missing"
rg -q 'UNIQUE \(dispatch_attempt_id\)' migrations/000009_create_model_dispatcher_assignments.up.sql || fail "one-use-per-attempt constraint is missing"
rg -q 'used_invocations <= max_invocations' migrations/000009_create_model_dispatcher_assignments.up.sql || fail "quota ceiling constraint is missing"

# Order: assignment/principal checks and model.invoke authorization must precede
# egress evaluation, which must precede adapter check and rendering.
dispatch_file=internal/modelruntime/dispatch_service.go
assignment_line="$(rg -n -m1 'assignments\.GetByID\(' "$dispatch_file" | cut -d: -f1)"
auth_line="$(rg -n -m1 'EvaluateDispatch\(' "$dispatch_file" | cut -d: -f1)"
egress_line="$(rg -n -m1 'egressEvaluator\.Evaluate\(' "$dispatch_file" | cut -d: -f1)"
adapter_line="$(rg -n -m1 'adapters\.Get\(' "$dispatch_file" | cut -d: -f1)"
render_line="$(rg -n -m1 'RenderContextSnapshot\(' "$dispatch_file" | cut -d: -f1)"
persist_line="$(rg -n -m1 'PersistPreSendAllowAndMarkSendStarted\(' "$dispatch_file" | cut -d: -f1)"
for line in "$assignment_line" "$auth_line" "$egress_line" "$adapter_line" "$render_line" "$persist_line"; do
  [[ "$line" =~ ^[0-9]+$ ]] || fail "could not determine security-sensitive dispatch order"
done
(( assignment_line < auth_line && auth_line < egress_line && egress_line < adapter_line && adapter_line < render_line && render_line < persist_line )) \
  || fail "dispatch order must be assignment -> model.invoke -> egress -> adapter -> render -> durable allow/send_started"

rg -q 'model_dispatcher_assignment_uses' internal/modelruntime/postgres/presend.go || fail "quota consumption is not part of the shared pre-send transaction"
rg -q "UPDATE model_dispatcher_assignments" internal/modelruntime/postgres/presend.go || fail "quota increment is not part of the shared pre-send transaction"

if rg -n 'RenderedContext|rendered_context|prompt|hidden_reasoning|provider_payload|tool_arguments|api_key|token[_ ]*:|password|private[_ ]key|certificate' internal/modeldispatch migrations/000009_create_model_dispatcher_assignments.up.sql; then
  fail "sensitive content or credential field detected in model dispatch persistence"
fi

if rg -n '^var[[:space:]]+[A-Za-z0-9_]+[[:space:]]*=.*map\[' internal/modeldispatch; then
  fail "mutable package-global map detected"
fi

echo "model-dispatch fitness: OK"
