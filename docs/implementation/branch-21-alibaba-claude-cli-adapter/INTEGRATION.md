# Rama 21 — Alibaba Token Plan via Claude Code CLI Adapter

## Estado

`pending_validation`

El core técnico está implementado en `feat/21-alibaba-claude-cli-adapter`, pero esta rama no debe declararse mergeable hasta ejecutar el set completo de pruebas en un checkout real y PostgreSQL 17, resolver los asserts de migration tip pertenecientes a R14 mediante su worker dueño, y registrar los resultados en este documento.

## Base

- Base funcional: `main` post-R20.
- Base post-R20 usada para reconstruir R21: `8782fda55c07e04024866a7826886f2ef2a7fea5`.
- Migración R20: `000017_create_approved_knowledge_rag`.
- Migración R21: `000018_make_provider_outcomes_transport_aware`.
- Rama: `feat/21-alibaba-claude-cli-adapter`.

El SHA final debe completarse después de formato, pruebas y fixes finales.

## Objetivo

Hacer ejecutable el binding canónico del CEO:

```text
role: empresa/ceo
profile: ceo-primary
provider: alibaba_token_plan_via_claude_code
model: qwen3.6-flash
transport: cli_adapter
direct_http_forbidden: true
```

sin introducir un cliente HTTP Alibaba en el Organization Kernel y sin ampliar silenciosamente la política de egress.

El camino es:

```text
Task / Context Engine
  -> Model Runtime
  -> authorization + egress + execution identity + dispatcher assignment
  -> durable provider request barrier
  -> alibabaclaude.ProviderAdapter
  -> Claude Code CLI
  -> Alibaba Token Plan
```

R21 no permite seleccionar provider/model/transport/endpoint/credential desde la request del agente.

## Adapter

Paquete:

```text
internal/modelruntime/adapter/alibabaclaude
```

Componentes:

- `config.go`: configuración operacional fail-closed;
- `settings.go`: validación del settings dedicado y credential boundary;
- `host_config.go`: HOME aislado y onboarding mínimo de Claude Code;
- `preflight.go`: executable/version/settings/workdir pinning;
- `process_unix.go`: process group, bounded I/O y cancelación;
- `adapter.go`: `ProviderAdapter` one-shot;
- `response.go`: parser del envelope JSON de Claude Code tolerante solo a metadata externa no autoritativa.

## Configuración

R21 está apagada por defecto:

```text
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_ENABLED=false
```

Cuando se habilita, se requieren:

```text
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_EXECUTABLE
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_EXPECTED_VERSION
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_EXECUTABLE_SHA256
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_SETTINGS_FILE
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_SETTINGS_SHA256
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_TOKEN_PLAN_BASE_URL
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_WORK_DIR
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_RUNTIME_PATH
```

más los límites de timeout, kill grace, stderr y concurrencia.

El endpoint aceptado por esta versión es exactamente:

```text
https://token-plan.ap-southeast-1.maas.aliyuncs.com/apps/anthropic
```

No se permite Coding Plan, PayG ni endpoint arbitrario en esta implementación.

## HOME aislado de Claude Code

R21 establece:

```text
HOME = ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_WORK_DIR
```

El workdir debe ser un directorio real, no symlink, y no group/world-writable.

Dentro debe existir únicamente el config global mínimo requerido para arrancar Claude Code con Alibaba sin heredar la cuenta/configuración personal del host:

```json
{"hasCompletedOnboarding":true}
```

Ruta:

```text
$ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_WORK_DIR/.claude.json
```

Reglas:

- regular file;
- bounded;
- private en POSIX;
- `DisallowUnknownFields`;
- un único JSON top-level;
- `hasCompletedOnboarding` debe ser `true`.

No copiar al HOME aislado:

```text
~/.claude/
memories
plugins
skills
MCP configs
projects
sessions
personal settings
```

## Settings dedicado y secreto

El settings de R21 admite exclusivamente:

```text
ANTHROPIC_AUTH_TOKEN
ANTHROPIC_BASE_URL
ANTHROPIC_MODEL
ANTHROPIC_DEFAULT_HAIKU_MODEL
ANTHROPIC_DEFAULT_SONNET_MODEL
ANTHROPIC_DEFAULT_OPUS_MODEL
CLAUDE_CODE_SUBAGENT_MODEL
```

Unknown fields fallan cerrado.

El archivo:

- es regular, bounded y privado;
- está pinneado por SHA-256;
- no admite symlink como sustituto de identidad del secreto;
- se vuelve a validar antes de cada envío;
- sus buffers se zeroizan después de validación.

El token no se persiste en PostgreSQL, audit, outbox ni errores normalizados.

## Supply-chain pinning

El executable de Claude Code se verifica antes de cada envío mediante:

