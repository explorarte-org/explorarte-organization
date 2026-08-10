#!/usr/bin/env bash
set -Eeuo pipefail

BASE_COMMIT="07cc8eac1330816ee755366f61be15991f7de4b6"
fail() { printf 'model-runtime fitness: %s\n' "$*" >&2; exit 1; }

command -v rg >/dev/null 2>&1 || fail "ripgrep is required"

test -d internal/modelruntime || fail "internal/modelruntime is missing"
test -f migrations/000007_create_model_runtime_gateway.up.sql || fail "migration 000007 is missing"
test -f migrations/000007_create_model_runtime_gateway.down.sql || fail "migration 000007 down is missing"

# Network is isolated to the single approved provider adapter. Subprocesses,
# shell execution and background model daemons remain forbidden everywhere
# outside the retired, unlinked Alibaba CLI package, which remains only for
# historical source compatibility and is governed by its retirement check.
if rg -n --glob '*.go' --glob '!internal/modelruntime/adapter/openaicompat/**' --glob '!internal/modelruntime/adapter/deepseek/**' --glob '!internal/modelruntime/adapter/gemini/**' --glob '!internal/modelruntime/adapter/openairesponses/**' '"net/http"' internal/modelruntime; then
  fail "network client found outside the approved openai-compatible, DeepSeek, Gemini, or OpenAI Responses adapters"
fi
if rg -n --glob '*.go' --glob '!internal/modelruntime/adapter/alibabaclaude/**' '("os/exec"|exec\.Command|syscall\.|/bin/(ba)?sh|sh -c)' internal/modelruntime internal/secrets; then
  fail "subprocess or shell execution found in model runtime"
fi
if rg -n --glob '*.go' '(\bmodeld\b|pollModel|polling|ReconcileInterval|ORG_MODEL_RUNTIME_RECONCILE_INTERVAL)' internal/modelruntime cmd/orgctl/models.go; then
  fail "persistent worker or reconcile interval found"
fi
if find internal/modelruntime/adapter -maxdepth 1 -type f -name '*.go' ! -name 'fake.go' ! -name 'fake_test.go' ! -name 'registry.go' -print | grep -q .; then
  fail "unexpected top-level provider adapter implementation found"
fi
if find internal/modelruntime/adapter -mindepth 1 -maxdepth 1 -type d ! -name openaicompat ! -name alibabaclaude ! -name deepseek ! -name gemini ! -name openairesponses -print | grep -q .; then
  fail "unexpected real provider adapter directory found"
fi

# orgd and the generic application process must remain unaware of adapters.
if rg -n 'internal/modelruntime|modelruntime' cmd/orgd internal/app; then
  fail "orgd or app imports model runtime"
fi

# Provider credentials remain file references. Raw API keys/tokens and generic
# caller-selectable URLs are forbidden. The retired Alibaba source package is
# excluded from production configuration scanning because bootstrap no longer
# imports it and .env.example exposes no activation variables.
if rg -n --glob '!**/*_test.go' --glob '!internal/modelruntime/adapter/alibabaclaude/**' '(API[_-]?KEY|ACCESS[_-]?TOKEN|PROVIDER[_-]?TOKEN|BASE[_-]?URL|ORG_MODEL_.*PRIVATE_KEY)' internal/modelruntime cmd/orgctl; then
  fail "raw provider credential or generic endpoint configuration found"
fi
if rg -n --glob '!**/*_test.go' '(API[_-]?KEY|ACCESS[_-]?TOKEN|PROVIDER[_-]?TOKEN|BASE[_-]?URL|ORG_MODEL_.*PRIVATE_KEY)' .env.example; then
  fail "raw provider credential or generic endpoint configuration found"
