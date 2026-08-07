#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
BASE_SHA="${MODEL_EGRESS_BASE_SHA:-07cc8eac1330816ee755366f61be15991f7de4b6}"

fail() {
  echo "model-egress fitness: $*" >&2
  exit 1
}

command -v rg >/dev/null 2>&1 || fail "ripgrep is required"
git cat-file -e "${BASE_SHA}^{commit}" 2>/dev/null || fail "base commit ${BASE_SHA} is unavailable"

test -f docs/canonical/model-egress-policy.yaml || fail "canonical model egress policy is missing"
test -f migrations/000008_create_model_egress_authorization.up.sql || fail "migration 000008 up is missing"
test -f migrations/000008_create_model_egress_authorization.down.sql || fail "migration 000008 down is missing"

mapfile -t canonical_changes < <({
  git diff --name-only "$BASE_SHA" -- docs/canonical
  git ls-files --others --exclude-standard -- docs/canonical
} | sort -u)
for path in "${canonical_changes[@]}"; do
  case "$path" in
    docs/canonical/capability-matrix.yaml|docs/canonical/model-routing.yaml|docs/canonical/model-egress-policy.yaml|docs/canonical/model-execution-identity-policy.yaml) ;;
    *) fail "unauthorized canonical change: $path" ;;
  esac
done
for required in docs/canonical/capability-matrix.yaml docs/canonical/model-egress-policy.yaml; do
  printf '%s\n' "${canonical_changes[@]}" | grep -Fxq "$required" || fail "required canonical change missing: $required"
done

git diff --exit-code "$BASE_SHA" -- \
  migrations/000001\* migrations/000002\* migrations/000003\* migrations/000004\* \
  migrations/000005\* migrations/000006\* migrations/000007\* >/dev/null \
  || fail "migration 000001-000007 changed"
git diff --exit-code "$BASE_SHA" -- docs/canonical/role-catalog.yaml >/dev/null || fail "role-catalog.yaml changed"
git diff --exit-code "$BASE_SHA" -- docs/canonical/leader-worker-map.yaml >/dev/null || fail "leader-worker-map.yaml changed"

if rg -n '"net/http"' internal/modelegress; then fail "net/http is forbidden in modelegress"; fi
if rg -n --glob '!internal/modelruntime/adapter/alibabaclaude/**' '"os/exec"|exec\.Command|/bin/(sh|bash)|sh -c|bash -c' internal/modelegress internal/modelruntime; then fail "process or shell execution is forbidden"; fi
if rg -n --glob '!**/*_test.go' 'context_snapshots|context_segments' internal/modelegress; then fail "modelegress may not access contextengine tables directly"; fi
if rg -n --glob '!**/*_test.go' 'CapabilityID\s*:\s*"task\.execute"|CapabilityID[^\n]*task\.execute' internal/modelruntime internal/modelegress; then fail "task.execute still authorizes model dispatch"; fi
rg -q 'CapabilityID\s*:\s*"model\.invoke"' internal/modelruntime/bootstrap/runtime.go || fail "model dispatch does not request model.invoke"

rg -q '^[[:space:]]*- id: model\.invoke$' docs/canonical/capability-matrix.yaml || fail "model.invoke capability missing"
rg -U -q 'execution_service:\n(?:[[:space:]]+- [^\n]+\n)*[[:space:]]+- model\.invoke' docs/canonical/capability-matrix.yaml || fail "execution_service does not receive model.invoke"
rg -U -q 'hard_denies:\n(?:.|\n)*?[[:space:]]+owner:\n(?:[[:space:]]+- [^\n]+\n)*[[:space:]]+- model\.invoke' docs/canonical/capability-matrix.yaml || fail "owner hard deny for model.invoke missing"

