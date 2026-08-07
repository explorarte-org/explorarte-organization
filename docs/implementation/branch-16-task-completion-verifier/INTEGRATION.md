# Rama 16 — Task Obligation & Completion Verifier

## Estado

Completa. Implementa la Fase 2 de `docs/canonical/reasoning-assurance.yaml`
(`task_obligation_and_completion_verifier`): un verificador independiente,
de solo lectura, de las cinco obligaciones que esa fase exige antes de dar
por completada una tarea.

## Base exacta

- Base: `76ad1e04b9f06108233bc5b6b7f4f486b0a99d82` (merge de PR #17, fix de
  autorización con organización retirada).
- Rama: `feat/16-task-completion-verifier`.
- Migración: ninguna. R16 no posee tablas propias — es puramente un lector
  cruzado de tablas de otras cuatro ramas ya existentes.

## Objetivo

Hoy `internal/tasks.Finalize` acepta `FinalCompleted` con dos chequeos
puramente estructurales: la tarea debe estar en `awaiting_verification`, y
todos los `requirements` obligatorios deben estar en `satisfied`. Pero
"satisfied" es autoatestiguado — quien llama `RecordEvidence`/
`RecordRequirementVerification` pasa `Satisfies: true` directamente, sin que
nada confirme que el artifact referenciado existe, que el check realmente
corrió y pasó, que la aprobación fue efectivamente consumida, o que la
decisión del decision graph en la que se apoya la tarea sigue vigente.

R16 cierra ese hueco releyendo la verdad directamente de los cuatro sistemas
de registro involucrados — `internal/tasks`, `internal/staging`,
`internal/authorization`, `internal/decisiongraph` — en vez de confiar en lo
que `internal/tasks` ya cree sobre sí mismo.

## Dominio puro (`internal/completion`)

- `VerificationLabel`: `verified | inferred | unknown | contradicted` — el
  mismo vocabulario que ya usa `decision_graph.verification_labels`,
  reutilizado a propósito.
- `ObligationID`: las cinco exactas del scope de la Fase 2 —
  `requirements_satisfied`, `artifact_exists`, `checks_passed`,
  `approval_present`, `no_rejected_branch_reused`.
- `Verdict`: `pass | fail | inconclusive`. Una obligación `contradicted`
  siempre produce `fail`; ninguna `contradicted` pero al menos una
  `unknown` produce `inconclusive` — nunca se colapsan en el mismo
  resultado, porque la corrección es distinta ("hay un problema real" vs.
  "falta evidencia"), siguiendo el propio principio del canon: *"Absence of
  proof is not proof of falsity."*
- `Service.Verify(ctx, VerificationRequest{TaskID, AttemptID})`: recorre los
  `requirements` obligatorios de la tarea y, según su `Type`, dispara el
  chequeo independiente correspondiente. Nunca muta nada — reporta un
  veredicto, el caller decide qué hacer con él.
- Cinco puertos (`ports.go`), cada uno resuelto por el adapter de Postgres
  leyendo tablas ajenas directamente — nunca importando el paquete Go de la
  otra rama —, el mismo patrón que ya establecieron
  `internal/cellworker/postgres` y `internal/decisiongraphtrace`:
  - `TaskReader` → `tasks`/`task_requirements`/`task_evidence` (Rama 04).
  - `ArtifactChecker` → `staging_artifacts` (Rama 05), comparando el
    digest real contra el que quedó en la evidencia.
  - `CheckRunChecker` → `staging_checks` (Rama 05), **no** el evidence log
    de tareas: es el registro real de que un check corrió y pasó,
    independiente de lo que se autoatestiguó en `task_evidence`.
  - `ApprovalChecker` → `authorization_requests` (Rama 06), exige
    `status='consumed'` y `action_digest` exacto.
  - `DecisionBranchChecker` → `decision_graph_runs` + `decision_records` +
    `decision_graph_nodes` (Rama 14), resolviendo el run por
    `(task_id, attempt_id)` — sin necesitar que el caller conozca el
    `run_id` de antemano, porque `decisiongraph.Run` ya carga esos dos
    campos directamente.

### Decisión de diseño: `no_rejected_branch_reused` es defensa en profundidad, no una carrera real

El trigger `decision_graph_guard_node_update` (migración `000012`) ya hace
que `BranchSelected` sea efectivamente terminal: un nodo `selected` nunca
puede transicionar a otro estado — solo `active` puede ir a
`selected`/`rejected_by_*`, y solo `rejected_by_*`/`inconclusive` puede
reabrir de vuelta a `active`. Combinado con que
`DecisionRecord.Validate` ya exige `candidate.BranchState == BranchSelected`
al grabar la decisión, el escenario "una decisión seleccionada se rechaza
después por evidencia nueva" es estructuralmente inalcanzable hoy.

