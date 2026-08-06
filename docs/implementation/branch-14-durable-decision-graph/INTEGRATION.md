# Rama 14 — Durable decision graph

## Estado

Parcial y en curso. Este primer corte introduce únicamente el dominio puro de `internal/decisiongraph`. PostgreSQL, migración `000012`, servicios, CLI, scheduler y composition root se añaden después de validar estos invariantes.

## Base exacta

- Base: `3584d9e7a2e44bbe9d953556704df5e84afd8cf3` (squash-merge de Rama 13).
- Rama: `feat/14-durable-decision-graph`.
- Migración reservada: `000012`.

## Objetivo

Añadir una traza estructurada y durable para ejecuciones multi-paso: goals, requirements, constraints, hypotheses, candidate actions, evidence, verifications and decisions. El grafo coordina invocaciones ya protegidas por Ramas 08-13; no reemplaza `modelruntime`, `modeldispatch`, identidad, egress ni el worker persistente.

## Fuente canónica

El vocabulario inicial se deriva de `docs/canonical/reasoning-assurance.yaml`:

- nodos: `goal`, `requirement`, `constraint`, `hypothesis`, `candidate_action`, `evidence`, `verification`, `decision`;
- edges: `depends_on`, `supports`, `contradicts`, `satisfies`, `prunes`, `selected_from`;
- branch states: `active`, `selected`, rechazos tipados, `superseded`, `inconclusive`;
- verification labels: `verified`, `inferred`, `unknown`, `contradicted`.

La política exige guardar structured decision traces y prohíbe persistir private chain-of-thought. Este primer corte mantiene únicamente schema versions y hashes de payload; no define un campo para razonamiento privado.

## Dominio implementado

`internal/decisiongraph` contiene:

- tipos y validación cerrada de enums;
- exactamente un goal por snapshot;
- IDs, schema versions, hashes y actor creador validados;
- detección de ciclos en el subgrafo de scheduling `depends_on`;
- semantic edges separados del scheduler: `supports` o `contradicts` no crean dependencias de ejecución;
- cálculo determinista de nodos ready basado en dependencias succeeded;
- transiciones default-deny para branch state y execution state;
- reapertura de branches rechazados solo con evidencia nueva explícita;
- `ambiguous` como estado terminal, sin retry implícito;
- `DecisionRecord` enlazado a candidate, evidence y verification nodes tipados;
- hash SHA-256 determinista del snapshot independiente del orden de entrada;
- budgets para nodes, depth, parallelism, model calls, tokens, replans, verifications y wall time;
- reserva de budget atómica, con detección de overflow.

## Invariantes

1. Un snapshot contiene exactamente un goal.
2. `depends_on` debe ser acíclico.
3. Un nodo solo está ready si está active/pending y todas sus dependencias succeeded.
4. Un branch rechazado no vuelve a active sin evidencia nueva.
5. Un candidate seleccionado debe estar en branch state `selected`.
6. Una decisión referencia nodos de evidencia y verificación del tipo correcto.
7. `ambiguous` no puede volver a ready ni ejecutarse otra vez automáticamente.
8. El budget se reserva antes de ejecutar y nunca se actualiza parcialmente.
9. El hash del grafo no depende del orden de slices recibido.
10. No se guarda private chain-of-thought.

## Próximos cortes de Rama 14

1. Esquema PostgreSQL `000012` para runs, graph snapshots, nodes, edges, executions, observations, verifications, decisions y budget ledger.
2. Store PostgreSQL con claims transaccionales de nodos ready y consumo de budget en la misma transacción.
3. Vínculo de `decision_node_executions` con `model_invocations` y `model_dispatch_attempts`; no se duplican sus estados.
4. Servicios de creación de run, append de graph version, claim, recording de observations/verifications y terminal decision.
5. Fitness, migration up/down/reapply y PostgreSQL 17 integration tests.
6. CLI y wiring productivo solo después de cerrar la semántica durable.

## Fuera de alcance

- memoria y RAG;
- tools o shell arbitrario;
- MCTS/AB-MCTS abierto;
- self-modification;
- promoción de candidatos;
- entrenamiento o RL online;
- auto-completion de tasks;
- modificación automática de capabilities, egress o identidad.

## Relación con Rama 15

Rama 15 puede desarrollar en paralelo su core aislado de evaluación y promoción contra interfaces/fakes. No debe importar `internal/decisiongraph` ni tocar migraciones, canonical docs, composition root, Makefile o CI hasta que Rama 14 se fusione. Después se rebasea sobre el squash de Rama 14 y adapta sus `TraceRef`/`TraceSource` a este ledger durable.