for authority in executive department_leadership specialist transversal_audit research_execution assurance; do
  if awk -v authority="$authority" '
    $0 ~ "^  " authority ":$" {inside=1; next}
    inside && $0 ~ /^  [a-zA-Z0-9_*.-]+:$/ {inside=0}
    inside && $0 ~ /- model\.invoke$/ {found=1}
    END {exit found ? 0 : 1}
  ' docs/canonical/capability-matrix.yaml; then
    fail "model.invoke granted to $authority"
  fi
done

python3 - <<'PYPOLICY'
from pathlib import Path
import sys
text=Path("docs/canonical/model-egress-policy.yaml").read_text(encoding="utf-8").splitlines()
rules=[]
current={}
in_rules=False
policy_version=None
for raw in text:
    line=raw.strip()
    if line.startswith("policy_version:"):
        policy_version=int(line.split(":",1)[1].strip())
    if line == "rules:":
        in_rules=True
        continue
    if not in_rules:
        continue
    if line.startswith("- provider_id:"):
        if current:
            rules.append(current)
        current={"provider_id": line.split(":",1)[1].strip()}
    elif current and ":" in line:
        key,value=line.split(":",1)
        current[key.strip()]=value.strip()
if current:
    rules.append(current)
if policy_version != 3:
    raise SystemExit(f"R24 model egress policy_version must be 3, got {policy_version}")
allows={(r.get("provider_id"), r.get("data_classification")) for r in rules if r.get("effect") == "allow"}
providers=("alibaba_token_plan_via_claude_code","deepseek","openai_compatible")
classes=("public","sanitized","organizational")
expected={(provider, cls) for provider in providers for cls in classes}
if allows != expected:
    print(f"R24 productive allow table must be exactly {sorted(expected)}, got {sorted(allows)}", file=sys.stderr)
    sys.exit(1)
for provider in providers:
    for cls in classes:
        matches=[r for r in rules if r.get("provider_id")==provider and r.get("data_classification")==cls]
        if len(matches)!=1 or matches[0].get("effect")!="allow":
            raise SystemExit(f"{provider}/{cls} must be explicit allow behind the R24 scope gate")
PYPOLICY
if rg -n 'provider_id:[[:space:]]*test\.fake' docs/canonical/model-egress-policy.yaml; then fail "test.fake leaked into productive policy"; fi
for class in secret clinical; do
  rg -U -q "data_classification:[[:space:]]*${class}\n[[:space:]]+reason_code:" docs/canonical/model-egress-policy.yaml || fail "$class hard deny missing"
done
rg -q '^default_action:[[:space:]]*deny$' docs/canonical/model-egress-policy.yaml || fail "default deny missing"

# R24: the broadened provider/classification table is safe only while every
# executive-only route is guarded by a backend-derived scope marker.
rg -q 'func ExecutiveScopeMarker\(' internal/modelegress/executive_scope.go || fail "executive scope derivation missing"
rg -q 'executive_scope_required' internal/modelegress/evaluator.go || fail "scope-missing deny missing"
rg -q 'executive_scope_verified' internal/modelegress/evaluator.go || fail "scope verification evidence missing"
rg -q 'scopeRequired\(' internal/modelegress/evaluator.go || fail "evaluator does not enforce executive scope"
rg -q 'ExecutiveScopeMarker\(snapshot\.ActorRoleID, snapshot\.Purpose, snapshot\.CorrelationID, snapshot\.TaskRef\)' internal/modelruntime/bootstrap/runtime.go || fail "model context adapter does not derive scope from durable snapshot metadata"
rg -q 'strings\.HasPrefix\(correlationID, "executive:"\)' internal/modelegress/executive_scope.go || fail "scope is not bound to executive correlation"
rg -q 'strings\.HasPrefix\(taskRef, "task:"\)' internal/modelegress/executive_scope.go || fail "scope is not bound to a durable task ref"
if rg -n 'ScopeExecutiveCEO|ScopeDepartmentLeader|ScopeDepartmentWorker' docs/canonical; then
  fail "internal scope markers must not become model-controlled canonical instructions"
fi