```text
absolute path
EvalSymlinks
regular executable file
size bound
SHA-256 exacto
claude --version exacta
```

Un cambio de binary o version provoca `request_not_sent` antes de iniciar el proceso de inferencia.

## Aislamiento CLI

R21 NO usa `--bare` porque ese modo altera el mecanismo de autenticación de Claude Code y no preserva el flujo documentado de Alibaba con `ANTHROPIC_AUTH_TOKEN`.

El invocation contract usa:

```text
--safe-mode
--setting-sources ""
-p <fixed provider query>
--output-format json
--no-session-persistence
--disable-slash-commands
--no-chrome
--strict-mcp-config
--tools ""
--disallowedTools mcp__*
--permission-mode dontAsk
--max-turns 1
--model <canonical provider model>
--settings <pinned settings>
--system-prompt <fixed bounded provider prompt>
```

`--json-schema` solo se agrega cuando el `CanonicalRequest` ya contiene un output schema autorizado.

No se invoca shell.

## Context boundary

`CanonicalRequest.RenderedContext` se entrega únicamente por stdin.

No entra en:

```text
argv
env
tempfiles
logs
audit
provider request ledger
```

El adapter aplica un límite de 10 MiB al stdin de Claude Code, además de los límites previos del Model Runtime.

## One-shot semantics

R21 desactiva retries internos:

```text
CLAUDE_CODE_MAX_RETRIES=0
MAX_STRUCTURED_OUTPUT_RETRIES=0
```

El límite canónico de output se mapea a:

```text
CLAUDE_CODE_MAX_OUTPUT_TOKENS
```

Una invocation no se vuelve a enviar porque Claude Code haya devuelto un error ambiguo.

## Process lifecycle

En Unix:

```text
exec.Command directo
Setpgid=true
bounded stdout
bounded stderr
timeout efectivo
SIGTERM process-group
grace period
SIGKILL process-group
Wait
```

stderr se usa solo para descarte/bounds; nunca se incorpora al error durable.

Semántica:

```text
Start() no ocurrió
  -> request_not_sent

Start() ocurrió y no existe evidencia concluyente de respuesta
  -> ambiguous_transport
  -> retryable=false

exit 0 + envelope válido
  -> response_received
```

Un exit code no cero conocido se normaliza como:

```text
process_exit_N
```

para poder derivarlo durablemente sin persistir stderr.

## Response envelope

Claude Code puede incluir metadata como session/type/subtype. R21 no la trata como authority ni falla por metadata externa adicional.

Consume únicamente:

```text
result
structured_output
usage.input_tokens
usage.output_tokens
```

Se exige un único JSON top-level y usage no negativo.

Hidden reasoning no se expone ni persiste.

## ProviderOutcome transport-aware

R21 extiende el contrato Go con:

```text
Transport
HTTPStatus
ProcessExitCode
```

Compatibilidad histórica:

- `Transport==""` se interpreta como HTTP;
- HTTP nunca lleva process exit;
- CLI nunca lleva HTTP status;
- CLI success exige exit 0;
- `request_not_sent` no puede llevar process exit;
- ambiguous puede conservar exit code cuando se conoce.

## Migración 000018

`model_provider_outcomes` de R12 era HTTP-centric.

R21 agrega:

```sql
transport TEXT
process_exit_code INTEGER
```

El up migration:

1. agrega columnas;
2. suspende temporalmente solo el trigger de inmutabilidad para el backfill;
3. deriva el transporte desde `model_provider_requests`;
4. restaura inmediatamente el trigger de inmutabilidad;
5. reemplaza checks HTTP-only por checks transport-aware;
6. instala `model_provider_outcomes_transport_derivation` antes de nuevos INSERTs.

Transport derivation:

```text
alibaba_token_plan_via_claude_code + alibaba_claude_code_print -> cli_adapter
test.fake + fake -> fake_adapter
other historical provider request -> http_adapter
```

CLI exit derivation:

```text
response_received -> 0
ambiguous response_contract_invalid/response_evidence_invalid -> 0
error_code process_exit_N -> N, 0..255
otherwise -> NULL
```

El down migration falla si ya existe cualquier outcome `cli_adapter`; no destruye evidencia CLI para posibilitar rollback de schema.

## Compiled availability

R21 hace conocido el provider Alibaba CLI por el binario, pero la habilitación de bindings se restringe a:

```text
ceo-primary
```

No habilita:

```text
executive.observer
research.audit
```

D-001 y cualquier decisión de research audit permanecen fuera de alcance.

## Bootstrap

`internal/modelruntime/bootstrap.Open` registra el adapter únicamente cuando:

```text
ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_ENABLED=true
```

y la configuración es válida.

