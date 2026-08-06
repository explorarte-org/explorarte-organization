# Rama 10 — Durable dispatcher assignments

## Base y alcance

- Base exacta: `822010fa7426150e624beb0d10bfaf520b66ca8f`.
- Rama: `feat/10-model-dispatcher-assignments`.
- Objetivo: introducir una identidad durable para los procesos que ejecutan invocaciones (execution principal) y una asignación durable, acotada y auditable (dispatcher assignment) que autoriza a un `execution_service` a despachar en representación de un subject role, sin que ambos deban coincidir.
- Fuera de alcance: adapters reales, HTTP/CLI a providers, shell, worker persistente, polling, scheduler, autenticación remota, tokens/certificados, failover, múltiples assignments activas por attempt, y todo lo ya excluido en Ramas 08–09.

## Separación de identidades

- **Subject role**: identidad cognitiva. Determina model policy y context snapshot. Coincide con `task.assigned_role_id`. No necesita `model.invoke`.
- **Dispatch actor role**: rol `execution_service` existente con `model.invoke`. No aporta contexto ni model policy, no selecciona provider/modelo, solo despacha mediante una assignment válida.
- **Execution principal**: instancia concreta de un proceso ejecutor (`model_execution_principals`). No es un agente, no tiene contexto ni memoria, no contiene credenciales.
- **Dispatcher assignment**: autorización durable y acotada que vincula exactamente una organization revision, un task, un attempt, un subject role y un execution principal.

La restricción temporal de Rama 09 (`dispatch_actor_role_id == subject_role_id == task.assigned_role_id`) queda reemplazada por:

```text
task.assigned_role_id == subject_role_id
dispatch_actor_role_id == dispatcher_assignment.dispatch_actor_role_id
execution_principal_id == dispatcher_assignment.execution_principal_id
```

## Capabilities y hard denies

Nuevas capabilities, riesgo `high`, sin approval mode:

```text
model.execution_principal.register
model.execution_principal.disable
model.dispatch_assignment.create
model.dispatch_assignment.revoke
```

Ninguna se concede a `executive`, `department_leadership`, `specialist`, `execution_service`, `transversal_audit`, `research_execution` ni `assurance`. Quedan disponibles para `owner` solo por su wildcard `*`. `execution_service` recibe hard deny explícito en las cuatro: un dispatcher no puede registrarse a sí mismo, activar otro principal, crear su propia assignment ni revocar rivales. El hard deny de `model.invoke` para `owner` (Rama 09) se preserva sin cambios. No se crea ningún rol `model_dispatcher` productivo; `role-catalog.yaml` y `leader-worker-map.yaml` no se modifican. Cualquier rol existente con `authority_class=execution_service` y `model.invoke` es elegible.

## Execution principals

`model_execution_principals`: `principal_key` estable, única por organización, no secreta (ej. `oracle-01/model-runtime-01`); `principal_kind` en `{local_process, cell_process}`; `status` en `{active, disabled}`. Registrar un principal no crea assignment ni autoriza dispatch. Deshabilitar es idempotente, impide nuevos claims/assignments/consumos, no revierte historial y no puede volver a `active` en esta rama.

## Dispatcher assignments

`model_dispatcher_assignments`: vincula organization revision, task, attempt, subject role, dispatch actor role y execution principal. `max_invocations` fijo al crear; `used_invocations` solo se incrementa al pasar a `send_started` (nunca en creación, claim, deny de authorization/egress, o adapter unavailable). Al alcanzar el máximo pasa a `exhausted` y no puede reabrir. Estados: `active`, `exhausted`, `expired`, `revoked`, todos terminales salvo `active`. Como máximo una assignment `active` por `(organization_id, task_id, attempt_id)`, forzado con un índice único parcial.

`valid_until` no puede superar la expiración del lease activo del attempt ni el TTL máximo configurado. La expiración es un comando one-shot (`orgctl model assignment expire`), no un worker: una assignment puede seguir `active` en estado más allá de `valid_until` hasta que alguien la expire explícitamente, por lo que tanto la creación de invocación como el dispatch revalidan la vigencia por tiempo, no solo el `status` de la fila.

## Assignment uses

