#!/usr/bin/env bash
set -Eeuo pipefail

BASE_COMMIT="07cc8eac1330816ee755366f61be15991f7de4b6"
fail() { printf 'model-runtime fitness: %s\n' "$*" >&2; exit 1; }

command -v rg >/dev/null 2>&1 || fail "ripgrep is required"

test -d internal/modelruntime || fail "internal/modelruntime is missing"
test -f migrations/000007_create_model_runtime_gateway.up.sql || fail "migration 000007 is missing"
test -f migrations/000007_create_model_runtime_gateway.down.sql || fail "migration 000007 down is missing"

# The branch is a one-shot local control plane. Network clients, subprocesses,
# shell execution and background model daemons are forbidden.
if rg -n --glob '*.go' '("net/http"|"os/exec"|exec\.Command|syscall\.|/bin/(ba)?sh|sh -c)' internal/modelruntime; then
  fail "network or subprocess execution found in model runtime"
fi
if rg -n --glob '*.go' '(\bmodeld\b|pollModel|polling|ReconcileInterval|ORG_MODEL_RUNTIME_RECONCILE_INTERVAL)' internal/modelruntime cmd/orgctl/models.go; then
  fail "persistent worker or reconcile interval found"
fi
if find internal/modelruntime/adapter -maxdepth 1 -type f -name '*.go' ! -name 'fake.go' ! -name 'fake_test.go' ! -name 'registry.go' -print | grep -q .; then
  fail "unexpected provider adapter implementation found"
fi

# orgd and the generic application process must remain unaware of adapters.
if rg -n 'internal/modelruntime|modelruntime' cmd/orgd internal/app; then
  fail "orgd or app imports model runtime"
fi

# Real provider secrets and endpoints are intentionally outside Branch 08.
# The branch 10 principal key is a non-secret local identity label. The branch
# 11 identity key-file variable is only a filesystem reference; raw private-key
# values remain forbidden by the model-identity fitness checks.
if rg -n --glob '!**/*_test.go' '(API[_-]?KEY|ACCESS[_-]?TOKEN|PROVIDER[_-]?TOKEN|BASE[_-]?URL|Authorization: Bearer|ORG_MODEL_.*(KEY|TOKEN|URL))' internal/modelruntime cmd/orgctl/models.go .env.example \
  | rg -v 'ORG_MODEL_EXECUTION_(PRINCIPAL_KEY|IDENTITY_KEY_FILE)' | rg -q .; then
  fail "provider credential or endpoint configuration found"
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
required = 'policy.Transport == TransportFake && policy.Provider == "test.fake"'
if required not in routing:
    print("fake availability is not restricted to test.fake", file=sys.stderr)
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
    docs/canonical/capability-matrix.yaml|docs/canonical/model-egress-policy.yaml|docs/canonical/model-execution-identity-policy.yaml) ;;
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
