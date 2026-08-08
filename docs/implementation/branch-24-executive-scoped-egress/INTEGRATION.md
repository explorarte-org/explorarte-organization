# Rama 24 — Executive Scoped Egress

## Estado

- Base efectiva: `feat/23-executive-evidence-recovery-layer` en `f19c2b4` (`main` + R21/R22 + R23, ya con los dos fixes de R23 — ver más abajo por qué esto importa).
- Rama: `feat/24-executive-scoped-egress`, rebaseada sobre esa base.
- SHA final verificado: `c7972bfe5ebeee79c120707d7e12d62754ebf26c`.
- Migración propia: ninguna.
- Capability matrix: sin cambios.
- No hay PR ni merge a `main` en este estado.

R24 se había ramificado originalmente desde `a30b27b` (la punta de R23 **antes** de que se corrigieran sus dos bugs de integración — ver `docs/implementation/branch-23-executive-evidence-recovery-layer/INTEGRATION.md`, sección "Addendum"). La rebaseé sobre `f19c2b4` (R23 ya corregida) porque de lo contrario R24 heredaba en silencio: (1) la inversión de semántica `created`/`reused` en `runtimeadapter.Tasks.CreateTask`, y (2) `driveDepartments` nunca señalando `done=true`, lo que rompe el mismo camino de integración que R24 también ejercita (`internal/executive`). El rebase fue limpio, sin conflictos.

## Objetivo

R24 cierra el egress productivo de CEO/líder que R17-R23 dejaron correctamente denegado por defecto (`model-egress-policy.yaml` con `effect: deny` para todo lo que no fuera OpenAI-compatible en `public`/`sanitized`), y deja preparado — pero no habilitado end-to-end — el scope de worker.

El mecanismo central es `internal/modelegress.ValidateExecutiveScope`, evaluado en `InvocationService.Create` (`internal/modelruntime/invocation_service.go`) **antes** de materializar cualquier invocation, después de que routing ya resolvió provider/transport y Context Engine ya resolvió data classes:

```text
Context Engine snapshot (actor_role_id, purpose, correlation_id, task_ref)
  -> ExecutiveScopeMarker (derivado, nunca aceptado del modelo ni renderizado al contexto)
  -> ContextSnapshotRef.ExecutiveScope
  -> InvocationService.Create
       -> ValidateExecutiveScope(provider, transport, data_classes, scope)
       -> allow | ErrEgressDenied
```

Mapeo de scope (jerarquía R22: CEO=Alibaba/Qwen vía CLI, líder=OpenAI-compatible/GPT vía HTTP, worker=DeepSeek vía HTTP — la reasignación de modelos propuesta después de R24, hacia GPT-5.6-Luna/DeepSeek-V4-Flash/Gemini-2.5-Flash, es un cambio de bindings canónicos para una R25 futura, no algo que R24 toque):

| Provider | Transport requerido | Scope requerido | Purpose que lo deriva |
|---|---|---|---|
| `alibaba_token_plan_via_claude_code` | `cli_adapter` | `scope.executive.ceo` | `executive_ceo_plan`, `executive_ceo_closure` con `actor_role_id=empresa/ceo` |
| `openai_compatible` (solo si alguna data class es `organizational`) | `http_adapter` | `scope.executive.department_leader` | `department_plan`, `department_review` con un rol de departamento |
| `deepseek` | `http_adapter` | `scope.executive.department_worker` | `department_worker` con un rol de departamento |

El scope se deriva exclusivamente de metadata durable de Context Engine (`ExecutiveScopeMarker`) — nunca se acepta desde output del modelo, nunca se renderiza al contexto, nunca se persiste como data classification. Un modelo no puede fabricar su propio scope.

## Blocker productivo que R24 NO relaja

**DeepSeek no tiene `ProviderAdapter` compilado en este repositorio.** Solo existen `internal/modelruntime/adapter/alibabaclaude` (R21) y `internal/modelruntime/adapter/openaicompat` (R9). `model-egress-policy.yaml` declara `deepseek` con `effect: allow` en las tres data classes, y tanto `internal/modelegress.ProductiveLoadOptions` como `internal/organization/registry/validation.go`'s `productiveEgressAllowRules` lo tratan como "compilado" — pero esto es **únicamente preparación de policy/scope-gate** (`ValidateExecutiveScope` ya sabe exigir `scope.executive.department_worker` + `http_adapter` para DeepSeek), no una afirmación de que el transporte real funciona. Cualquier intento real de despachar a DeepSeek fallaría en la capa de transporte/adapter (que no existe), no en el egress gate.

**No es legítimo afirmar un smoke productivo completo Qwen → GPT-Luna → DeepSeek** mientras DeepSeek no tenga adapter real. R24 deja CEO (Alibaba/Qwen) y líder (OpenAI-compatible) correctamente cerrados y verificables; el worker (DeepSeek) queda con su gate de autorización listo y probado, pero sin transporte productivo.

## Bug encontrado y corregido: allowlists de compiled-egress desincronizadas