No se registra provider fake productivamente y no hay selector libre de provider/model desde CLI de invocation.

## Egress

R21 no modifica:

```text
docs/canonical/model-egress-policy.yaml
internal/modelegress productive allowlist
registry productive egress allowlist
```

Por lo tanto el estado esperado después de R21 técnica es:

```text
adapter compiled: yes
adapter operationally registerable: yes, opt-in
CEO profile compiled availability: yes
Alibaba productive egress: NO
```

Esto impide que instalar/configurar Claude Code convierta automáticamente el Token Plan en un canal de datos de la organización.

Antes de autorizar tráfico productivo debe existir una decisión separada sobre:

1. términos/uso permitido del plan para este workload automatizado;
2. data classifications autorizadas;
3. actualización explícita y revisada de la policy canónica.

## Deployment boundary

El Dockerfile de `orgd` sigue siendo distroless y no debe incorporar Claude Code.

R21 está destinado al proceso de Model Worker de R13 en un host/container dedicado que posea:

```text
Claude Code executable
isolated HOME
settings secret
execution identity private key
network egress permitido por infraestructura
```

`orgd` sigue sin ejecutar providers.

## Fitness

Nuevo target:

```bash
make test-alibaba-cli-fitness
```

Está incluido en `make verify`.

El fitness protege:

- no `net/http` en el adapter;
- no shell;
- context solo stdin;
- safe-mode/settings-source isolation;
- tools/MCP/session/browser deshabilitados;
- no `--bare`;
- retries internos cero;
- process-group cancellation;
- endpoint y opt-in;
- CEO-only compiled profile;
- migration transport/exit evidence;
- restoration del trigger de inmutabilidad;
- no `effect: allow` Alibaba agregado silenciosamente.

## Cobertura agregada

Unit tests R21 cubren:

- transport-aware ProviderOutcome;
- compatibilidad HTTP legacy;
- invalid CLI outcome combinations;
- configuration disabled/defaults;
- endpoint/path/config bounds;
- executable SHA/version drift;
- dedicated settings allowlist;
- unsafe token/settings rejection;
- private-file permissions;
- onboarding config strictness;
- rendered context absent from argv;
- structured output;
- Claude metadata extension;
- invalid schema before process;
- known nonzero exit normalization;
- process start failure;
- stdout/stderr overflow;
- timeout post-start.

PostgreSQL integration agregada:

```text
TestR21TransportAwareProviderOutcomeMigrationPostgreSQL17
```

Valida:

- tip 18;
- columnas;
- constraints;
- trigger de derivación;
- trigger de inmutabilidad;
- down 18;
- reapply únicamente 18.

## Migration-tip compatibility

R21 actualiza a 18 las suites no-R14 que expresaban explícitamente el tip global y corrige rollback stacks donde 000018 debe bajar antes que 000011.

`internal/decisiongraph` y `internal/decisiongraphtrace` no se modifican desde R21 por ownership explícito de R14. Si sus integration tests siguen afirmando tip 17, el worker dueño de R14 debe realizar exclusivamente ese bump/rollback-order compatibility change.

## Validación pendiente

No marcar estas casillas sin evidencia real:

- [ ] `gofmt` limpio en todos los Go files tocados.
- [ ] `git diff --check` limpio.
- [ ] `go build ./...`.
- [ ] `go vet ./...`.
- [ ] `go test ./internal/modelruntime/...`.
- [ ] `go test -race -short ./internal/modelruntime/...`.
- [ ] `make test-alibaba-cli-fitness`.
- [ ] PostgreSQL 17 R21 migration integration.
- [ ] `make test-model-runtime-integration`.
- [ ] `make test-model-egress-integration`.
- [ ] `make verify`.
- [ ] `make verify-all`.
- [ ] VPS executable/settings/onboarding preflight.
- [ ] final branch SHA registrado.

Un provider request real a Alibaba NO es requisito mientras el egress canónico continúe deny y el uso automatizado del plan no esté aprobado. El smoke seguro en ese estado valida la instalación/preflight y demuestra que egress bloquea cualquier request organizacional antes de iniciar Claude Code.

## VPS runbook

Ver:

```text
docs/implementation/branch-21-alibaba-claude-cli-adapter/VPS-SMOKE.md
```

## Handoff a R22

R22 debe rebasearse sobre R21 una vez mergeada y consumir únicamente `modelruntime`.

R22 no debe:

- llamar Claude Code directamente;
- conocer el settings/token;
- seleccionar Alibaba/model desde output del CEO;
- saltar egress;
- reintentar `ambiguous_transport`;
- activar observer como efecto lateral.

La disponibilidad del adapter no reemplaza dispatcher assignment, execution identity, authorization, Context Engine ni completion verification.
