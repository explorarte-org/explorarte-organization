# Rama 14 — Durable decision graph

## Estado

Implementación completa en `feat/14-durable-decision-graph`; validación final de CI pendiente. No abrir PR ni fusionar hasta que el workflow normal `ci.yml` finalice con `verify` y `postgres-final-validation` en verde sobre el árbol definitivo.

## Base exacta

- Base: `3584d9e7a2e44bbe9d953556704df5e84afd8cf3` (squash-merge de Rama 13).
- Rama: `feat/14-durable-decision-graph`.
- Migración: `000012_create_durable_decision_graph`.
- No modifica `docs/canonical`.
- El SHA final y el run ID se registran después de la validación autoritativa.

## Objetivo

Añadir una traza estructurada y durable para ejecuciones multi-paso: goals, requirements, constraints, hypotheses, candidate actions, evidence, verifications y decisions.

El grafo coordina invocaciones ya protegidas por las Ramas 08–13. No reemplaza:

- `tasks`, que conserva la autoridad de finalización de tareas;
- `modelruntime`, que conserva claim, envío y outcome de cada invocación;
- `modeldispatch`, que conserva principals y assignments;
- identidad criptográfica, egress, provider adapter o worker persistente.

## Fuente canónica

El vocabulario deriva de `docs/canonical/reasoning-assurance.yaml`:

- nodos: `goal`, `requirement`, `constraint`, `hypothesis`, `candidate_action`, `evidence`, `verification`, `decision`;
- edges: `depends_on`, `supports`, `contradicts`, `satisfies`, `prunes`, `selected_from`;
- branch states: `active`, `selected`, `rejected_by_evidence`, `rejected_by_policy`, `rejected_by_capability`, `rejected_by_dependency`, `rejected_by_budget`, `superseded`, `inconclusive`;
- verification labels: `verified`, `inferred`, `unknown`, `contradicted`.

La fuente canónica exige una structured decision trace y prohíbe persistir private chain-of-thought. La Rama 14 guarda tipos, hashes, versiones, relaciones, evidencia opaca y reason codes; no define campos para razonamiento privado, prompts completos ni respuestas completas.

## Paquetes

`internal/decisiongraph` contiene:

- tipos y validación cerrada de enums;
- exactamente un goal por snapshot;
- validación de IDs, schema versions, hashes y actores;
- detección de ciclos en `depends_on`;
- separación entre edges semánticos y edges de scheduling;
- cálculo determinista de nodos ready;
- transiciones default-deny de branch y ejecución;
- reapertura de branches rechazaros solo mediante evidencia nueva;
- `ambiguous` terminal y no retryable;
- decisiones enlazadas a candidate, evidence y verification nodes tipados;
- hash SHA-256 determinista del snapshot;
- budgets de nodos, profundidad, paralelismo, model calls, tokens, replans, verificaciones y wall time;
- servicio de aplicación y puertos sin dependencia de PostgreSQL.

`internal/decisiongraph/postgres` implementa el ledger durable con transacciones serializables y claims mediante `FOR UPDATE ... SKIP LOCKED`.

## Migración 000012

Tablas:

- `decision_graph_runs`: identidad del run, task/attempt, policy pin, límites y estado terminal;
- `decision_graph_versions`: snapshots inmutables y versionados;
- `decision_graph_nodes`: nodos tipados por versión;
- `decision_graph_edges`: relaciones tipadas;
- `decision_branch_events`: historial append-only de selección, rechazo y reapertura;
- `decision_graph_budgets`: contadores durables actuales;
- `decision_node_executions`: claims, leases y vínculo opcional con model runtime;
- `decision_observations`: observaciones opacas y hasheadas;
- `decision_verifications`: labels, verifier refs, reason codes y evidence-set hashes;
- `decision_records`: decisión terminal única por run;
- `decision_budget_events`: ledger append-only del consumo de budget.

Restricciones principales:

- FKs compuestas preservan `organization_id`, `run_id` y `graph_version_id`;
- el grafo `depends_on` se valida en dominio y mediante trigger de PostgreSQL;
- un solo execution activo por nodo;
- un solo terminal decision record por run;
- observations, verifications, decisions, versions, edges, branch events y budget events son inmutables;
- los updates de runs y nodos pasan por triggers de transición default-deny;
- una reapertura hacia `active` exige un branch event con `evidence_hash` dentro de la misma transacción;
- los tokens de claim se persisten exclusivamente como SHA-256;
- el esquema no contiene private reasoning, credenciales, prompts ni respuestas completas.