if rg -n 'adapter\.NewFake\(' internal/modelruntime/bootstrap cmd/orgd; then fail "product runtime registers FakeAdapter"; fi
if rg -n '(api[_-]?key|authorization:[[:space:]]*bearer|provider[_-]?secret|base[_-]?url|endpoint[_-]?url)' internal/modelegress docs/canonical/model-egress-policy.yaml migrations/000008_create_model_egress_authorization.up.sql; then
  fail "provider secret or endpoint configuration detected"
fi
if rg -n -- '--(provider|policy|policy-id|policy-version|transport|classifications|effect|url|api-key)' cmd/orgctl/model_egress.go; then
  fail "model egress CLI exposes forbidden policy/provider selection"
fi

rg -U -q 'id:[[:space:]]*ingenieria_ia/code-runner(?:.|\n)*?model_policy:[[:space:]]*null' docs/canonical/role-catalog.yaml || fail "code-runner model_policy is no longer null"

# Dispatch order is security-sensitive: capability, egress and adapter checks must precede rendering.
dispatch_file=internal/modelruntime/dispatch_service.go
auth_line="$(rg -n -m1 'EvaluateDispatch\(' "$dispatch_file" | cut -d: -f1)"
egress_line="$(rg -n -m1 'egressEvaluator\.Evaluate\(' "$dispatch_file" | cut -d: -f1)"
adapter_line="$(rg -n -m1 'adapters\.Get\(' "$dispatch_file" | cut -d: -f1)"
render_line="$(rg -n -m1 'RenderContextSnapshot\(' "$dispatch_file" | cut -d: -f1)"
persist_line="$(rg -n -m1 'PersistPreSendAllowAndMarkSendStarted\(' "$dispatch_file" | cut -d: -f1)"
for line in "$auth_line" "$egress_line" "$adapter_line" "$render_line" "$persist_line"; do
  [[ "$line" =~ ^[0-9]+$ ]] || fail "could not determine security-sensitive dispatch order"
done
(( auth_line < egress_line && egress_line < adapter_line && adapter_line < render_line && render_line < persist_line )) \
  || fail "dispatch order must be authorization -> egress -> adapter -> render -> durable allow/send_started"

# Branch 10 moved this transaction into internal/modelruntime/postgres so it can
# also consume a model_dispatcher_assignment atomically (a single shared
# implementation, not one per module — see modeldispatch fitness for the rest).
rg -q 'PersistPreSendAllowAndMarkSendStarted' internal/modelruntime/postgres/presend.go || fail "atomic allow transition is missing"
rg -q "INSERT INTO model_egress_evaluations" internal/modelruntime/postgres/presend.go || fail "durable pre-send evaluation is missing"
rg -q "SET status='send_started'" internal/modelruntime/postgres/presend.go || fail "atomic send_started transition is missing"
rg -q 'UNIQUE \(dispatch_attempt_id\)' migrations/000008_create_model_egress_authorization.up.sql || fail "one-evaluation-per-attempt constraint is missing"
rg -q 'model_egress_policy_version_id IS NULL AND model_egress_policy_hash IS NULL' migrations/000008_create_model_egress_authorization.up.sql || fail "legacy null/null policy pin is not preserved"
rg -q 'egress_policy_unpinned' internal/modelruntime/dispatch_service.go || fail "legacy unpinned dispatch guard is missing"

# Event payloads and schema must remain metadata-only.
if rg -n 'RenderedContext|rendered_context|prompt|hidden_reasoning|provider_payload|tool_arguments|api_key|claim_token[^_h]' internal/modelegress migrations/000008_create_model_egress_authorization.up.sql; then
  fail "sensitive content field detected in model egress persistence"
fi

# Verify the parser/domain package itself stays deterministic and free of package-global mutable state.
if rg -n '^var[[:space:]]+[A-Za-z0-9_]+[[:space:]]*=.*map\[' internal/modelegress; then
  fail "mutable package-global map detected"
fi

echo "model-egress fitness: OK"