`model_dispatcher_assignment_uses` es un ledger inmutable (trigger `no_mutation`). `UNIQUE(invocation_id)` y `UNIQUE(dispatch_attempt_id)` garantizan que ninguna invocación ni intento consuma cuota dos veces. Su existencia implica evaluation allow durable e invocation/attempt en `send_started` o posterior.

## Pinning en `model_invocations`

`CreateInvocationCommand` ya no acepta `dispatch_actor_role_id` (el JSON lo rechaza vía `DisallowUnknownFields`). La creación resuelve internamente, en este orden: organization revision → task/attempt/lease → subject == assigned role → única assignment activa para el scope → revisión y vigencia de la assignment → cuota disponible → execution principal y su elegibilidad de rol → model binding desde el subject → egress policy → context snapshot → request hash → persistencia. `dispatcher_assignment_id` y `execution_principal_id` quedan fijados en `model_invocations` (ambos NULL o ambos NOT NULL); `dispatch_actor_role_id` se deriva del principal, no del comando.

`request_hash` incluye ahora el ID y hash de la assignment y el ID/clave del principal. `ActionDigest` (recalculado íntegramente en `internal/modelruntime`, ya no delega en `modelegress`) cubre invocation ID, request hash, dispatcher assignment ID, execution principal ID y el pin de egress — cualquier cambio en esos campos cambia el digest.

## Invocaciones legacy

Las invocaciones anteriores a esta rama mantienen `dispatcher_assignment_id`/`execution_principal_id` en `NULL/NULL` (expand-and-contract, sin backfill). Al intentar despacharlas, el claim las acepta (cualquier principal activo puede crear el attempt de fallo) pero el dispatcher las falla inmediatamente con `dispatcher_assignment_unpinned`, antes de validar egress, renderizar contexto o tocar el adapter.

## Identidad local, no criptográfica

`ORG_MODEL_EXECUTION_PRINCIPAL_KEY` identifica qué principal durable representa el proceso local que ejecuta `orgctl model invocation dispatch`. No es un secreto ni autenticación remota: solo tiene sentido dentro del entorno de confianza local, y el proceso debe resolver contra un principal `active` en PostgreSQL. `--claimed-by` fue eliminado; `--principal` y `--assignment` se rechazan explícitamente en el comando de dispatch.

## Claim vinculado al principal

`ClaimInvocation` recibe el execution principal resuelto. Si la invocación ya está pinneada (`execution_principal_id` no nulo) y no coincide con el principal configurado, el claim se rechaza dentro de la misma transacción antes de mutar nada: no se crea attempt, no cambia el estado de la invocación. Para invocaciones legacy no pinneadas, el claim procede (cualquier principal activo puede reclamarla) para poder producir el fallo `dispatcher_assignment_unpinned` de forma durable; el `dispatch_attempt.execution_principal_id` queda NULL en ese caso porque el FK exige que coincida exactamente con `model_invocations.execution_principal_id`.

## Orden de dispatch

1. runtime enabled → 2. resolver principal local → 3. principal activo → 4. claim vinculado al principal → 5. deadline/task/attempt/lease → 6. cargar y validar la assignment (activa, vigente, cuota, coincide con revision/subject/dispatcher) → 7. model binding y egress pin → 8. metadata de contexto y drift → 9. action digest → 10. `model.invoke` sobre el dispatch actor → 11. egress → 12. adapter disponible → 13. render y verificación de hash → 14. transacción atómica → 15. `FakeAdapter`.

## Atomicidad pre-send

La transacción que antes vivía en `internal/modelegress/postgres` se trasladó a `internal/modelruntime/postgres/presend.go`, que ahora es la única implementación PostgreSQL que escribe `model_egress_evaluations`, `model_dispatcher_assignment_uses`, `model_dispatcher_assignments`, `model_dispatch_attempts`, `model_invocations` y `audit_events` para esta transición (sin transacciones separadas ni dependencias circulares entre `modelegress` y `modeldispatch`). En una sola transacción: verifica el claim, bloquea (`FOR UPDATE`) y revalida la assignment, inserta la evaluation de egress y sus audits, inserta el `assignment_use`, incrementa `used_invocations` (y marca `exhausted` si corresponde) condicionado a `status='active'` — de modo que una revocación concurrente que ya haya ganado la carrera hace fallar esta actualización — y solo entonces marca `send_started` en el attempt y la invocation. Ningún camino de deny toca la assignment. `internal/modelegress` conserva únicamente sus operaciones de registry (`validate/diff/sync/status`); su interfaz `EvaluationStore` fue retirada por no tener ya implementación propia.