## Flujo durable

```text
create run
  ↓
append immutable graph version
  ↓
start run
  ↓
claim ready node + reserve budget atomically
  ↓
execute through an external orchestrator
  ↓
finish execution + consume tokens/wall-time
  ↓
record observation / verification
  ↓
transition branches using typed evidence
  ↓
record terminal decision
  ↓
export TraceRef for evaluation
```

`AppendGraph` permite nuevas versiones mientras el run está `planned` o `running`. Cada versión posterior consume `used_replans`; cada snapshot vuelve a validar el DAG y consume nodos/profundidad de forma durable.

## Claim y recovery

El claim:

1. bloquea run y budget;
2. comprueba estado, deadline, paralelismo y model-call budget;
3. selecciona un nodo de la última versión con `SKIP LOCKED`;
4. revalida todas sus dependencias;
5. genera un token criptográfico;
6. persiste solo su hash;
7. crea `decision_node_executions`;
8. cambia el nodo a `running`;
9. reserva paralelismo/model call;
10. inserta un budget event;
11. confirma todo en una sola transacción.

Dos workers pueden competir por el mismo run, pero solo uno puede crear la ejecución efectiva.

La recuperación de un lease vencido:

- nunca vuelve el nodo a `ready` automáticamente;
- marca `ambiguous` si existe una invocación cuyo envío puede haber ocurrido;
- marca `failed` cuando no existe riesgo de duplicar trabajo externo;
- libera el slot paralelo solo si todavía estaba reservado;
- consume wall time observado;
- falla el run y rechaza la rama por budget cuando corresponde.

## Budgets

Límites pinned por run:

- `max_nodes`;
- `max_depth`;
- `max_parallel_nodes`;
- `max_model_calls`;
- `max_input_tokens`;
- `max_output_tokens`;
- `max_replans`;
- `max_verifications`;
- `max_wall_time`.

Los contadores se actualizan dentro de las mismas transacciones que append, claim, finish, verification o recovery. Cada cambio produce un `decision_budget_events` inmutable.

Un exceso de tokens o wall time registra el consumo real, falla el run con `budget_exceeded` y rechaza la rama activa como `rejected_by_budget`. Las comparaciones evitan overflow signed antes del update PostgreSQL.

## Branch transitions

`TransitionBranch` es la única operación de aplicación para modificar `branch_state`.

Reglas:

- desde `active` puede seleccionarse, rechazarse, supersederse o quedar inconclusive;
- un branch rechazado/inconclusive solo puede volver a `active` con un `evidence_hash` nuevo explícito;
- `selected` y `superseded` no se reabren;
- cada transición crea un evento append-only antes del update;
- eventos con el mismo contenido pueden repetirse legítimamente después de una reapertura, por lo que `event_hash` no actúa como idempotency key.

## Vínculo con model runtime

`decision_node_executions` puede fijar conjuntamente:

- `model_invocation_id`;
- `dispatch_attempt_id`.

Ambos deben pertenecer al mismo task/attempt del run y respetar la FK compuesta del runtime. La Rama 14 no copia prompts, resultados ni estados del provider; conserva solo los IDs durables y hashes de outcome.

Un outcome ambiguo del runtime se conserva como `ExecutionAmbiguous` y no se redispatcha automáticamente.

## CLI

Comandos explícitos:

```text
orgctl decision create
orgctl decision append
orgctl decision start
orgctl decision transition
orgctl decision claim
orgctl decision finish
orgctl decision observe
orgctl decision verify
orgctl decision decide
orgctl decision recover
orgctl decision trace
```

Las mutaciones complejas reciben JSON estricto mediante `--file`; unknown fields y múltiples valores top-level son rechazados. `decision finish` lee el claim token exclusivamente desde stdin mediante el helper de tokens existente, por lo que el bearer token no se incluye en JSON ni argumentos de proceso.

No se añade un scheduler automático a `orgd`. El lanzamiento de runs y la decisión de qué ejecutor procesa cada nodo siguen siendo explícitos. Esto evita crear un segundo worker implícito antes de definir la política de orquestación posterior.

## Invariantes

