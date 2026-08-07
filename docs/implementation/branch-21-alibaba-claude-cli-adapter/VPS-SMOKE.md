# R21 — VPS validation runbook

Este runbook valida la frontera operacional de Claude Code para R21 sin modificar `docs/canonical/model-egress-policy.yaml` y sin asumir que Alibaba Token Plan está autorizado para tráfico automatizado productivo.

## 1. Checkout y baseline

```bash
git fetch origin
git checkout feat/21-alibaba-claude-cli-adapter
git status --short
git rev-parse HEAD
```

No continuar si el árbol tiene cambios ajenos a R21.

## 2. Resolver Claude Code instalado

```bash
CLAUDE_BIN="$(command -v claude)"
test -n "$CLAUDE_BIN"
readlink -f "$CLAUDE_BIN"
"$CLAUDE_BIN" --version
sha256sum "$(readlink -f "$CLAUDE_BIN")"
```

Registrar exactamente:

- path resuelto;
- salida exacta de `--version`;
- SHA-256 del executable real.

R21 no acepta `latest`, symlink mutable como identidad suficiente ni version ranges.

## 3. Crear HOME aislado

Ejemplo:

```bash
sudo install -d -m 0700 -o "$USER" -g "$USER" /var/lib/explorarte/alibaba-claude
cat >/var/lib/explorarte/alibaba-claude/.claude.json <<'JSON'
{"hasCompletedOnboarding":true}
JSON
chmod 0600 /var/lib/explorarte/alibaba-claude/.claude.json
```

No copiar `~/.claude.json`, `~/.claude/`, proyectos, memories, skills, MCP configs ni plugins personales al workdir de R21.

El HOME de R21 debe contener únicamente el mínimo operacional explícitamente provisionado.

## 4. Crear settings dedicado

El archivo contiene secreto. No hacer commit, no imprimirlo en logs y no pegarlo en tickets.

```bash
install -d -m 0700 /run/explorarte
umask 077
cat >/run/explorarte/alibaba-claude-settings.json <<'JSON'
{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "REPLACE_WITH_TOKEN_PLAN_TOKEN",
    "ANTHROPIC_BASE_URL": "https://token-plan.ap-southeast-1.maas.aliyuncs.com/apps/anthropic",
    "ANTHROPIC_MODEL": "qwen3.6-flash",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "qwen3.6-flash",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "qwen3.6-flash",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "qwen3.6-flash",
    "CLAUDE_CODE_SUBAGENT_MODEL": "qwen3.6-flash"
  }
}
JSON
chmod 0600 /run/explorarte/alibaba-claude-settings.json
sha256sum /run/explorarte/alibaba-claude-settings.json
```

Después de obtener el hash, evitar cualquier comando que imprima el contenido del settings.

## 5. Exportar configuración R21

Reemplazar valores exactos observados en pasos anteriores:

```bash
export ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_ENABLED=true
export ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_EXECUTABLE="$(readlink -f "$CLAUDE_BIN")"
export ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_EXPECTED_VERSION='EXACT_CLAUDE_VERSION_OUTPUT'
export ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_EXECUTABLE_SHA256='EXACT_EXECUTABLE_SHA256'
export ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_SETTINGS_FILE=/run/explorarte/alibaba-claude-settings.json
export ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_SETTINGS_SHA256='EXACT_SETTINGS_SHA256'
export ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_TOKEN_PLAN_BASE_URL=https://token-plan.ap-southeast-1.maas.aliyuncs.com/apps/anthropic
export ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_WORK_DIR=/var/lib/explorarte/alibaba-claude
export ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_RUNTIME_PATH=/usr/local/bin:/usr/bin:/bin
export ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_REQUEST_TIMEOUT=2m
export ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_KILL_GRACE=3s
export ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_MAX_STDERR_BYTES=65536
export ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_MAX_CONCURRENCY=1
```

No exportar `ANTHROPIC_AUTH_TOKEN` en el shell del servicio. El token vive únicamente dentro del settings dedicado consumido por Claude Code.

## 6. Validación local de R21

