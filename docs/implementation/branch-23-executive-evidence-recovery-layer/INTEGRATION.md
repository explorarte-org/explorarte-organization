# Rama 23 — Executive Evidence & Recovery Layer

## Estado

R23 endurece el flujo ejecutivo integrado de R21/R22 sin crear un segundo task engine, model runtime, context engine ni persistencia ejecutiva paralela.

- Base: `3090966772da048a6245200dcef4bcfe42c9d22c` (`main` con R21 + R22 integradas y los fixes de integración).
- Rama: `feat/23-executive-evidence-recovery-layer`.
- Migración propia: **none**.
- Capability matrix: **sin cambios**.
- Decision Graph/R14: **sin cambios**.
- `ceo_observer`: continúa desactivado.
- `daily_cycle`: continúa desactivado.
- Provider adapters: R23 no importa ni ejecuta adapters concretos.

## Problemas que corrige

### 1. Review departamental ciego

R22 creaba el review task con un resumen de workers que solo indicaba `verified result recorded`. El líder no recibía el `WorkerResult` validado, sus evidence refs ni un verdict actual del completion verifier.

R23 agrega `runtimeadapter.EvidenceTasks`. Antes de que `CreateTask` de un review retorne al orquestador, el decorator:

1. encuentra el `DepartmentPlan` durable del departamento;
2. recupera su `review_criteria` y `ResponseHash` desde `InvocationResult`;
3. recupera cada `WorkerResult` únicamente desde la invocation durable ya completada;
4. vuelve a ejecutar `CompletionGate.Verify` para cada worker que R22 declaró completed;
5. incorpora evidence refs del output y de `task_evidence`;
6. materializa un bundle JSON determinista y bounded (máximo 14 KiB);
7. registra ese bundle como evidence **informativa** (`Satisfies=false`, sin RequirementID) del review task.

El bundle no se concatena manualmente al prompt. `internal/tasks/contextprovider` lo entrega mediante Context Engine como parte de `task_context`, manteniendo:

- `TrustUntrusted`;
- `MayGrantCapabilities=false`;
- `DataOrganizational`;
- precedencia canónica sin cambios.

El provider solo expone `Metadata` cuando la reference empieza por `executive-evidence:`. Metadata arbitraria de otras evidencias continúa fuera del contexto para evitar ampliar accidentalmente superficie de egress.

### 2. CEO closure ciego

R22 entregaba al CEO principalmente `department + review_status + task_id`.

R23 proyecta al closure task solamente el **review completed más reciente por departamento**, incluyendo:

- `DepartmentReview.verdict`;
- findings;
- unsatisfied criteria;
- evidence refs;
- task evidence refs;
- completion verdict re-verificado;
- response hash;
- lista de tasks actualmente blocked.

Reviews obsoletos previos a un replan no se mezclan con el review final.

### 3. Ventana de carrera al materializar el DAG

R22 creaba workers y después agregaba worker-to-worker dependencies mediante `AddDependency`. Una task inicialmente sin dependencies podía quedar `ready` durante esa ventana.

R23 agrega `runtimeadapter.DAGTasks`.

Todo executive worker recibe desde el mismo `CreateTask` una dependencia real al planning/review task indicado por su `CausationID=task:<source-id>`.

Esto garantiza:

```text
source plan/review = running
        ↓
worker CreateTask(dependency = source)
        ↓
worker status = pending
        ↓
R22 agrega worker→worker edges
        ↓
source puede completar
        ↓
Task Engine recién entonces puede promover worker
```

No se usa sleep, available-at artificial ni estado en memoria para cerrar la carrera.

### 4. Budget no enforceado prospectivamente

`InvocationBudget.Validate` existía, pero R22 no lo colocaba delante de `EnsureInvocation`.

R23 agrega `runtimeadapter.BudgetModels` delante del puerto de Model Runtime.

Antes de crear una invocation nueva:

1. lista tasks por `CorrelationID`;
2. recorre attempts durables;
3. cuenta invocations ya materializadas;
4. clasifica CEO / leader / worker por task/role;
5. cuenta replans durables;
6. suma **prospectivamente** la invocation solicitada;
7. ejecuta `InvocationBudget.Validate`.

Una invocation ya existente para el mismo task attempt puede seguir siendo reutilizada aunque el presupuesto haya llegado al límite; el gate bloquea únicamente una creación nueva.

Normal path continúa siendo:

```text
2 + 2D + A
```

### 5. Crash después de inference y antes de materializar task result

R22 mantenía el lease token solo en memoria — correctamente, porque no debe persistirse — pero tras expirar el lease podía abrir un nuevo attempt aunque el attempt viejo ya tuviera una invocation `succeeded` durable.

R23 modifica `ResumeDurable`:

- primero deja que Task Engine reconcilie leases;
- nunca reconstruye ni persiste el lease token;
- mientras un lease del proceso anterior siga activo, espera;
- cuando el lease ya no es adoptable, inspecciona invocations del attempt viejo;
- si existe exactamente una invocation `succeeded` y su `InvocationResult` es durable, la root queda:

```text
blocked
reason_code = orphaned_model_result
```

No se crea una segunda inference silenciosa.

Este caso requiere reconciliación explícita porque el Task Engine no ofrece un puerto seguro para finalizar un attempt antiguo sin su lease token. R23 prefiere bloquear antes que inventar autoridad, persistir secrets o romper el contrato del lease.

## Persistencia

No se agrega tabla `executive_runs` ni migración R23.

El estado necesario sigue derivándose de:

- tasks;
- task dependencies;
- attempts;
- leases;
- task evidence;
- model invocations;
- invocation results;
- correlation/causation IDs;
- completion verification.

Los executive evidence bundles se almacenan usando `task_evidence.Metadata`, infraestructura existente.

## Seguridad

R23 no cambia:

- routing canónico;
- provider selection;
- execution identity;
- dispatcher assignment authority;
- authorization approvals;
- RAG/memory admission;
- hidden reasoning policy.

Los bundles contienen solo resultados ya validados y referencias durables. No contienen chain-of-thought, raw HTTP/CLI responses, credentials ni ToolIntents ejecutables.

## Tests R23

Unitarios nuevos cubren:

- worker nace con dependencia al source task;
- tercera llamada CEO prospectiva es rechazada antes de crear invocation;
- projection usa el WorkerResult real, completion pass y task/model evidence refs;
- orphaned succeeded invocation se detecta después de lease expiry;
- running attempt activo no se clasifica como orphan;
- TaskContextProvider incluye metadata de `executive-evidence:*`;
- metadata de evidence no ejecutiva no entra al contexto.

Integración PostgreSQL nueva:

`TestR23PostgreSQLProjectsWorkerEvidenceAndClosesDAGRace`

usa el harness PostgreSQL real de R22 y verifica:

1. corrida ejecutiva completa con decorators R23;
2. worker tiene dependencia durable al planning task;
3. review task contiene `executive-evidence:department:*` en `task_evidence`;
4. `internal/tasks/contextprovider` lee ese task desde PostgreSQL;
5. el context payload contiene el worker summary real (`bounded findings`) y su evidence ref (`integration:evidence:1`).

## Fitness

`check-executive-fitness.sh` ahora exige:

- orphaned-result guard;
- prospective `BudgetModels`;
- `DAGTasks` wired;
- `EvidenceTasks` wired;
- metadata contextual limitada a `executive-evidence:`;
- no migraciones R23;
- no capability-matrix changes;
- no R14 changes;
- se mantienen bans de provider concreto, shell, HTTP, SQL, observer, scheduler y ToolIntent execution.

## CI de rama

`.github/workflows/r23-verify.yml` ejecuta sobre PostgreSQL 17:

```bash
gofmt check
go vet ./...
go test ./internal/executive/... ./internal/tasks/contextprovider/...
go test -race -short ./internal/executive/... ./internal/tasks/contextprovider/...
bash scripts/check-executive-fitness.sh
go test -tags=integration ./internal/executive/... -count=1
go build ./...
```

El job publica commit status `r23/verify`.

## Blocker productivo que R23 NO relaja

El `main` usado como base sigue teniendo `model-egress-policy.yaml` con deny para Alibaba Token Plan y DeepSeek en `organizational`, `public` y `sanitized`.

Por tanto:

- routing CEO → Qwen/Alibaba existe;
- routing worker → DeepSeek existe;
- R21 adapter existe;
- R23 evidence/recovery layer existe;
- **pero el policy gate todavía impide el egress productivo de CEO y workers**.

R23 no cambia esa policy silenciosamente porque permitir `organizational` egress es una decisión de seguridad/owner distinta de una corrección de orquestación.

No debe declararse un smoke productivo Qwen/DeepSeek exitoso mientras esa policy continúe en deny.

## Reproducción

```bash
git fetch origin
git checkout feat/23-executive-evidence-recovery-layer

gofmt -w internal/executive internal/tasks/contextprovider
go vet ./...
go test ./internal/executive/... ./internal/tasks/contextprovider/...
go test -race -short ./internal/executive/... ./internal/tasks/contextprovider/...
bash scripts/check-executive-fitness.sh

# PostgreSQL 17 real
export ORG_TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5432/explorarte_test?sslmode=disable'
go test -tags=integration ./internal/executive/... -count=1

go build ./...
```

## Definition of Done R23

