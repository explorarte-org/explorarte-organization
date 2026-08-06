# Rama 15 — Bounded self-improvement

## Estado

Completa. El core puro (`internal/evaluation`, `internal/improvement`) se
desarrolló en paralelo a la Rama 14; tras su fusión, esta rama se rebaseó
sobre `main` y se cerró con la integración durable: adapter `TraceSource`
contra el ledger de decisiones, migración `000013`, store de PostgreSQL para
`Candidate` con optimistic concurrency, guard de estados a nivel de base de
datos, y fitness.

## Base exacta

- Base original del core puro: `3584d9e7a2e44bbe9d953556704df5e84afd8cf3`
  (squash-merge de Rama 13).
- Rebaseada sobre `main` tras el squash-merge de Rama 14.
- Rama: `feat/15-bounded-self-improvement`.
- Migración: `000013_create_bounded_self_improvement`.

## Objetivo

Dar a la organización una forma acotada y auditable de proponer, evaluar y
promover mejoras (candidatos) sobre su propio comportamiento, sin permitir
nunca una promoción automática a producción sin evaluación ni gate explícito.

## Dominio puro

### `internal/evaluation`

- `TraceRef`: referencia opaca (run ID, trace hash, schema version,
  organization ID). El mismo shape que `decisiongraph.TraceRef` (alineación
  deliberada de `OrganizationID` a `string`, corregida durante el desarrollo
  paralelo para que el futuro adapter no necesitara conversión con pérdida).
- `EvaluationTrace`: traza materializada por un `TraceSource`, con
  verificación de integridad (`ContentHash()` debe igualar `Ref.TraceHash`).
- `EvaluationSuite` / `EvaluationCase`: conjunto versionado de casos, cada
  uno atado a un `TraceRef`, un resultado esperado y un peso (`Weight`,
  usado por `CompareSuite` para `WeightedPassRatio`, informativo — nunca
  sustituye el veredicto de seguridad).
- `EvaluationRequest` / `EvaluationResult`: contrato de entrada/salida de un
  `Evaluator` (`SubjectID`/`SubjectArtifactHash`, ya que ambos campos
  identifican tanto al baseline como al candidato, no solo al candidato),
  con `EvaluationRole` (`baseline` | `candidate`), métricas (`Metric`) y
  veredicto (`Verdict`: `pass` | `fail` | `inconclusive`).
- `CompareResults` / `CompareSuite`: comparación determinista
  baseline-versus-candidate. Verifica que ambos resultados compartan
  `TraceRef` (y que esa `TraceRef` sea la que la suite fija para el caso),
  que el delta de cada métrica sea finito, y agrega con `worseVerdict`
  (fail > inconclusive > pass) para que un solo caso roto nunca quede
  oculto.
- `TraceSource`, `Evaluator`: puertos mínimos sugeridos por el owner.
- `Service`: orquesta carga de traza + evaluación + comparación sin estado
  propio; valida que el `Evaluator` haya respondido sobre el caso/rol/trace
  que efectivamente se le pidió, no confía ciegamente en su respuesta.
- `FakeTraceSource` (clona `Payload` en `Seed`/`LoadTrace`), `FakeEvaluator`:
  fakes seguros para concurrencia.

### `internal/improvement`

- `ArtifactRef`, `Lineage`, `Candidate` (con `CanonicalHash()` — hash
  estable de artefacto + lineage, independiente de ID/estado/timestamps).
- `CandidateState`: los diez estados del owner. `ValidateCandidateTransition`
  es una máquina default-deny (`TestCandidateTransitionMatrixIsDefaultDeny`
  recorre las 100 combinaciones); `proposed -> active` es irreproducible.
- `RollbackTarget`: solo válido con `FromState` en `canary`/`active`, y un
  candidato en `rolled_back` exige tenerlo seteado (invariante verificada en
  ambas direcciones).
- `PromotionRequest`/`PromotionDecision`: el `PromotionRequest` exige una
  `Evaluation` válida y con veredicto `pass` — una promoción nunca se
  autoriza sobre evidencia fallida o vacía, independientemente de lo que
  decida el `ApprovalGate`. `CandidateHash` es el `CanonicalHash` del
  candidato (identidad completa artefacto+lineage), no solo el hash del
  artefacto.
- `ApprovalGate`: único camino a `canary`/`active`. `Deprecate`/`RollBack`
  son acciones de seguridad, nunca pasan por el gate.
- `Service`: revalida el candidato después de mutarlo (salvo en el paso a
  `rolled_back`, donde `RollBack` valida tras adjuntar `RollbackTarget`).
