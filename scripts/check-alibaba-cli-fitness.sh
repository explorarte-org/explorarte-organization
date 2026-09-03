#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PKG="$ROOT/internal/modelruntime/adapter/alibabaclaude"
ADAPTER="$PKG/adapter.go"
PROCESS="$PKG/process_unix.go"
CONFIG="$PKG/config.go"
BOOTSTRAP="$ROOT/internal/modelruntime/bootstrap/runtime.go"
AVAILABILITY="$ROOT/internal/modelruntime/compiled_availability_r21.go"
MIGRATION="$ROOT/migrations/000018_make_provider_outcomes_transport_aware.up.sql"
POLICY="$ROOT/docs/canonical/model-egress-policy.yaml"
ROUTING="$ROOT/docs/canonical/model-routing.yaml"
ENV_EXAMPLE="$ROOT/.env.example"

fail() { printf 'R21 Alibaba CLI fitness: %s\n' "$*" >&2; exit 1; }

[[ -d "$PKG" ]] || fail "adapter package missing"
[[ -f "$MIGRATION" ]] || fail "000018 transport-aware migration missing"

if grep -R --include='*.go' -nE '"net/http"|http\.NewRequest|http\.Client' "$PKG" >/dev/null; then
  fail "Alibaba CLI adapter must not contain direct HTTP"
fi
if grep -R --include='*.go' -nE 'exec\.Command\([^,]*("sh"|"bash"|"zsh")|CommandContext\([^,]+,[^,]*("sh"|"bash"|"zsh")' "$PKG" >/dev/null; then
  fail "Alibaba CLI adapter must not invoke a shell"
fi

grep -Fq 'Stdin: stdin' "$ADAPTER" || fail "encoded model context is not passed by stdin"
grep -Fq 'request.RenderedContext' "$ADAPTER" || fail "rendered context is not encoded for stdin"
if grep -nE 'Args:.*request\.RenderedContext|Env:.*request\.RenderedContext' "$ADAPTER" >/dev/null; then
  fail "rendered context may leak into argv/env"
fi
for token in '--safe-mode' '--setting-sources' '--no-session-persistence' '--disable-slash-commands' '--no-chrome' '--strict-mcp-config' '--tools' '--disallowedTools'; do
  grep -Fq -- "$token" "$ADAPTER" || fail "required CLI isolation flag missing: $token"
done
if grep -Fq -- '"--bare"' "$ADAPTER"; then
  fail "bare mode is incompatible with Alibaba ANTHROPIC_AUTH_TOKEN settings"
fi
grep -Fq '"--tools", ""' "$ADAPTER" || fail "built-in tools are not disabled"
grep -Fq '"CLAUDE_CODE_MAX_RETRIES=0"' "$ADAPTER" || fail "Claude internal retries must remain zero"
grep -Fq '"MAX_STRUCTURED_OUTPUT_RETRIES=0"' "$ADAPTER" || fail "structured-output retries must remain zero"
grep -Fq 'ProviderOutcomeAmbiguous' "$ADAPTER" || fail "post-start ambiguity classification missing"
grep -Fq 'Retryable: false' "$ADAPTER" || fail "ambiguous CLI outcomes must not be retryable"
grep -Fq 'process_exit_' "$ADAPTER" || fail "known nonzero process exits are not normalized"

grep -Fq 'Setpgid: true' "$PROCESS" || fail "process group isolation missing"
grep -Fq 'syscall.SIGTERM' "$PROCESS" || fail "SIGTERM cancellation stage missing"
grep -Fq 'syscall.SIGKILL' "$PROCESS" || fail "SIGKILL escalation missing"

grep -Fq 'SingaporeTokenPlanEndpoint' "$CONFIG" || fail "fixed Token Plan endpoint pin missing"
grep -Fq 'ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_ENABLED' "$CONFIG" || fail "historical package lost its explicit opt-in boundary"
if grep -Eq 'alibabaclaude|ORG_MODEL_PROVIDER_ALIBABA_CLAUDE' "$BOOTSTRAP" "$ENV_EXAMPLE"; then
  fail "retired Alibaba adapter remains product-wired or configurable"
fi

grep -Fq 'provider.AdapterStatus = AdapterUnavailable' "$AVAILABILITY" || fail "provider retirement barrier missing"
grep -Fq 'version.AdapterStatus = AdapterUnavailable' "$AVAILABILITY" || fail "profile retirement barrier missing"

grep -Fq 'ADD COLUMN transport' "$MIGRATION" || fail "provider outcome transport column missing"
grep -Fq 'ADD COLUMN process_exit_code' "$MIGRATION" || fail "provider process exit evidence missing"
grep -Fq 'model_provider_outcomes_transport_derivation' "$MIGRATION" || fail "transport derivation trigger missing"
grep -Fq 'process_exit_' "$MIGRATION" || fail "known CLI exit-code derivation missing"
grep -Fq 'model_provider_outcomes_no_mutation' "$MIGRATION" || fail "outcome immutability is not restored after backfill"

if grep -Fq 'provider_id: alibaba_token_plan_via_claude_code' "$POLICY"; then
  fail "retired Alibaba provider remains in productive egress policy"
fi
if grep -Eq '^[[:space:]]+provider:[[:space:]]+alibaba_token_plan_via_claude_code$' "$ROUTING"; then
  fail "retired Alibaba provider remains in canonical routing"
fi

printf 'R21 Alibaba CLI retirement fitness: ok\n'