fi
python3 - <<'PYENV'
from pathlib import Path
import re, sys
allowed = {
    "ORG_MODEL_EXECUTION_PRINCIPAL_KEY",
    "ORG_MODEL_EXECUTION_IDENTITY_KEY_FILE",
    "ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_ENABLED",
    "ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_ENDPOINT_URL",
    "ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_CREDENTIAL_FILE",
    "ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_REQUEST_TIMEOUT",
    "ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_CIRCUIT_FAILURE_THRESHOLD",
    "ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_CIRCUIT_OPEN_DURATION",
    "ORG_MODEL_PROVIDER_DEEPSEEK_ENABLED",
    "ORG_MODEL_PROVIDER_DEEPSEEK_ENDPOINT_URL",
    "ORG_MODEL_PROVIDER_DEEPSEEK_CREDENTIAL_FILE",
    "ORG_MODEL_PROVIDER_DEEPSEEK_REQUEST_TIMEOUT",
    "ORG_MODEL_PROVIDER_DEEPSEEK_CIRCUIT_FAILURE_THRESHOLD",
    "ORG_MODEL_PROVIDER_DEEPSEEK_CIRCUIT_OPEN_DURATION",
    "ORG_MODEL_PROVIDER_GEMINI_ENABLED",
    "ORG_MODEL_PROVIDER_GEMINI_ENDPOINT_URL",
    "ORG_MODEL_PROVIDER_GEMINI_CREDENTIAL_FILE",
    "ORG_MODEL_PROVIDER_GEMINI_REQUEST_TIMEOUT",
    "ORG_MODEL_PROVIDER_GEMINI_CIRCUIT_FAILURE_THRESHOLD",
    "ORG_MODEL_PROVIDER_GEMINI_CIRCUIT_OPEN_DURATION",
    "ORG_MODEL_PROVIDER_OPENAI_RESPONSES_ENABLED",
    "ORG_MODEL_PROVIDER_OPENAI_RESPONSES_ENDPOINT_URL",
    "ORG_MODEL_PROVIDER_OPENAI_RESPONSES_CREDENTIAL_FILE",
    "ORG_MODEL_PROVIDER_OPENAI_RESPONSES_REQUEST_TIMEOUT",
    "ORG_MODEL_PROVIDER_OPENAI_RESPONSES_CIRCUIT_FAILURE_THRESHOLD",
    "ORG_MODEL_PROVIDER_OPENAI_RESPONSES_CIRCUIT_OPEN_DURATION",
}
seen=set()
for path in [Path("internal/modelruntime"), Path(".env.example")]:
    paths = path.rglob("*.go") if path.is_dir() else [path]
    for item in paths:
        if item.name.endswith("_test.go"):
            continue
        if "internal/modelruntime/adapter/alibabaclaude" in item.as_posix():
            continue
        seen.update(re.findall(r"ORG_MODEL_(?:EXECUTION|PROVIDER)_[A-Z0-9_]+", item.read_text(encoding="utf-8")))
extra={value for value in seen if ("KEY" in value or "TOKEN" in value or "URL" in value or value.startswith("ORG_MODEL_PROVIDER_")) and value not in allowed}
if extra:
    print("unapproved provider configuration variables:", sorted(extra), file=sys.stderr)
    sys.exit(1)
PYENV

if rg -n 'alibabaclaude|ORG_MODEL_PROVIDER_ALIBABA_CLAUDE' internal/modelruntime/bootstrap .env.example; then
  fail "retired Alibaba Token Plan adapter is still product-wired"
fi

# Context is consumed only through contextengine public interfaces.
if rg -n --glob '*.go' --glob '!integration_test.go' '(context_snapshots|context_segments)' internal/modelruntime; then
  fail "direct context-engine SQL access found"
fi

# Sensitive intermediate fields must not reach persistence or CLI layers.
if rg -n 'HiddenReasoning' internal/modelruntime/postgres internal/modelruntime/bootstrap cmd/orgctl/models.go; then
  fail "hidden reasoning reached persistence/bootstrap/CLI"
fi
if rg -n 'RenderedContext' internal/modelruntime/postgres cmd/orgctl/models.go; then
  fail "rendered context reached persistence or CLI"
fi
if rg -n '(ExecuteTool|ToolExecutor|RunTool|DispatchTool|tool execution)' internal/modelruntime --glob '*.go' --glob '!**/*_test.go'; then
  fail "tool execution implementation found"
fi

python3 - <<'PY'
from pathlib import Path
import re, sys