`no_rejected_branch_reused` sigue siendo un chequeo real y con valor: releer
el estado desde `decisiongraph` en vez de confiar en cualquier valor que
haya viajado por otros caminos, y cubre un caso que el schema sí permite —
más de un `decision_graph_runs` para el mismo `(task_id, attempt_id)` (una
corrida de razonamiento repetida), donde la decisión correcta a usar es la
del run `succeeded` más reciente, no una anterior.

### Ausencia de run de decisión no es "unknown"

La mayoría de las tareas nunca tocan el decision graph. Si
`DecisionBranchChecker` no encuentra ningún run para `(task_id, attempt_id)`,
la obligación se marca `verified` (verdad vacía: no hubo ninguna rama que
reutilizar), no `unknown` — de otro modo, cualquier tarea normal saldría
`inconclusive` por defecto solo por no haber usado el decision graph.

## Adapter de Postgres (`internal/completion/postgres`)

`Store` implementa los cinco puertos con SQL puro contra las tablas de las
Ramas 04/05/06/14, sin importar sus paquetes Go. No escribe nada — el
fitness script verifica explícitamente que no exista ningún
`INSERT`/`UPDATE`/`DELETE`/`TRUNCATE` en `store.go`.

`TestCompletionStorePostgreSQL17` (integración real) cubre: todas las
obligaciones verificadas y `pass`; digest de artifact que no coincide y
`fail`; ausencia de run de decisión y `pass` vacío; tarea inexistente y
`ErrTaskNotFound`. La corrida feliz usa `driveRunToSucceeded`, adaptada
directamente del helper que ya escribió `internal/decisiongraphtrace` para
llevar un run real a `succeeded` con una decisión terminal grabada, en vez
de insertar filas de `decision_graph_*` a mano.

## CLI (`orgctl completion verify`)

`cmd/orgctl/completion.go` es su propia composition root — abre Postgres,
construye `completionpostgres.Store` + `completion.Service`, y expone
`orgctl completion verify -task <id> -attempt <id> [-json]`. Códigos de
salida nuevos: `10` (verificación falló, alguna obligación `contradicted`),
`11` (inconclusa, alguna obligación `unknown` sin ninguna `contradicted`).

## Decisión de diseño: no se tocó `internal/tasks`

`reasoning-assurance.yaml` exige `enforcement:
block_terminal_completion_on_failed_verification` — pero hoy no existe
ningún orquestador automático que finalice tareas (`orgctl task finalize`
es un comando manual). Modificar `internal/tasks.Finalize` para depender de
`internal/completion` habría violado el invariante de `modular_boundaries`
(una rama ya construida y probada no debería ganar una dependencia dura
hacia una rama futura) sin ganar enforcement real, porque no hay ningún
caller automatizado hoy que se beneficie de eso.

En cambio, R16 queda como una capa de verificación independiente y el
contrato explícito es: **cualquier flujo automatizado que finalice tareas
(R22, el orquestador CEO) debe llamar `completion.Service.Verify` y exigir
`VerdictPass` antes de llamar `tasks.Service.FinalizeTask` con
`FinalCompleted`.** `orgctl task finalize` sigue sin cambios; operadores
humanos pueden correr `orgctl completion verify` antes de finalizar como
buena práctica, pero el enforcement automático real llega recién con R22.

## Fitness (`scripts/check-completion-fitness.sh`)

Cada check se probó inyectando el bug real que dice atrapar antes de
confiar en él:

- Import directo de `internal/tasks`/`staging`/`authorization`/
  `decisiongraph` en código no-test → detectado (los tests de integración
  están explícitamente exceptuados, porque legítimamente accionan el
  `Service` real de `decisiongraph` para armar fixtures, mismo precedente
  de `internal/decisiongraphtrace`).
- SQL mutante en `store.go` → detectado.
- Vocabulario cerrado de `ObligationID` desalineado → detectado.
- `aggregateVerdict` sin cortocircuito inmediato a `VerdictFail` ante
  `LabelContradicted` → detectado.

## Verificación

- `go build`/`go vet` (incl. `-tags=integration`): limpios.
- `go test ./...`: 13 tests de dominio + verificación de integración real.
- `./scripts/test-integration.sh all`: 15 suites de Postgres, verdes.
- `make verify`: fmt, vet, tests, 14 fitness scripts, build — verde.

## Próxima rama compatible

R17 (Organization Rules Shadow Verifier, Fase 1 de reasoning-assurance) no
depende de R16. R22 (CEO Executive Orchestrator) sí: es quien debe llamar
`completion.Service.Verify` antes de finalizar tareas automáticamente,
según el contrato documentado arriba.