```bash
gofmt -w internal/modelruntime/provider_adapter.go \
  internal/modelruntime/provider_adapter_cli_test.go \
  internal/modelruntime/compiled_availability_r21.go \
  internal/modelruntime/compiled_availability_r21_test.go \
  internal/modelruntime/bootstrap/runtime.go \
  internal/modelruntime/adapter/alibabaclaude/*.go \
  internal/modelruntime/postgres/r21_transport_integration_test.go

git diff --check

go test ./internal/modelruntime/adapter/alibabaclaude/...
go test ./internal/modelruntime/...
go test -race -short ./internal/modelruntime/adapter/alibabaclaude/...
go test -race -short ./internal/modelruntime/...
make test-alibaba-cli-fitness
```

Cualquier cambio producido por `gofmt` debe revisarse y commitearse en R21 antes del handoff.

## 7. PostgreSQL 17 / migración 000018

Con `ORG_TEST_DATABASE_URL` apuntando a una base de integración desechable:

```bash
go test -tags=integration -run TestR21TransportAwareProviderOutcomeMigrationPostgreSQL17 ./internal/modelruntime/postgres
make test-model-runtime-integration
make test-model-egress-integration
```

Verificar explícitamente:

```sql
SELECT column_name, data_type
FROM information_schema.columns
WHERE table_name='model_provider_outcomes'
  AND column_name IN ('transport','process_exit_code')
ORDER BY column_name;
```

Y:

```sql
SELECT tgname
FROM pg_trigger
WHERE tgrelid='model_provider_outcomes'::regclass
  AND NOT tgisinternal
ORDER BY tgname;
```

Deben existir tanto el trigger de inmutabilidad como el de derivación de transporte.

## 8. Registry materializado

Después de sincronizar la revisión canónica:

```bash
orgctl model registry diff --json
orgctl model registry sync --apply --json
orgctl model registry status --json
```

Comprobar en PostgreSQL:

```sql
SELECT profile_id, provider_id, provider_model_id, transport, adapter_status, dispatch_enabled
FROM model_profile_versions
WHERE provider_id='alibaba_token_plan_via_claude_code'
ORDER BY profile_id, version_number;
```

R21 espera:

```text
ceo-primary            available / dispatch_enabled=true
executive.observer     unavailable / false
research.audit         unavailable / false
```

No activar observer ni research audit como efecto lateral del adapter CEO.

## 9. Egress permanece cerrado

Antes de cualquier smoke con contenido organizacional:

```bash
orgctl model egress status --json
```

La policy actual debe continuar denegando Alibaba. La instalación correcta del adapter NO es una razón para modificar esa policy automáticamente.

Con egress cerrado, un intento productivo debe detenerse antes de ejecutar Claude Code. Ese resultado es esperado.

## 10. Test directo de Claude Code: solo con aprobación explícita

No ejecutar este paso como parte automática de `verify`.

Solo después de confirmar que el uso automatizado del Token Plan está permitido para este workload y de aprobar explícitamente el egress correspondiente, puede hacerse un request real y mínimo.

El primer payload debe ser sintético/public, nunca clinical, secret ni memoria organizacional real.

Una vez habilitado por decisión explícita, verificar:

- una sola invocación externa;
- `transport=cli_adapter`;
- `http_status IS NULL`;
- `process_exit_code=0` en `response_received`;
- no hay rendered context, token, settings body ni stderr en PostgreSQL/audit/outbox;
- timeout/cancel post-start produce `ambiguous_transport` y nunca retry automático.

## 11. Full verification

```bash
make verify
make verify-all
```

R21 no se declara mergeable hasta que ambas terminen verdes, con la única excepción de un assert de migration tip perteneciente a R14: ese archivo debe ser actualizado por el worker dueño de R14, no desde R21.

## 12. Evidencia a devolver

Guardar únicamente metadata no secreta:

```text
branch head SHA
Claude executable path
Claude version
Claude executable SHA-256
settings SHA-256 (no content)
000018 integration result
adapter unit/race result
fitness result
make verify result
make verify-all result
registry profile availability counts
confirmation that Alibaba egress remains deny
```

Nunca incluir el Token Plan token ni el contenido del settings en el reporte.
