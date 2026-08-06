#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
BASE_SHA="${MODEL_PROVIDER_BASE_SHA:-c34e0f489ee84de99ba61fb89a75062752c4f065}"
fail() { echo "model-provider fitness: $*" >&2; exit 1; }
command -v rg >/dev/null 2>&1 || fail "ripgrep is required"
git cat-file -e "${BASE_SHA}^{commit}" 2>/dev/null || fail "base commit ${BASE_SHA} is unavailable"

for path in \
  internal/modelruntime/adapter/openaicompat/adapter.go \
  internal/modelruntime/adapter/openaicompat/config.go \
  internal/modelruntime/provider_adapter.go \
  internal/modelruntime/provider_request.go \
  internal/secrets/token_file.go \
  migrations/000011_create_model_provider_adapter.up.sql \
  migrations/000011_create_model_provider_adapter.down.sql; do
  test -f "$path" || fail "required file missing: $path"
done

mapfile -t canonical_changes < <({
  git diff --name-only "$BASE_SHA" -- docs/canonical
  git ls-files --others --exclude-standard -- docs/canonical
} | sort -u)
for path in "${canonical_changes[@]}"; do
  case "$path" in
    docs/canonical/model-routing.yaml|docs/canonical/model-egress-policy.yaml) ;;
    *) fail "unauthorized canonical change: $path" ;;
  esac
done
for required in docs/canonical/model-routing.yaml docs/canonical/model-egress-policy.yaml; do
  printf '%s\n' "${canonical_changes[@]}" | grep -Fxq "$required" || fail "required canonical change missing: $required"
done

git diff --exit-code "$BASE_SHA" -- migrations/000001\* migrations/000002\* migrations/000003\* \
  migrations/000004\* migrations/000005\* migrations/000006\* migrations/000007\* \
  migrations/000008\* migrations/000009\* migrations/000010\* >/dev/null \
  || fail "migration 000001-000010 changed"
git diff --exit-code "$BASE_SHA" -- cmd/orgd internal/app >/dev/null || fail "orgd or application composition changed"

if find internal/modelruntime/adapter -mindepth 1 -maxdepth 1 -type d ! -name openaicompat -print | grep -q .; then
  fail "more than one real provider adapter was introduced"
fi
if rg -n '"net/http"' internal/modelruntime --glob '*.go' --glob '!internal/modelruntime/adapter/openaicompat/**'; then
  fail "HTTP client found outside openai-compatible adapter"
fi
if rg -n '"os/exec"|exec\.Command|/bin/(sh|bash)|sh -c|bash -c' internal/modelruntime internal/secrets; then
  fail "shell or subprocess execution found"
fi
if rg -n '(deepseek|alibaba_token_plan|claude_code).*(Adapter|Dispatch|http\.Client)' internal/modelruntime/adapter --glob '*.go'; then
  fail "an unapproved real provider adapter was introduced"
fi

python3 - <<'PY'
from pathlib import Path
import re, sys
allowed={
 "ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_ENABLED",
 "ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_ENDPOINT_URL",
 "ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_CREDENTIAL_FILE",
 "ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_REQUEST_TIMEOUT",
 "ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_CIRCUIT_FAILURE_THRESHOLD",
 "ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_CIRCUIT_OPEN_DURATION",
}
seen=set()
for root in (Path("internal/modelruntime"), Path(".env.example")):
    files=root.rglob("*.go") if root.is_dir() else [root]
    for path in files:
        if path.name.endswith("_test.go"):
            continue
        seen.update(re.findall(r"ORG_MODEL_PROVIDER_[A-Z0-9_]+", path.read_text(encoding="utf-8")))
if seen != allowed:
    print("provider env contract mismatch", "missing", sorted(allowed-seen), "extra", sorted(seen-allowed), file=sys.stderr)
    sys.exit(1)
for path in Path("internal/modelruntime/adapter/openaicompat").rglob("*.go"):
    if path.name.endswith("_test.go"):
        continue
    text=path.read_text(encoding="utf-8").lower()
    for forbidden in ("openai_api_key","api_key_env","access_token_env","base_url_env"):
        if forbidden in text:
            raise SystemExit(f"raw credential/env selector found in {path}: {forbidden}")
PY

config=internal/modelruntime/adapter/openaicompat/config.go
adapter=internal/modelruntime/adapter/openaicompat/adapter.go
rg -q 'endpoint\.Scheme != "https"' "$config" || fail "HTTPS endpoint enforcement missing"
rg -q 'endpoint\.User != nil' "$config" || fail "endpoint userinfo rejection missing"
rg -q 'Proxy:[[:space:]]+nil' "$config" || fail "environment proxy bypass is missing"
rg -q 'MinVersion:[[:space:]]+tls\.VersionTLS12' "$config" || fail "TLS minimum is missing"
rg -q 'redirects are forbidden' "$config" "$adapter" || fail "redirect rejection is missing"
rg -q 'LoadBearerToken' "$adapter" || fail "external credential file loading is missing"
rg -q 'secrets\.Zero\(token\)' "$adapter" || fail "credential zeroing is missing"
rg -q 'X-Client-Request-Id' "$adapter" || fail "provider idempotency header is missing"
rg -q 'readBounded' "$adapter" || fail "bounded response reading is missing"
rg -q 'circuitBreaker' "$adapter" internal/modelruntime/adapter/openaicompat/breaker.go || fail "circuit breaker is missing"
rg -q 'ProviderOutcomeAmbiguous' "$adapter" || fail "ambiguous transport classification is missing"
rg -q 'ProviderOutcomeRejected' "$adapter" || fail "known provider rejection classification is missing"
rg -q 'ProviderOutcomeNotSent' "$adapter" || fail "not-sent classification is missing"