- [x] Evidence layer usa primitivas durables existentes.
- [x] Líder recibe WorkerResult validado + evidence refs + review criteria.
- [x] CEO recibe latest DepartmentReview validado por departamento.
- [x] Context Engine sigue siendo el único assembly path.
- [x] Executive evidence sigue untrusted/no-authority.
- [x] DAG worker no tiene ventana inicial ready.
- [x] Budget se valida antes de crear nueva invocation.
- [x] Crash con succeeded orphan no reinfiere automáticamente.
- [x] No nueva migración.
- [x] No capability widening.
- [x] No R14 changes.
- [x] `internal/executive` integration suite verde contra PostgreSQL 17 real (ver "Addendum" abajo).
- [ ] Egress productivo Alibaba/DeepSeek aprobado en una decisión separada antes de smoke real.

## Addendum — validación real post-doc (SHA `fa75153`)

El checkbox `r23/verify success en el SHA final` quedó sin marcar en la versión original de este documento porque el workflow de GitHub Actions **nunca llegó a ejecutarse**: los 5 check-runs sobre `a30b27b` fallan en check-runs, la anotación de GitHub CI en ~1-3 segundos con la anotación `The job was not started because recent account payments have failed or your spending limit needs to be increased` — un bloqueo de facturación de la cuenta, no un fallo de tests.

Al validar localmente contra PostgreSQL 17 real (`go test -tags=integration ./internal/executive/...`), la suite **fallaba**, y no por datos corruptos ni por contenedores compartidos: el mismo fallo se reproduce de forma determinista en `main` en el commit `3090966` (el merge de R21+R22, base exacta de R23) — es decir, **ninguno de los dos bugs encontrados es específico de R23**; R23 simplemente heredó una base que nunca se había corrido en verde contra Postgres real.

### Bug 1 — semántica `created`/`reused` invertida

`internal/executive/runtimeadapter/tasks.go`, método `Tasks.CreateTask`, pasaba el segundo valor de retorno de `tasks.Service.CreateTask` directo como `reused`. Ese valor significa **`created`** en su origen (`internal/tasks/postgres/create.go`, `createResult.Created`; `cmd/orgctl/tasks.go` nombra la misma llamada `created`). Resultado: toda creación de tarea nueva (el caso normal) se reportaba como `reused=true`, y cada test de integración de `internal/executive` trata eso como fatal por diseño. Fix: invertir una sola vez en el límite del adapter (`reused := !created`), no en cada llamador.

### Bug 2 — `driveDepartments` nunca señala `done=true`

`driveDepartments` no retornaba `true` en ningún punto de su cuerpo — ni siquiera al completar el loop sobre todos los departamentos con revisión `ReviewAccept`. Como el llamador (`Resume`) solo corta temprano cuando `done || err != nil`, con `done` permanentemente `false` el código caía a crear/avanzar la tarea de cierre del CEO en **cada** ciclo, incluido el primero — antes incluso de que el leader-plan de un departamento se hubiera manejado. `validateRunCompletionEvidence` rechazaba correctamente ese cierre prematuro ("department review is not verified completed"), y la suite lo ve como un error duro de `Resume()`. Fix: nuevo helper `driveInProgress` que hace que cada punto de "hice trabajo parcial, el llamador debe esperar al próximo ciclo" corte devolviendo el estado real vía `Status`, en vez de caer al cierre; el único `return ..., false, nil` que queda es el fallthrough genuino al final del loop, cuando todos los departamentos ya fueron aceptados.

### Validación tras los fixes (SHA `fa75153`)

```bash
go build ./...
go vet ./...
go test -count=1 ./internal/executive/... ./internal/tasks/contextprovider/...
go test -race -short ./internal/executive/... ./internal/tasks/contextprovider/...
bash scripts/check-executive-fitness.sh
# PostgreSQL 17 real, project aislado (--project-name explorarte-org-integration)
go test -count=1 -tags=integration ./internal/executive/...
```

Las cuatro pruebas de integración de `internal/executive` pasan: las tres preexistentes de R21/R22 (`TestExecutivePostgreSQL17EndToEndAndRestart`, `TestExecutivePostgreSQL17AmbiguousBlocksWithoutSecondInvocation`, `TestExecutivePostgreSQL17CompletionInconclusiveNeverCompletes`) y la propia de R23 (`TestR23PostgreSQLProjectsWorkerEvidenceAndClosesDAGRace`).

**Nota operativa:** `scripts/test-executive-integration.sh` no aísla su proyecto Docker (`compose.yaml` fija `name: explorarte-organization`), por lo que puede chocar con un stack de desarrollo persistente que use el mismo nombre por defecto. Usar `--project-name explorarte-org-integration` explícito (como hace `scripts/test-integration.sh`) para evitar colisiones.

**Riesgo para R24 y ramas futuras:** R24 se ramificó desde el SHA `a30b27b` (antes de este fix). Cualquier rama que dependa de `internal/executive/runtimeadapter` o de `Resume`/`driveDepartments` debe rebasar sobre este commit (`fa75153`) o cherry-pickear estos dos fixes antes de considerarse validada — de lo contrario hereda ambos bugs silenciosamente, igual que le pasó a R23.