allowed = {
    "model.registry_validated",
    "model.registry_synced",
    "model.invocation_requested",
    "model.invocation_reused",
    "model.invocation_claimed",
    "model.invocation_dispatched",
    "model.invocation_succeeded",
    "model.invocation_failed",
    "model.invocation_cancelled",
    "model.invocation_timed_out",
    "model.invocation_ambiguous",
    "model.invocation_reconciled",
}
seen = set()
for path in Path("internal/modelruntime").rglob("*.go"):
    if path.name.endswith("_test.go"):
        continue
    text = path.read_text(encoding="utf-8")
    seen.update(re.findall(r'"(model\.(?:registry|invocation)_[a-z_]+)"', text))
extra = seen - allowed
if extra:
    print("unapproved model events:", sorted(extra), file=sys.stderr)
    sys.exit(1)

source = Path("internal/modelruntime/domain.go").read_text(encoding="utf-8")
command = source[source.index("type CreateInvocationCommand struct"):source.index("type Invocation struct")]
for forbidden in ("Messages", "Tools", "RenderedContext", "ProviderID", "ProviderModelID", "BaseURL", "Headers", "APIKey"):
    if forbidden in command:
        print(f"public invocation command contains forbidden field {forbidden}", file=sys.stderr)
        sys.exit(1)

claim = source[source.index("type ClaimedInvocation struct"):source.index("type ToolIntent struct")]
if 'ClaimToken      string          `json:"-"`' not in claim:
    print("claim token is not JSON-redacted", file=sys.stderr)
    sys.exit(1)

routing = Path("internal/modelruntime/canonical_routing.go").read_text(encoding="utf-8")
required = {
    'policy.Transport == TransportFake && policy.Provider == "test.fake"',
    'policy.Transport == TransportHTTP && policy.Provider == "openai_compatible"',
    'policy.Transport == TransportHTTP && policy.Provider == "deepseek"',
    'policy.Transport == TransportHTTP && policy.Provider == "gemini"',
    'policy.Transport == TransportHTTP && policy.Provider == "openai_responses"',
}
missing = [item for item in required if item not in routing]
if missing:
    print("compiled adapter availability is not exact:", missing, file=sys.stderr)
    sys.exit(1)
PY

# Canonical changes are restricted by Branch 09 and all previous migrations are immutable.
git cat-file -e "${BASE_COMMIT}^{commit}" 2>/dev/null || fail "required base commit is unavailable"
mapfile -t canonical_changes < <({
  git diff --name-only "$BASE_COMMIT" -- docs/canonical
  git ls-files --others --exclude-standard -- docs/canonical
} | sort -u)
for path in "${canonical_changes[@]}"; do
  case "$path" in
    docs/canonical/capability-matrix.yaml|docs/canonical/model-routing.yaml|docs/canonical/model-egress-policy.yaml|docs/canonical/model-execution-identity-policy.yaml) ;;
    # R30 resolves D-007 in docs/canonical/decisions-required.yaml:resolved
    # (see docs/adr/ADR-0006-hybrid-logic-ir-shadow.md) — a deliberate,
    # documented governance action, D-005 stays untouched.
    docs/canonical/decisions-required.yaml) ;;
    *) fail "unauthorized canonical change: $path" ;;
  esac
done
git diff --exit-code "$BASE_COMMIT" -- \
  migrations/000001_create_audit_events.up.sql \
  migrations/000001_create_audit_events.down.sql \
  migrations/000002_create_organization_registry.up.sql \
  migrations/000002_create_organization_registry.down.sql \
  migrations/000003_create_durable_task_engine.up.sql \
  migrations/000003_create_durable_task_engine.down.sql \
  migrations/000004_create_staging_promotion_engine.up.sql \
  migrations/000004_create_staging_promotion_engine.down.sql \
  migrations/000005_create_capability_policy_engine.up.sql \
  migrations/000005_create_capability_policy_engine.down.sql \
  migrations/000006_create_context_engine.up.sql \
  migrations/000006_create_context_engine.down.sql \
  migrations/000007_create_model_runtime_gateway.up.sql \
  migrations/000007_create_model_runtime_gateway.down.sql >/dev/null || fail "a previous migration changed"
git diff --exit-code "$BASE_COMMIT" -- cmd/orgd internal/app >/dev/null || fail "orgd or app changed"

printf 'model-runtime fitness: OK\n'
