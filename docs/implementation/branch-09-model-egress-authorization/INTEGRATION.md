# Rama 09 — Model invocation authorization and egress policy

## Base y alcance

- Base exacta: `07cc8eac1330816ee755366f61be15991f7de4b6`.
- Rama: `feat/09-model-egress-authorization`.
- Objetivo: autorizar el dispatch mediante `model.invoke`, materializar una política canónica de egress default-deny y persistir decisiones pre-send antes de renderizar o enviar contexto.
- Fuera de alcance: adapters reales, HTTP, shell, API keys, credenciales, provider URLs, worker persistente, polling, tool execution, task completion, memoria, skills, CEO/observer runtime y cambios de routing.

## Fuentes de verdad

| Fuente | Autoridad exclusiva |
|---|---|
| `docs/canonical/model-routing.yaml` | provider, provider model, transport, `direct_http_forbidden`, routing y disponibilidad derivada |
| `docs/canonical/model-egress-policy.yaml` | clasificaciones permitidas/prohibidas, default action, hard denies y reason codes de egress |
| `docs/canonical/capability-matrix.yaml` | definición y grants/hard denies de `model.invoke` |
| PostgreSQL | materialización operacional inmutable, bindings por revisión y evidencia de decisiones |

No existe un registry paralelo. `model-egress-policy.yaml` forma parte del hash semántico y de `organization_registry_revisions.document_hashes`.

## Capability `model.invoke`

`model.invoke` tiene riesgo `high` y no tiene approval mode. Solo autoriza a un actor de infraestructura a despachar una invocación dentro de un task attempt ya validado. No autoriza a aceptar resultados, completar tareas, ejecutar tools, modificar políticas, seleccionar provider/model/transport/policy, escribir memoria, activar skills ni omitir task assignment, attempt, lease, revision pinning o egress.

Grant productivo:

```text
execution_service -> model.invoke
```

Hard deny:

```text
owner -> model.invoke
```

El hard deny prevalece sobre el wildcard del owner. `executive`, `department_leadership`, `specialist`, `transversal_audit`, `research_execution` y `assurance` permanecen default-deny. El subject role no hereda la capability y el dispatcher no hereda la autoridad del subject.

El único rol productivo `execution_service` continúa siendo `ingenieria_ia/code-runner`, con `model_policy: null`. No se modifican `role-catalog.yaml` ni `leader-worker-map.yaml`; por tanto no existe un dispatcher productivo que reúna capability, model policy, task assignment, binding y adapter ejecutable.

## Política canónica

Schema inicial:

```yaml
schema_version: 0.1.0
document_status: branch_09_candidate
policy_id: model-egress
policy_version: 1
default_action: deny
hard_denies: []
rules: []
```

El documento real incluye hard deny para `secret` y `clinical` y reglas deny para `public`, `sanitized` y `organizational` en cada provider productivo de `model-routing.yaml`. No contiene ninguna regla allow ni `test.fake`.

El parser rechaza campos desconocidos, claves duplicadas, anchors, aliases, merge keys, tabs, flow collections, block scalars, múltiples documentos, providers/clasificaciones/efectos desconocidos, versiones no positivas, default distinto de deny, hard denies inválidos, reglas duplicadas y allow productivo. El orden de reglas no modifica el semantic hash.

## Fixtures

`test.fake` existe únicamente en fixtures aisladas. Esas fixtures pueden declarar allow explícito para `public`, `sanitized` y `organizational`, pero mantienen hard deny para `secret` y `clinical`, default deny para `unknown`, conjuntos vacíos y combinaciones no declaradas. No se modifica el catálogo productivo para habilitarlo.

## Versionado y bindings

`model_egress_policy_versions` representa contenido semántico inmutable. Las reglas son:

- misma policy ID + version + hash: idempotente;
- misma policy ID + version + hash diferente: conflicto;
- nueva version + hash diferente: nueva versión;
- nueva version + hash ya existente: versión redundante rechazada.

