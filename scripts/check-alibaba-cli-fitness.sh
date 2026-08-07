#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PKG="$ROOT/internal/modelruntime/adapter/alibabaclaude"
ADAPTER="$PKG/adapter.go"
PROCESS="$PKG/process_unix.go"
CONFIG="$PKG/config.go"
BOOTSTRAP="$ROOT/internal/modelruntime/bootstrap/runtime.go"
MIGRATION="$ROOT/migrations/000018_make_provider_outcomes_transport_aware.up.sql"
POLICY="$ROOT/docs/canonical/model-egress-policy.yaml"

fail() { printf 'R21 Alibaba CLI fitness: %s\n' "$*" >&2; exit 1; }

[[ -d "$PKG" ]] || fail "adapter package missing"
[[ -f "$MIGRATION" ]] || fail "000018 transport-aware migration missing"

if grep -R --include='*.go' -nE '"net/http"|http\.NewRequest|http\.Client' "$PKG" >/dev/null; then
  fail "Alibaba CLI adapter must not contain direct HTTP"
fi
if grep -R --include='*.go' -nE 'exec\.Command\([^,]*("sh"|"bash"|"zsh")|CommandContext\([^,]+,[^,]*("sh"|"bash"|"zsh")' "$PKG" >/dev/null; then
  fail "Alibaba CLI adapter must not invoke a shell"
fi

grep -Fq 'Stdin: request.RenderedContext' "$ADAPTER" || fail "rendered context is not passed by stdin"
if grep -n 'request.RenderedContext' "$ADAPTER" | grep -E 'args|Env|append\(' >/dev/null; then
  fail "rendered context may leak into argv/env"
fi
for token in '--safe-mode' '--setting-sources' '--no-session-persistence' '--disable-slash-commands' '--no-chrome' '--strict-mcp-config' '--tools' '--disallowedTools'; do
  grep -Fq -- "$token" "$ADAPTER" || fail "required CLI isolation flag missing: $token"
done
grep -Fq '"--tools", ""' "$ADAPTER" || fail "built-in tools are not disabled"
grep -Fq '"CLAUDE_CODE_MAX_RETRIES=0"' "$ADAPTER" || fail "Claude internal retries must remain zero"
grep -Fq '"MAX_STRUCTURED_OUTPUT_RETRIES=0"' "$ADAPTER" || fail "structured-output retries must remain zero"
grep -Fq 'ProviderOutcomeAmbiguous' "$ADAPTER" || fail "post-start ambiguity classification missing"
grep -Fq 'Retryable: false' "$ADAPTER" || fail "ambiguous CLI outcomes must not be retryable"

grep -Fq 'Setpgid: true' "$PROCESS" || fail "process group isolation missing"
grep -Fq 'syscall.SIGTERM' "$PROCESS" || fail "SIGTERM cancellation stage missing"
grep -Fq 'syscall.SIGKILL' "$PROCESS" || fail "SIGKILL escalation missing"

grep -Fq 'SingaporeTokenPlanEndpoint' "$CONFIG" || fail "fixed Token Plan endpoint pin missing"
grep -Fq 'ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_ENABLED' "$CONFIG" || fail "explicit opt-in flag missing"
grep -Fq 'alibabaclaude.LoadConfig' "$BOOTSTRAP" || fail "Alibaba adapter not wired in bootstrap"
grep -Fq 'if alibabaConfig.Enabled' "$BOOTSTRAP" || fail "Alibaba adapter registration is not opt-in"

grep -Fq 'ADD COLUMN transport' "$MIGRATION" || fail "provider outcome transport column missing"
grep -Fq 'ADD COLUMN process_exit_code' "$MIGRATION" || fail "provider process exit evidence missing"
grep -Fq 'model_provider_outcomes_transport_derivation' "$MIGRATION" || fail "transport derivation trigger missing"
grep -Fq 'model_provider_outcomes_no_mutation' "$MIGRATION" || fail "outcome immutability is not restored after backfill"

grep -Fq 'provider_id: alibaba_token_plan_via_claude_code' "$POLICY" || fail "canonical Alibaba egress rules missing"
if awk '
  $0 ~ /provider_id: alibaba_token_plan_via_claude_code/ { inrule=1 }
  inrule && $0 ~ /effect: allow/ { exit 0 }
  inrule && $0 ~ /provider_id:/ && $0 !~ /alibaba_token_plan_via_claude_code/ { inrule=0 }
  END { exit 1 }
' "$POLICY"; then
  fail "R21 must not silently widen Alibaba egress while plan terms/policy remain unresolved"
fi

printf 'R21 Alibaba CLI fitness: ok\n'