`internal/organization/registry/validation.go` tiene una segunda lista (`productiveEgressAllowRules`, de la era R12) que debe reflejar exactamente lo mismo que `internal/modelegress.ProductiveLoadOptions` — el propio comentario del código ya decía "keep these two allowlists in sync when a future branch adds a provider". R24 actualizó la de `modelegress` (agregó Alibaba en las tres clases, DeepSeek en las tres clases, OpenAI-compatible en `organizational`) pero nunca tocó la de `registry/validation.go`, que seguía teniendo únicamente `openai_compatible: {public, sanitized}` de R12.

Resultado: **el propio canonical registry loader rechazaba la política que R24 acababa de escribir.** `TestLoadCanonicalRoutingAndPlan`, `TestCanonicalRoutingHashMatchesOrganizationRegistryDigest` y `TestRegistryPlanBindsRevisionAndDisablesInactiveRoles` fallaban con `productive model egress policy allow rule alibaba_token_plan_via_claude_code/organizational is not compiled into this adapter release` — es decir, el propio build de R24 nunca llegó a cargar su política canónica en `internal/modelruntime`.

Fix: sincronicé `productiveEgressAllowRules` con `ProductiveLoadOptions` exactamente (Alibaba + DeepSeek + OpenAI-compatible en las tres clases), documentando explícitamente en ambos sitios que la presencia de DeepSeek es preparación de policy, no una afirmación de adapter real. También reescribí `TestCrossValidationFailures/model_egress_productive_allow`: como ahora los tres providers reales están compilados/aprobados para las tres clases, ya no existe ninguna combinación real que dispare el rechazo `productive_allow_forbidden`; el test ahora registra un provider sintético no compilado (vía una entrada extra en `ModelRouting.Policies` + una regla de egress) para seguir cubriendo ese camino de fail-closed.

## Tests

Unitarios + fitness (verde):

```bash
gofmt -l .            # limpio
go vet ./...
go test ./internal/modelegress/... ./internal/modelruntime/... ./internal/executive/...
go test -race -short ./internal/modelegress/... ./internal/modelruntime/... ./internal/executive/...
bash scripts/check-model-egress-fitness.sh
bash scripts/check-model-runtime-fitness.sh
bash scripts/check-executive-fitness.sh
go build ./...
```

También corrí `go test ./...` sobre el repo completo (no solo los paquetes tocados) para descartar daño colateral en algo que dependa de los hashes canónicos de `model-egress-policy.yaml` — verde.

PostgreSQL 17 real — **corrida como tres pasos secuenciales**, exactamente como los define `.github/workflows/r24-verify.yml` (`go test -tags=integration ./internal/modelegress/... -count=1`, luego `./internal/modelruntime/...`, luego `./internal/executive/...`, cada uno su propio proceso):

```bash
go test -count=1 -tags=integration ./internal/modelegress/...
go test -count=1 -tags=integration ./internal/modelruntime/...
go test -count=1 -tags=integration ./internal/executive/...
```

Las tres corridas en verde. **Nota importante:** correrlas combinadas en una sola invocación (`go test -tags=integration ./internal/modelegress/... ./internal/modelruntime/... ./internal/executive/...`) produce fallos espurios — Go ejecuta paquetes distintos como procesos concurrentes, y cada paquete hace su propio `TRUNCATE ... RESTART IDENTITY CASCADE` contra el mismo Postgres compartido al iniciar, pisando el estado de los otros paquetes en pleno vuelo (violaciones de FK, `SQLSTATE 40001` de serialización, `dispatch assignment scope mismatch`). No es un bug de R24; es una propiedad del harness al combinarlos. El workflow de CI ya lo hace bien (tres pasos separados); documentado acá para quien reproduzca localmente.

## Riesgos residuales

- El bug de la allowlist desincronizada implica que **ninguna corrida de CI de R24 pudo haber pasado nunca** contra el `internal/modelruntime` real, aunque el estado de GitHub Actions no llegó a confirmarlo (mismo bloqueo de facturación de cuenta que afectó a R23).
- DeepSeek sigue sin `ProviderAdapter`. Cualquier smoke real hacia DeepSeek requiere primero compilar ese adapter (fuera de alcance de R24).
- La propuesta de reasignar CEO/líder/worker a GPT-5.6-Luna/DeepSeek-V4-Flash/Gemini-2.5-Flash (compartida por el usuario durante esta sesión) no está implementada — sería una R25 de cambio de bindings canónicos + adapters nuevos (Gemini, DeepSeek), no un fix de R24.
- No confirmé el estado del check `r24/verify` en GitHub (mismo bloqueo de facturación observado en R23); toda la validación de este documento es local, contra PostgreSQL 17 real.

## Comandos exactos de reproducción

```bash
git fetch origin
git checkout feat/24-executive-scoped-egress   # ya rebaseada sobre f19c2b4

gofmt -l .
go vet ./...
go test ./internal/modelegress/... ./internal/modelruntime/... ./internal/executive/...
go test -race -short ./internal/modelegress/... ./internal/modelruntime/... ./internal/executive/...
bash scripts/check-model-egress-fitness.sh
bash scripts/check-model-runtime-fitness.sh
bash scripts/check-executive-fitness.sh

export ORG_TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5432/explorarte_test?sslmode=disable'
go test -tags=integration ./internal/modelegress/... -count=1
go test -tags=integration ./internal/modelruntime/... -count=1
go test -tags=integration ./internal/executive/... -count=1

go build ./...
```

No se abrió PR. No se hizo merge a `main`.
