# Rama 15 — Bounded self-improvement

## Estado

Parcial y en curso. Este primer corte introduce únicamente el dominio puro de
`internal/evaluation` e `internal/improvement`, desarrollado en paralelo a la
Rama 14. PostgreSQL, migración `000013`, adapter de `TraceSource` contra el
ledger durable, fitness y wiring productivo se añaden después de que la Rama
14 se fusione.

## Base exacta

- Base: `3584d9e7a2e44bbe9d953556704df5e84afd8cf3` (squash-merge de Rama 13).
- Rama: `feat/15-bounded-self-improvement`.
- Migración reservada: `000013`.

## Objetivo

Dar a la organización una forma acotada y auditable de proponer, evaluar y
promover mejoras (candidatos) sobre su propio comportamiento, sin permitir
nunca una promoción automática a producción sin evaluación ni gate explícito.

## Dominio implementado

### `internal/evaluation`

- `TraceRef`: referencia opaca e inmutable a una traza de ejecución (run ID,
  trace hash, schema version, organization ID). No importa
  `internal/decisiongraph`; es un placeholder hasta que la Rama 14 se
  fusione y aporte un `TraceSource` real.
- `EvaluationTrace`: traza materializada por un `TraceSource`, con
  verificación de integridad (`ContentHash()` debe igualar `Ref.TraceHash`).
- `EvaluationSuite` / `EvaluationCase`: conjunto versionado de casos, cada
  uno atado a un `TraceRef` y un resultado esperado; validación cierra IDs
  duplicados y sets vacíos.
- `EvaluationRequest` / `EvaluationResult`: contrato de entrada/salida de un
  `Evaluator`, con `EvaluationRole` (`baseline` | `candidate`), métricas
  (`Metric`) y veredicto (`Verdict`: `pass` | `fail` | `inconclusive`).
- `CompareResults` / `CompareSuite`: comparación determinista
  baseline-versus-candidate, produce `MetricDelta` por métrica y un
  `OverallVerdict` que nunca oculta un fallo o resultado inconcluso
  (`worseVerdict` prioriza fail > inconclusive > pass al agregar por suite).
- `TraceSource`, `Evaluator`: puertos mínimos sugeridos por el owner. `Clock`
  / `SystemClock` para tiempo inyectable, igual que en `decisiongraph`.
- `Service`: orquesta carga de traza + evaluación + comparación sin estado
  propio ni dependencia de base de datos.
- `FakeTraceSource`, `FakeEvaluator`: fakes seguros para concurrencia,
  exportados para reutilizarse desde `internal/improvement` y desde
  wiring futuro.

### `internal/improvement`

- `ArtifactRef`: referencia inmutable y con hash de contenido al artefacto
  que un candidato propone (config, prompt, política o diff); el contenido
  en sí es opaco para este paquete.
- `Lineage`: de qué candidato/artefacto se deriva uno nuevo, si aplica; una
  raíz no tiene padre.
- `Candidate`: agregado con `ID`, `Artifact`, `Lineage`, `CandidateState`,
  timestamps y `RollbackTarget` opcional. `CanonicalHash()` es la identidad
  estable del candidato (hash de artefacto + lineage), independiente de ID,
  estado o timestamps.
- `CandidateState` con exactamente los diez estados recomendados por el
  owner (`proposed`, `validated`, `evaluating`, `rejected`, `inconclusive`,
  `approved`, `canary`, `active`, `deprecated`, `rolled_back`).
- `ValidateCandidateTransition`: máquina de estados default-deny mediante un
  mapa explícito de transiciones permitidas, en el mismo estilo que
  `ValidateExecutionTransition` de `decisiongraph`. Cualquier par no
  presente en el mapa es rechazado, incluyendo `proposed -> active`, que es
  estructuralmente irreproducible (cubierto por
  `TestCandidateTransitionMatrixIsDefaultDeny`, que recorre las 100
  combinaciones posibles de los 10 estados).
- `PromotionRequest` / `PromotionDecision` / `PromotionKind` /
  `PromotionOutcome`: contrato del `ApprovalGate`. Un `PromotionRequest` se
  autovalida contra la transición esperada de su `PromotionKind`
  (`to_canary` exige `approved -> canary`; `to_active` exige
  `canary -> active`).
- `RollbackTarget`: sólo válido con `FromState` en `canary` o `active`; debe
  coincidir con el estado actual del candidato para aplicarse.
- `ApprovalGate`: único punto por el que un candidato puede avanzar a
  `canary` o `active`. `Deprecate` y `RollBack` son acciones de seguridad y
  nunca requieren gate.
- `Service`: sin persistencia; cada método recibe el `Candidate` actual y
  devuelve el siguiente. `RecordEvaluationVerdict` mapea
  `evaluation.SuiteComparisonResult.OverallVerdict` a
  `approved | rejected | inconclusive` de forma determinista.
- `FakeApprovalGate`: autoriza por defecto; permite inyectar denegaciones
  para probar que el estado del candidato no cambia ante un rechazo.

## Invariantes

1. `proposed -> active` es irreproducible: sólo existe la ruta
   `proposed -> validated -> evaluating -> approved -> canary -> active`.
2. Ninguna transición fuera del mapa explícito de `candidateTransitions` es
   posible; el default es deny, no allow.
3. `evaluating -> approved | rejected | inconclusive` se decide únicamente a
   partir del `OverallVerdict` de una comparación baseline-versus-candidato,
   nunca a mano.
4. `approved -> canary` y `canary -> active` requieren una decisión
   `authorized` de `ApprovalGate`; una decisión `denied` no cambia el estado
   del candidato.
5. `Deprecate` y `RollBack` nunca pasan por el gate.
6. Un rollback exige que `RollbackTarget.FromState` coincida con el estado
   real del candidato (`canary` o `active`).
7. El hash canónico de un candidato depende sólo de su artefacto y su
   lineage, nunca de su ID, estado o timestamps.
8. Una traza cargada por un `TraceSource` se valida contra el hash
   inmutable de su `TraceRef` antes de usarse.
9. Una comparación exige el mismo conjunto de nombres/unidades de métrica en
   baseline y candidato; si no son comparables, falla explícitamente en vez
   de comparar parcialmente.
10. No se persiste nada: toda la lógica de este corte es pura y determinista
    dado el mismo `Clock`.

## Fuera de alcance de este corte

- PostgreSQL, migración `000013`, composition root, CLI.
- Adapter real de `TraceSource` contra `internal/decisiongraph`.
- Fitness y CI completa.
- Auto-merge, edición de código productivo fuera de
  `internal/evaluation/**` e `internal/improvement/**`.
- Modificación de capabilities, egress o identidad.
- RL online, promoción automática sin gate, experimentos sobre datos
  clínicos reales.

## Integración final (cuando la Rama 14 se fusione)

1. Rama 14 se fusiona.
2. Rebase de `feat/15-bounded-self-improvement` sobre `main`.
3. Adaptar `TraceSource` al ledger durable de `internal/decisiongraph`.
4. Añadir migración `000013`.
5. Añadir PostgreSQL store y wiring.
6. Incorporar fitness e integración.
7. CI completa.
8. PR de Rama 15.

## Relación con Rama 14

Rama 15 depende de Rama 14; Rama 14 nunca depende de Rama 15. Este corte no
importa `internal/decisiongraph` ni toca `migrations/**`,
`docs/canonical/**`, el composition root, el Makefile ni CI, tal como fija
la sección "Relación con Rama 15" del `INTEGRATION.md` de la Rama 14.
