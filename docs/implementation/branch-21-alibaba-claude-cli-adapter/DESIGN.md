# Rama 21 — Alibaba Token Plan via Claude Code CLI Adapter

## Estado

Implementación técnica en curso sobre `main` post-R20.

- Rama: `feat/21-alibaba-claude-cli-adapter`
- Base reconstruida: `8782fda55c07e04024866a7826886f2ef2a7fea5`
- Migración: `000018_make_provider_outcomes_transport_aware`
- Provider canónico: `alibaba_token_plan_via_claude_code`
- Perfil habilitable por R21: `ceo-primary`
- Modelo canónico: `qwen3.6-flash`
- Transporte: `cli_adapter`
- Direct HTTP: prohibido

R21 implementa la frontera técnica. No modifica por sí misma la política de egress para autorizar uso productivo de Alibaba.

## Objetivo

Agregar un `ProviderAdapter` real para el CEO canónico mediante Claude Code CLI sin introducir un cliente HTTP directo hacia Alibaba, sin exponer contexto/credenciales en argv, logs o PostgreSQL, y conservando la semántica durable de one-shot/ambiguous del Model Runtime.

```text
modelruntime.DispatchService
        |
        v
ProviderAdapter
        |
        v
Claude Code CLI
        |
        v
Alibaba Token Plan endpoint
        |
        v
Qwen 3.6 Flash
```

Claude Code es el único componente que implementa el protocolo externo. `internal/modelruntime/adapter/alibabaclaude` no contiene `net/http`.

## Dos gates distintos

R21 distingue explícitamente:

1. **compiled adapter availability**: el binario conoce el transporte CLI y puede registrar el adapter cuando su configuración operacional es válida;
2. **productive egress authorization**: `model-egress-policy.yaml` debe permitir explícitamente las clasificaciones correspondientes.

La rama solo resuelve el primer gate. La política canónica actual continúa negando `public`, `sanitized` y `organizational` para Alibaba. `secret` y `clinical` siguen hard-deny globales.

Esto es deliberado. La disponibilidad de código no amplía egress.

Además, la documentación vigente de Alibaba para Token Plan debe revisarse operacional/legalmente antes de usar el plan como backend automatizado de la organización. R21 no convierte esa decisión externa en una modificación silenciosa de policy.

## Perfil CEO únicamente

El mismo provider aparece actualmente en bindings candidatos para otros roles, pero R21 no los activa:

- `ceo-primary`: compilado/disponible por R21;
- `executive.observer`: permanece indisponible; D-001 sigue pendiente;
- `research.audit`: permanece indisponible en esta rama.

El provider puede figurar como adapter compilado a nivel de provider, pero solo `ceo-primary` recibe `adapter_status=available` y `dispatch_enabled=true` en las ProfileVersions materializadas por R21.

## Configuración operacional

Todas las variables son opt-in y el adapter permanece apagado por defecto:

```text
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_ENABLED=false
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_EXECUTABLE=/usr/local/bin/claude
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_EXPECTED_VERSION=
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_EXECUTABLE_SHA256=
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_SETTINGS_FILE=/run/secrets/alibaba-claude-settings.json
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_SETTINGS_SHA256=
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_TOKEN_PLAN_BASE_URL=https://token-plan.ap-southeast-1.maas.aliyuncs.com/apps/anthropic
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_WORK_DIR=/var/lib/explorarte/alibaba-claude
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_RUNTIME_PATH=/usr/local/bin:/usr/bin:/bin
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_REQUEST_TIMEOUT=2m
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_KILL_GRACE=3s
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_MAX_STDERR_BYTES=65536
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_MAX_CONCURRENCY=2
```

Cuando `Enabled=false`, no se requiere que executable/settings existan y el adapter no se registra.

## Supply-chain pinning

Antes de cada envío:

- executable por path absoluto;
- `EvalSymlinks` del executable;
- regular file, executable y bounded;
- SHA-256 exacto;
- `claude --version` exacta bajo timeout;
- settings dedicado por path absoluto;
- settings regular, bounded y private (`0600`/sin group-other en POSIX);
- SHA-256 exacto del settings;
- endpoint exacto aprobado del Token Plan.

Cualquier drift falla antes del request barrier del adapter.

## Settings y credenciales

El JSON dedicado solo admite:

```text
env.ANTHROPIC_AUTH_TOKEN
env.ANTHROPIC_BASE_URL
env.ANTHROPIC_MODEL
env.ANTHROPIC_DEFAULT_HAIKU_MODEL
env.ANTHROPIC_DEFAULT_SONNET_MODEL
env.ANTHROPIC_DEFAULT_OPUS_MODEL
env.CLAUDE_CODE_SUBAGENT_MODEL
```

Unknown settings se rechazan.

El token:

- no entra a PostgreSQL;
- no entra a argv;
- no entra a errores públicos;
- no se copia al environment del proceso padre;
- vive solo en el settings dedicado que consume Claude Code;
- los buffers usados para validar el archivo se zeroizan después de uso.

El Model Runtime persiste `SHA-256(settings_file_path)` como referencia opaca mediante el descriptor, nunca el token.

## Aislamiento de Claude Code

R21 inicialmente evaluó `--bare`, pero se descartó porque bare mode altera el mecanismo normal de autenticación de Claude Code y no es compatible con la configuración documentada de Alibaba basada en `ANTHROPIC_AUTH_TOKEN` sin introducir otro mecanismo secreto.

El contrato final usa:

```text
--safe-mode
--setting-sources ""
-p <fixed query prompt>
--output-format json
--no-session-persistence
--disable-slash-commands
--no-chrome
--strict-mcp-config
--tools ""
--disallowedTools mcp__*
--permission-mode dontAsk
--max-turns 1
--model <provider model from canonical routing>
--settings <pinned dedicated settings>
--system-prompt <fixed provider system prompt>
```