`model_egress_revision_bindings` vincula cada organization revision a una policy version y hash. Una policy version puede asociarse con varias revisiones si el documento de egress no cambió. Versiones, reglas, bindings y evaluaciones tienen protección contra UPDATE/DELETE.

## Migración 000008

`000008_create_model_egress_authorization` crea:

- `model_egress_policy_versions`;
- `model_egress_rules`;
- `model_egress_revision_bindings`;
- `model_egress_evaluations`.

Extiende `model_invocations` con:

```text
model_egress_policy_version_id NULL
model_egress_policy_hash NULL
```

La constraint exige `NULL/NULL` o `NOT NULL/NOT NULL`. No hay backfill ni policy retroactiva.

La down migration elimina constraints y columnas nuevas y luego las tablas child-to-parent, sin CASCADE y sin modificar tablas de Rama 08.

## Invocaciones legacy

Las filas anteriores quedan `legacy_unpinned`. No se inventa una policy legacy-allow. Un dispatch legacy:

- no renderiza;
- no llama adapter;
- termina `failed_before_send`;
- usa `error_code=egress_policy_unpinned`;
- conserva audit y el outbox terminal existente;
- no genera una evaluación asociada a una policy inexistente.

Las columnas no pasan a `NOT NULL` en esta rama.

## Pinning al crear una invocation

La creación resuelve internamente:

1. organization revision actual;
2. task, attempt, assignment y lease;
3. context snapshot y revision;
4. role model binding y profile version;
5. provider fijado por routing;
6. egress revision binding y policy version.

La invocation persiste policy version ID y hash. `CreateInvocationCommand` y la CLI no exponen policy ni provider seleccionables.

El request hash incluye organization/revision, task/attempt, dispatcher, subject, snapshot, purpose, profile/version, provider/model, capabilities, output contract, deadline y policy version/hash. La misma idempotency key con request hash distinto produce conflicto.

## Action digest

```text
SHA-256(canonical JSON {
  invocation_id,
  request_hash,
  model_egress_policy_version_id,
  model_egress_policy_hash
})
```

Cambiar cualquiera de esos valores cambia el digest. Se calcula antes de authorization.

## Contexto

Modelruntime usa exclusivamente las interfaces públicas de `contextengine`. Las clasificaciones provienen de metadata (`DataClasses`), no del contenido renderizado. Antes de autorización valida estado ready, organización, revisión, subject role, task scope y drift. Luego normaliza, ordena y deduplica clasificaciones y calcula su hash.

`secret` y `clinical` ya no son rechazadas por el helper estructural: generan una decisión durable de egress deny. Se conserva un guard final de defensa en profundidad antes del adapter.

## Orden pre-send

1. runtime enabled;
2. claim invocation;
3. deadline;
4. task y attempt;
5. active lease;
6. task assignment;
7. dispatcher y subject;
8. organization revision;
9. model binding/profile/provider materialization;
10. policy pin y revision binding;
11. metadata del snapshot y drift;
12. normalized classifications;
13. action digest;
14. authorization `model.invoke`;
15. egress evaluation;
16. adapter registrado/compatible;
17. render del contexto;
18. rendered hash;
19. transacción durable `allow + send_started`;
20. adapter.

Authorization deny/error, egress deny/error, policy unpinned y adapter unavailable tienen render call count cero y nunca llaman al adapter.

La restricción temporal se conserva:

```text
dispatch_actor_role_id == subject_role_id == task.assigned_role_id
```

Una rama futura puede introducir `dispatcher_assignments`; Rama 09 no lo hace.

## Evaluación de egress

Precedencia:

```text
hard deny > explicit deny > explicit allow > default deny
```

Inputs: provider y transport fijados por routing, policy version/hash, organization revision y conjunto normalizado de clasificaciones. Una sola clasificación denegada bloquea el conjunto completo. No existe partial egress.

Se deniegan provider desconocido, revision binding ausente, hash mismatch, conjunto vacío, `unknown` y combinaciones no declaradas.

## Atomicidad pre-send

### Allow