## Revocación concurrente

`RevokeAssignment` y el consumo atómico usan el mismo lock de fila (`FOR UPDATE` sobre `model_dispatcher_assignments`), de modo que solo uno gana antes de `send_started`: si la revocación gana, el consumo falla y no hay `send_started`; si el consumo gana, el `use` y `send_started` quedan durables y la revocación solo afecta usos futuros. Nunca hay `send_started` sin `use`, ni `use` después de una revocación que ya ganó.

## CLI

```text
orgctl model principal register --file <json> --actor-role <role> [--json]
orgctl model principal get <id> [--json]
orgctl model principal list [--organization-id ID] [--limit N] [--json]
orgctl model principal disable <id> --actor-role <role> --reason <code> [--json]

orgctl model assignment create --task-id N --attempt-id N --subject-role R --principal-key K --max-invocations N [--valid-until RFC3339|--ttl DURATION] --idempotency-key K --actor-role R [--json]
orgctl model assignment get <id> [--json]
orgctl model assignment list [--organization-id ID] [--limit N] [--json]
orgctl model assignment revoke <id> --actor-role <role> --reason <code> [--json]
orgctl model assignment expire [--organization-id ID] [--batch N] [--json]

orgctl model invocation dispatch <id> [--json]
```

Ningún comando acepta provider, modelo, policy, egress ni transport. `assignment create` no acepta dispatch actor role (se deriva del principal); `invocation create` no acepta assignment, principal ni dispatch actor.

## Migración 000009

Crea `model_execution_principals`, `model_dispatcher_assignments`, `model_dispatcher_assignment_uses`; añade `dispatcher_assignment_id`/`execution_principal_id` (nullable, expand-and-contract) a `model_invocations` y `execution_principal_id` (nullable) a `model_dispatch_attempts`. Migraciones 000001–000008 sin cambios byte-a-byte. La down migration elimina solo lo añadido, sin `CASCADE`, en orden child-to-parent.

## Audit y outbox

Eventos nuevos: `model.execution_principal_registered/reused/disabled`, `model.dispatch_assignment_created/reused/revoked/expired/exhausted/consumed`. Ninguno agrega outbox nuevo — el lifecycle de principals/assignments es interno; el outbox terminal existente de `model_invocations` (Ramas 08–09) no cambia. Los payloads de audit son metadata acotada (IDs, roles, cuotas, hashes, reason codes); nunca contexto, prompts, tokens de claim, credenciales o contenido organizacional/clínico.

## Pruebas

Unitarias en `internal/modeldispatch` (hashing determinista, validación de comandos, elegibilidad de rol, servicios con dependencias fake) y en `internal/modelruntime` (creación con assignment pinneada, claim principal-bound, orden de dispatch). Integración PostgreSQL 17 en `internal/modeldispatch/postgres` e `internal/modelruntime/postgres` (claim races, cuota concurrente, revocación vs. consumo, legacy sin pin, migración up/down/reapply). `go test -race` sobre `modeldispatch`, `modelruntime` y `modelegress`. Fitness: `check-model-dispatch-fitness.sh` (nuevo) y `check-model-runtime-fitness.sh`/`check-model-egress-fitness.sh` (actualizados para el nuevo orden de dispatch y la nueva ubicación de la transacción compartida).

## Límites y siguiente rama

- No existe dispatch productivo: ningún rol combina `model.invoke` + model policy + task assignment + binding + adapter ejecutable fuera de fixtures.
- `FakeAdapter` sigue siendo el único adapter ejecutable; providers reales continúan `adapter_status=unavailable`, `dispatch_enabled=false`; `orgd` no ejecuta adapters.
- Antes de un adapter real hace falta: credenciales reales (fuera de alcance de `ExecutionPrincipal`), matriz de cancelación por provider, garantías de idempotencia por provider, y decidir si `ORG_MODEL_EXECUTION_PRINCIPAL_KEY` necesita evolucionar a autenticación criptográfica para procesos remotos.
- Un worker/poller persistente, scheduler o selección automática entre principals queda para una rama posterior.