Para JSON estructurado se agrega `--json-schema` con el schema ya fijado por la invocation.

No se cargan customizations del usuario/proyecto como parte del request, y ninguna skill/tool/MCP de Claude Code se convierte en autoridad de la organización.

## Contexto exclusivamente por stdin

`CanonicalRequest.RenderedContext`:

- nunca aparece en argv;
- nunca aparece en env;
- nunca se escribe a tempfile;
- se envía por stdin;
- se rechaza sobre 10 MiB, límite operativo documentado por Claude Code.

El prompt fijo de argv no contiene contenido de usuario ni memoria/RAG.

## One-shot

Claude Code puede tener retries propios. R21 los anula:

```text
CLAUDE_CODE_MAX_RETRIES=0
MAX_STRUCTURED_OUTPUT_RETRIES=0
```

`CanonicalRequest.MaxOutputTokens` se mapea a:

```text
CLAUDE_CODE_MAX_OUTPUT_TOKENS
```

No existen retries del adapter después de `Start()`.

## Process boundary

Unix:

- `exec.Command` directo; nunca `sh -c`/shell;
- process group propio (`Setpgid`);
- stdout/stderr bounded;
- timeout efectivo = min(request deadline, adapter timeout);
- cancel/timeout -> SIGTERM al process group;
- grace bounded;
- luego SIGKILL;
- se espera `Wait()` para no dejar hijos zombis.

Enviar SIGTERM/SIGKILL no prueba que Alibaba no haya recibido el request. Por eso una falla posterior a `cmd.Start()` es conservadoramente ambigua salvo evidencia más fuerte.

## Semántica de outcome

`ProviderOutcome` ahora es transport-aware:

```text
Transport
HTTPStatus
ProcessExitCode
```

Compatibilidad:

- transport vacío histórico se interpreta como HTTP;
- HTTP no puede llevar process exit code;
- CLI no puede llevar HTTP status;
- CLI `response_received` requiere exit code 0;
- `request_not_sent` no puede llevar process exit evidence;
- `ambiguous_transport` nunca es auto-retryable.

La respuesta exitosa se emite únicamente después de:

```text
process started
Wait returned
exit code == 0
stdout bounded
JSON envelope valid
expected result/structured_output valid
```

## Claude JSON envelope

`--output-format json` devuelve el resultado junto con metadata de ejecución/sesión. R21 consume exclusivamente:

```text
result
structured_output
usage.input_tokens
usage.output_tokens
```

Metadata adicional del provider es tolerada pero no se transforma en authority ni se persiste como output organizacional.

Se exige un único top-level JSON value.

## Migración 000018

R12 creó `model_provider_outcomes` con checks HTTP-céntricos. R21 agrega:

```text
transport
process_exit_code
```

La migración:

1. agrega columnas;
2. suspende temporalmente solo el trigger de inmutabilidad para backfill;
3. deriva transport desde el `model_provider_requests` inmutable;
4. restaura inmediatamente el trigger de inmutabilidad;
5. reemplaza checks HTTP-only por checks transport-aware;
6. agrega un BEFORE INSERT trigger que deriva transport desde la provider-request durable.

Para un `response_received` del adapter CLI, el trigger persiste `process_exit_code=0`, hecho que ya quedó probado por el contrato de Adapter antes de devolver success.

El down migration falla cerrado si existe cualquier evidencia `transport='cli_adapter'`; no destruye evidencia CLI para permitir rollback de schema.

## Bootstrap

`internal/modelruntime/bootstrap.Open`:

- carga openai-compatible igual que antes;
- carga config Alibaba;
- registra `alibabaclaude.Adapter` solo si `Enabled=true`;
- no registra fake adapter productivamente;
- no selecciona provider/model desde CLI o request JSON.

Canonical routing sigue siendo autoridad de provider/model/transport.

## Egress no modificado

R21 NO modifica:

```text
docs/canonical/model-egress-policy.yaml
internal/modelegress productive allowlist
organization registry productive egress allowlist
```

Por lo tanto, aun con:

```text
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_ENABLED=true
```

un dispatch productivo con una clasificación actualmente denied debe detenerse en el egress gate antes de ejecutar Claude Code.

Esto es un invariante, no un bug de disponibilidad.

## Fitness

`scripts/check-alibaba-cli-fitness.sh` falla si detecta:

- `net/http` en el adapter;
- shell invocation;
- contexto fuera de stdin;
- ausencia de aislamiento CLI;
- tools habilitadas;
- retries internos > 0;
- falta de process-group cancellation;
- endpoint no pinneado;
- adapter no opt-in;
- falta de transport/process evidence en 000018;
- pérdida del trigger de inmutabilidad;
- un `effect: allow` Alibaba agregado silenciosamente a la policy.

`make verify` incluye `test-alibaba-cli-fitness`.

## Cierre pendiente

Antes de declarar R21 mergeable faltan:

- actualizar asserts de migration tip global 17 -> 18 donde correspondan;
- prueba PostgreSQL 17 específica de 000018;
- ajustar integration de model registry a `openai + ceo-primary Alibaba` sin activar observer/research;
- `go test`, race, `make verify`, `make verify-all`;
- documentar resultado final en `INTEGRATION.md`;
- smoke VPS del adapter/preflight con Claude Code pinneado puede hacerse con egress cerrado;
- cualquier smoke que efectivamente envíe contenido al Token Plan requiere antes una decisión explícita sobre términos de uso + una revisión/actualización canónica del egress policy.