1. Cada snapshot tiene exactamente un goal.
2. `depends_on` es acíclico en dominio y PostgreSQL.
3. Un nodo solo se reclama si está active, pendiente/ready y todas sus dependencias succeeded.
4. El claim y la reserva de budget son atómicos.
5. El token de claim nunca se persiste en claro.
6. Un branch rechazado no se reabre sin evidencia nueva.
7. `ambiguous` es terminal y no vuelve a ready.
8. Una terminal decision requiere candidate seleccionado y verificación `verified` o `inferred`.
9. Toda decisión terminal conserva hashes de evidence/verification sets.
10. Completing a decision run no completa automáticamente la task.
11. El ledger no ejecuta tools, shell, HTTP ni adapters.
12. El ledger no accede a credenciales.
13. No se guarda private chain-of-thought.
14. Todos los registros históricos relevantes son append-only.
15. Un run no puede exceder budgets silenciosamente.

## Modelo de amenazas

Controles incorporados:

- tenant isolation mediante `organization_id` y FKs compuestas;
- transacciones serializables para append, claim, branch transition, verification, finish y terminal decision;
- optimistic discovery + authoritative transactional claim;
- token bearer de alta entropía almacenado solo como hash;
- stdin para consumir el token en CLI;
- leases acotados y recovery explícito;
- no retry automático después de outcomes ambiguos;
- graph cycle guard en dos capas;
- immutable ledger triggers;
- payloads externos representados por hashes y refs opacas;
- budgets consumidos antes o dentro de la operación protegida;
- no provider, network, secret o tool boundary dentro del paquete.

## Riesgos residuales

- `evidence_set_hash`, `verification_set_hash` y `decision_hash` son commitments opacos; Capa 14 no reconstruye automáticamente su contenido externo.
- La calidad epistemológica de un verifier no se deriva del ledger; queda para la capa de evaluación/promoción.
- El actor de CLI es una identidad operativa declarada, no una nueva frontera mTLS o de proceso.
- El scheduler de alto nivel todavía es externo y debe respetar los puertos del servicio.
- PostgreSQL protege consistencia durable, pero no constituye aislamiento de host o sandbox de ejecución.
- La traza estructurada mejora auditabilidad, pero no garantiza por sí sola que una decisión sea verdadera.

## Fuera de alcance

- memoria y RAG;
- tools o shell arbitrario;
- MCTS/AB-MCTS abierto;
- self-modification;
- modificación de código;
- promoción de candidatos;
- entrenamiento, fine-tuning o RL online;
- auto-completion de tasks;
- modificación automática de capabilities, egress, identidad o secretos;
- experimentación sobre datos clínicos reales.

## Rollback

El rollback de código consiste en detener cualquier proceso que use `orgctl decision`, volver al commit anterior y reconstruir binaries.

No ejecutar el down de `000012` mientras existan runs o trazas que deban conservarse. El rollback destructivo requiere respaldo y aprobación humana:

```bash
pg_dump --format=custom \
  --file=/ruta/segura/explorarte-before-rama14.dump \
  "$ORG_DATABASE_URL"
```

Después puede ejecutarse `000012_create_durable_decision_graph.down.sql`. El down elimina exclusivamente las tablas, triggers y funciones de Capa 14; no elimina tasks, context snapshots, model invocations, dispatch attempts ni políticas previas.

Las suites de migración verifican up, down y reapply desde la punta real `000012`. Las suites antiguas que desmontan contexto o model runtime bajan primero `000012` para respetar sus FKs.

## Validación requerida

La rama solo queda lista para PR cuando el árbol definitivo, sin workflows o carpetas de transferencia temporales, registra código 0 para:

```bash
git diff --check 3584d9e7a2e44bbe9d953556704df5e84afd8cf3...HEAD
make verify
make build-cross
make test-decisiongraph-integration
make verify-all
```

La CI normal debe confirmar:

```text
verify ✓
postgres-final-validation ✓
```

## Relación con Rama 15

Capa 15 debe rebasarse sobre el squash-merge de Capa 14 y adaptar su `TraceSource` al `TraceRef` durable:

```text
organization_id
run_id
trace_hash
schema_version = decision-trace/v1
```

La dependencia es unidireccional:

```text
improvement/evaluation → decisiongraph

decisiongraph ↛ improvement/evaluation
```

Capa 15 puede evaluar trazas, comparar candidates y aplicar promotion gates, pero no puede modificar runs históricos ni omitir los budgets, evidence hashes o verification labels de Capa 14.