`PersistPreSendAllowAndMarkSendStarted` bloquea invocation/attempt, verifica claim, scope, metadata, policy pin/binding y ausencia de evaluación; inserta evaluación y audits; fija provider idempotency hash; transiciona attempt e invocation a `send_started`; y confirma todo en una única transacción. El adapter se llama solo después del commit.

### Failure pre-send

`PersistPreSendDenyAndFail` persiste la evaluación, audit correspondiente, `failed_before_send`, invocation terminal `failed` y el outbox `model.invocation_failed` en una única transacción. También registra fallos posteriores a decisiones allow —por ejemplo adapter unavailable o render failure— sin reescribir la decisión de egress.

Invariantes:

- no `send_started` sin evaluación allow durable;
- una evaluación por dispatch attempt;
- deny no deja invocation despachable;
- fallar un audit revierte evaluación y transición;
- claim token bruto nunca se persiste;
- evaluaciones no contienen contenido.

## Eventos

Audits nuevos:

```text
model.egress_registry_validated
model.egress_registry_synced
model.invocation_authorized
model.invocation_authorization_denied
model.invocation_authorization_error
model.egress_allowed
model.egress_denied
model.egress_error
```

No hay outbox para decisiones internas. Continúan los eventos terminales de Rama 08. Audit/outbox almacenan solo identificadores, hashes, efectos, reason codes, clasificaciones normalizadas y correlación; nunca contexto, prompts, mensajes, respuestas, hidden reasoning, payloads, URLs, headers, tokens o credenciales.

## CLI y sincronización

```bash
orgctl model egress validate [--json]
orgctl model egress diff [--json]
orgctl model egress status [--json]
orgctl model egress sync [--apply] [--json]
```

La CLI no acepta flags para provider, policy, version, transport, classifications, effect, URL o API key.

Orden obligatorio tras un cambio canónico:

```bash
orgctl registry validate
orgctl registry diff
orgctl registry sync --apply
orgctl model registry diff
orgctl model registry sync --apply
orgctl model egress diff
orgctl model egress sync --apply
```

Una nueva invocation requiere que organization revision, task, snapshot, model binding y egress binding coincidan. Model registry stale bloquea antes del binding; egress registry stale bloquea antes de crear la invocation.

`ORG_MODEL_RUNTIME_ENABLED=false` no impide validar/diff/status/sync de egress, pero bloquea dispatch. Con `true`, capability, egress y adapter siguen siendo obligatorios.

## Rollback

1. detener cualquier proceso que cree invocaciones nuevas;
2. confirmar que no hay migraciones posteriores a 000008;
3. ejecutar `000008_create_model_egress_authorization.down.sql`;
4. retirar el registro del migration runner según su mecanismo normal;
5. restaurar el commit previo.

No se deben borrar versiones o bindings manualmente ni usar CASCADE.

## Pruebas

La rama incorpora:

- parser y hashing canónico;
- policy productiva y fixtures;
- capability grants/hard denies;
- versionado y revision bindings;
- invocation pinning/request hash/action digest;
- orden de dispatch y call counts;
- atomicidad/rollback/claim mismatch;
- migración up/down/reapply en PostgreSQL 17;
- CLI smoke y flags prohibidos;
- race tests para modelruntime y modelegress;
- fitness de no HTTP/shell/secrets/adapters reales y allowlist canónica;
- regresión de tasks, staging, authorization, contextengine y modelruntime.

Comandos de validación:

```bash
go test ./...
go test -race -short ./internal/modelruntime/...
go test -race -short ./internal/modelegress/...
make test-model-egress-fitness
make test-model-egress-integration
make verify
make build-cross
make verify-all
```

## Límites y siguiente rama compatible

Rama 09 no habilita egress productivo ni un adapter. La siguiente rama compatible debe incorporar el primer adapter real junto con aprobación explícita de una nueva policy version que añada reglas allow mínimas, credentials fuera del repositorio, health/circuit breaking y pruebas de transporte. No debe relajar hard deny de `secret` o `clinical`, revision pinning ni atomicidad pre-send.