migration=migrations/000011_create_model_provider_adapter.up.sql
for table in model_provider_requests model_provider_outcomes; do
  rg -q "CREATE TABLE ${table}" "$migration" || fail "${table} table missing"
done
for field in egress_evaluation_id dispatcher_assignment_use_id identity_assertion_id request_hash endpoint_fingerprint credential_ref_hash idempotency_key_hash; do
  rg -q "$field" "$migration" || fail "provider request provenance field missing: $field"
done
rg -q 'model_provider_requests_no_mutation' "$migration" || fail "provider requests are not immutable"
rg -q 'model_provider_outcomes_no_mutation' "$migration" || fail "provider outcomes are not immutable"
rg -q 'UNIQUE \(dispatch_attempt_id\)' "$migration" || fail "one provider request/outcome per dispatch attempt is not enforced"
if rg -ni --glob '!**/*_test.go' '(credential[^_].*(text|bytea)|authorization_header|request_body|response_body|rendered_context|prompt|hidden_reasoning|raw_endpoint)' "$migration" internal/modelruntime/postgres; then
  fail "sensitive provider material reached durable persistence"
fi

# The durable request barrier must be inside the same transaction as egress allow,
# assignment consumption and send_started, and before the external adapter call.
presend=internal/modelruntime/postgres/presend.go
dispatch=internal/modelruntime/dispatch_service.go
rg -q 'insertProviderRequest' "$presend" || fail "durable provider request insert is missing"
rg -q 'model_dispatcher_assignment_uses' "$presend" || fail "assignment use is not co-transactional"
rg -q 'INSERT INTO model_egress_evaluations' "$presend" || fail "egress evaluation is not co-transactional"
rg -q "SET status='send_started'" "$presend" || fail "send_started transition is missing"
preflight_line="$(rg -n -m1 'providerAdapter\.Preflight\(' "$dispatch" | cut -d: -f1)"
render_line="$(rg -n -m1 'RenderContextSnapshot\(' "$dispatch" | cut -d: -f1)"
persist_line="$(rg -n -m1 'PersistPreSendAllowAndMarkSendStarted\(' "$dispatch" | cut -d: -f1)"
call_line="$(rg -n -m1 'providerAdapter\.Dispatch\(' "$dispatch" | cut -d: -f1)"
for value in "$preflight_line" "$render_line" "$persist_line" "$call_line"; do
  [[ "$value" =~ ^[0-9]+$ ]] || fail "could not resolve provider dispatch order"
done
(( preflight_line < render_line && render_line < persist_line && persist_line < call_line )) \
  || fail "provider order must be preflight -> render -> durable request/send_started -> external call"

for method in MarkResponseReceived RejectProviderResponse FailCommittedBeforeRequest MarkAmbiguous MarkCancelled; do
  rg -q "func \(s \*Store\) ${method}" internal/modelruntime/postgres/results.go || fail "classified outcome transition missing: ${method}"
done
rg -q 'provider outcome already persisted' internal/modelruntime/postgres/results.go || fail "duplicate cancellation outcome guard is missing"

python3 - <<'PY'
from pathlib import Path
import sys
lines=Path("docs/canonical/model-egress-policy.yaml").read_text(encoding="utf-8").splitlines()
rules=[]; current=None; in_rules=False
for raw in lines:
    line=raw.strip()
    if line == "rules:": in_rules=True; continue
    if not in_rules: continue
    if line.startswith("- provider_id:"):
        if current: rules.append(current)
        current={"provider_id":line.split(":",1)[1].strip()}
    elif current and ":" in line:
        k,v=line.split(":",1); current[k.strip()]=v.strip()
if current: rules.append(current)
allows={(r.get("provider_id"),r.get("data_classification")) for r in rules if r.get("effect")=="allow"}
expected={("openai_compatible","public"),("openai_compatible","sanitized")}
if allows != expected: raise SystemExit(f"unexpected productive allow set: {allows}")
PY

for test_name in \
  TestDispatchSendsBoundedCanonicalRequestAndNormalizesResponse \
  TestDispatchClassifiesProviderRejectionWithoutLeakingMessage \
  TestTransportFailureIsAmbiguousAndCircuitOpens \
  TestLoadBearerTokenRejectsUnsafePermissions; do
  rg -q "$test_name" internal/modelruntime/adapter/openaicompat internal/secrets || fail "required provider test missing: $test_name"
done

echo "model-provider fitness: OK"
