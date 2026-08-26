#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
BASE_SHA="${MODEL_EGRESS_BASE_SHA:-$(bash "$ROOT/scripts/resolve-task-base.sh")}"
R23_TIP_SHA="${MODEL_EGRESS_R23_TIP_SHA:-f19c2b4bede1b255e05a71f9de62093eb078b68e}"
R24_TIP_SHA="${MODEL_EGRESS_R24_TIP_SHA:-c1d15c09e065996b8b6e3a184a59276409a38b17}"

fail() {
  echo "model-egress fitness: $*" >&2
  exit 1
}

command -v rg >/dev/null 2>&1 || fail "ripgrep is required"
git cat-file -e "${BASE_SHA}^{commit}" 2>/dev/null || fail "base commit ${BASE_SHA} is unavailable"
git cat-file -e "${R23_TIP_SHA}^{commit}" 2>/dev/null || fail "R23 tip ${R23_TIP_SHA} is unavailable"
git cat-file -e "${R24_TIP_SHA}^{commit}" 2>/dev/null || fail "R24 tip ${R24_TIP_SHA} is unavailable"
git merge-base --is-ancestor "$R23_TIP_SHA" "$R24_TIP_SHA" || fail "pinned R23/R24 history is not linear"
git merge-base --is-ancestor "$R24_TIP_SHA" HEAD || fail "pinned R24 tip is not an ancestor of HEAD"

test -f docs/canonical/model-egress-policy.yaml || fail "canonical model egress policy is missing"
test -f migrations/000008_create_model_egress_authorization.up.sql || fail "migration 000008 up is missing"
test -f migrations/000008_create_model_egress_authorization.down.sql || fail "migration 000008 down is missing"

# Canonical immutability is defined ONCE (delta vs the real change base).
bash "$ROOT/scripts/check-canonical-immutability.sh" "$BASE_SHA"
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
if policy_version != 4:
    raise SystemExit(f"API-only model egress policy_version must be 4, got {policy_version}")
allows={(r.get("provider_id"), r.get("data_classification")) for r in rules if r.get("effect") == "allow"}
# R30 retired gemini from this table: model-routing.yaml no longer routes
# any role to gemini for generation, and the embeddings path (R29,
# internal/embeddingruntime) never consults this policy file — see
# docs/implementation/branch-30-canary-evaluation-bge-m3/DESIGN.md. The
# three gemini allow rules were removed from docs/canonical/model-egress-
# policy.yaml accordingly; policy_version stays 4 since the surviving
# rows/reason codes are unchanged.
providers=("deepseek","openai_compatible")
classes=("public","sanitized","organizational")
expected={(provider, cls) for provider in providers for cls in classes}
if allows != expected:
    print(f"API-only productive allow table must be exactly {sorted(expected)}, got {sorted(allows)}", file=sys.stderr)
    sys.exit(1)
for provider in providers:
    for cls in classes:
        matches=[r for r in rules if r.get("provider_id")==provider and r.get("data_classification")==cls]
        if len(matches)!=1 or matches[0].get("effect")!="allow":
            raise SystemExit(f"{provider}/{cls} must be an explicit API-only productive allow")
PYPOLICY
if rg -n 'provider_id:[[:space:]]*test\.fake' docs/canonical/model-egress-policy.yaml; then fail "test.fake leaked into productive policy"; fi
for class in secret clinical; do
  rg -U -q "data_classification:[[:space:]]*${class}\n[[:space:]]+reason_code:" docs/canonical/model-egress-policy.yaml || fail "$class hard deny missing"
done
rg -q '^default_action:[[:space:]]*deny$' docs/canonical/model-egress-policy.yaml || fail "default deny missing"

# R24: policy v3 broadens the non-sensitive provider/classification table, but
# InvocationService must reject executive-only routes before materialization
# unless scope was derived from the durable Context Engine snapshot.
rg -q 'func ExecutiveScopeMarker\(' internal/modelegress/executive_scope.go || fail "executive scope derivation missing"
rg -q 'func ValidateExecutiveScope\(' internal/modelegress/executive_scope.go || fail "executive scope validator missing"
rg -q 'modelegress\.ValidateExecutiveScope\(' internal/modelruntime/invocation_service.go || fail "InvocationService does not enforce executive scope"
rg -q 'ExecutiveScopeMarker\(snapshot\.ActorRoleID, snapshot\.Purpose, snapshot\.CorrelationID, snapshot\.TaskRef\)' internal/modelruntime/bootstrap/runtime.go || fail "model context adapter does not derive scope from durable snapshot metadata"
rg -q 'ExecutiveScope:[[:space:]]*scope' internal/modelruntime/bootstrap/runtime.go || fail "derived executive scope is not carried to InvocationService"
rg -q 'strings\.TrimPrefix\(snapshot\.TaskRef, "task:"\)' internal/modelruntime/bootstrap/runtime.go || fail "canonical task:<id> reference is not normalized at modelruntime boundary"
rg -q 'strings\.HasPrefix\(correlationID, "executive:"\)' internal/modelegress/executive_scope.go || fail "scope is not bound to executive correlation"
rg -q 'strings\.HasPrefix\(taskRef, "task:"\)' internal/modelegress/executive_scope.go || fail "scope is not bound to a durable task ref"
if rg -n 'ScopeExecutiveCEO|ScopeDepartmentLeader|ScopeDepartmentWorker' docs/canonical; then
  fail "internal scope markers must not become model-controlled canonical instructions"
fi
if git diff --name-only "$R23_TIP_SHA..$R24_TIP_SHA" -- migrations | grep -q .; then
  fail "R24 changed migrations; scope must remain backend-derived metadata, not a data classification"
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

rg -q 'PersistPreSendAllowAndMarkSendStarted' internal/modelruntime/postgres/presend.go || fail "atomic allow transition is missing"
rg -q "INSERT INTO model_egress_evaluations" internal/modelruntime/postgres/presend.go || fail "durable pre-send evaluation is missing"
rg -q "SET status='send_started'" internal/modelruntime/postgres/presend.go || fail "atomic send_started transition is missing"
rg -q 'UNIQUE \(dispatch_attempt_id\)' migrations/000008_create_model_egress_authorization.up.sql || fail "one-evaluation-per-attempt constraint is missing"
rg -q 'model_egress_policy_version_id IS NULL AND model_egress_policy_hash IS NULL' migrations/000008_create_model_egress_authorization.up.sql || fail "legacy null/null policy pin is not preserved"
rg -q 'egress_policy_unpinned' internal/modelruntime/dispatch_service.go || fail "legacy unpinned dispatch guard is missing"

if rg -n 'RenderedContext|rendered_context|prompt|hidden_reasoning|provider_payload|tool_arguments|api_key|claim_token[^_h]' internal/modelegress migrations/000008_create_model_egress_authorization.up.sql; then
  fail "sensitive content field detected in model egress persistence"
fi
if rg -n '^var[[:space:]]+[A-Za-z0-9_]+[[:space:]]*=.*map\[' internal/modelegress; then
  fail "mutable package-global map detected"
fi

echo "model-egress fitness: OK"