- `CandidateStore` (puerto nuevo, no consumido por `Service`): persistencia
  con optimistic concurrency. `Service` permanece sin estado exactamente
  como en el corte anterior — el composition root es quien hace
  cargar → mutar → guardar contra la revisión leída.

## Integración durable

### `internal/decisiongraphtrace` — adapter `TraceSource`

Lee un run `succeeded` de la Rama 14 directamente de sus tablas de Postgres
(mismo patrón que `internal/cellworker/postgres` leyendo `model_invocations`
sin pasar por el service layer de otra rama) — nunca importa el paquete Go
de `internal/decisiongraph` desde código no-test. Construye su propio
payload JSON canónico (nodos y edges por `logical_node_id`, ordenados
explícitamente para no depender del orden de escaneo de la base) y calcula
su propio hash SHA-256, sin reutilizar el hash de auditoría interno de
`decisiongraph.Store.TraceRef` (una concatenación de cinco hashes distintos
vía un helper no exportado — replicarlo desde otro paquete habría sido un
acoplamiento frágil a un formato que nunca fue un contrato público entre
ramas). `TraceRefForRun` y `LoadTrace` recalculan el mismo payload de forma
independiente y verifican que el hash siga coincidiendo.

### Migración `000013`

- `improvement_candidates`: una fila por `Candidate`, columna `revision`
  para optimistic concurrency, y un trigger `BEFORE UPDATE`
  (`improvement_guard_candidate_update`) que reproduce exactamente el mismo
  mapa default-deny de `transitions.go` a nivel de base de datos — un
  `UPDATE` directo que salte la capa Go tampoco puede hacer
  `proposed -> active`. Cierra un riesgo que Qwen señaló en su revisión de
  cierre de la Rama 14 ("la migración 000013 debería incluir constraints o
  triggers para transiciones inválidas").
- `improvement_promotion_decisions`: historial append-only de
  `PromotionRequest`/`PromotionDecision`, inmutable tras el insert (mismo
  patrón que las tablas de eventos de `decisiongraph`).

### `internal/improvement/postgres` — `CandidateStore`

Implementa `ProposeCandidate`, `GetCandidate`, `SaveCandidate` (falla con
`ErrRevisionConflict` si la revisión ya no coincide) y
`RecordPromotionDecision`.

### `scripts/check-improvement-fitness.sh`

Wireado en `make verify`. Antes de confiar en los chequeos negativos
(por ejemplo, "`proposed -> active` no debe aparecer en el trigger SQL"),
cada uno se probó inyectando el bug real y confirmando que el script lo
detecta — dos de los primeros intentos no lo hacían (un regex de Go cegado
por las llaves de `struct{}{}`, y un `\|` mal escapado) y se corrigieron o
se reemplazaron por la verificación real en Go (`TestCandidateTransitionMatrixIsDefaultDeny`).

## Invariantes

1. `proposed -> active` es irreproducible en Go y en la base de datos.
2. `evaluating -> approved | rejected | inconclusive` se decide únicamente a
   partir del `OverallVerdict` de una comparación baseline-versus-candidato.
3. `approved -> canary` y `canary -> active` requieren `ApprovalGate`
   autorizado sobre una evaluación con veredicto `pass`; `denied` no cambia
   el estado.
4. `Deprecate` y `RollBack` nunca pasan por el gate.
5. Un rollback exige que `RollbackTarget.FromState` coincida con el estado
   real, y un candidato `rolled_back` exige tener `RollbackTarget`.
6. El hash canónico de un candidato depende solo de artefacto + lineage.
7. Una traza se valida contra el hash inmutable de su `TraceRef` antes de
   usarse, tanto en `evaluation.EvaluationTrace.Validate()` como en
   `decisiongraphtrace.Store.LoadTrace`.
8. Guardar un candidato exige la revisión exacta leída; un escritor
   concurrente falla con `ErrRevisionConflict` en vez de pisar el cambio.
9. Las decisiones de promoción son un registro append-only inmutable.

## Fuera de alcance

- CLI (`orgctl improvement ...`): no incluida en este cierre, siguiendo el
  mismo precedente de la Rama 14 (que también difirió su CLI al commit
  final) y el plan de 8 pasos del owner, que no la menciona.
- Memoria, RAG, tool execution, shell, credenciales en este paquete.
- RL online, promoción automática sin gate, experimentos sobre datos
  clínicos reales.

## Relación con Rama 14

Rama 15 depende de Rama 14; Rama 14 nunca depende de Rama 15. El dominio
puro (`internal/evaluation`, `internal/improvement`) nunca importa
`internal/decisiongraph`; solo `internal/decisiongraphtrace` (código de
test, no productivo) lo hace, para levantar un run real vía el `Service`
público de `decisiongraph` como fixture de integración.
