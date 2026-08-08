# Rama 25 — Executive Decision Trace Wiring

## Estado

- Base: `rebase/24-executive-scoped-egress-on-r23` en `c1d15c0` (R21→R24, ya verificada).
- Rama: `feat/25-executive-decision-trace-wiring`.
- SHA final verificado: `a7872344625ada77814a769df3ec02617ed9e0b5`.
- Migración propia: ninguna — `internal/decisiongraph`'s tablas ya existen desde la migración `000012_create_durable_decision_graph`.
- No hay PR ni merge a `main` en este estado.

## Origen

Esta rama nace de una revisión de arquitectura ("Explorarte — Σ y el siguiente ladrillo") que encontró que `internal/decisiongraph` (R14), `internal/decisiongraphtrace`, `internal/evaluation` (R15) e `internal/improvement` están completamente construidos y persistidos en Postgres, pero `internal/executive` nunca creaba un `decisiongraph.Run` — no había ninguna traza real de la que `evaluation`/`improvement` pudieran partir. R25 es el primer cable de una cadena de pasos independientemente mergeables; explícitamente **no** toca retrieval de memoria/RAG, scoring de reward, ni Finanzas — esos dependen de que esta traza exista primero.

Nota de numeración: "R25" aquí no colisiona con ningún trabajo previo — se verificó que no existe `feat/25-*` ni `rebase/25-*` en `origin` antes de crear la rama.

## Objetivo

`gatedComplete` (el único punto por el que pasa toda verificación de cierre, tanto de tareas hoja como del cierre del propio CEO) ahora registra un `decisiongraph.Run` por cada `(task_id, attempt_id)` verificado:

```text
completion.Verify(task, attempt) -> CompletionResult{Verdict, Detail}
  -> DecisionRecorder.RecordAttemptDecision
       -> decisiongraph.CreateRun        (policy hash reusado de contextengine, budget de executive.Limits)
       -> decisiongraph.AppendGraph      (goal -> candidate_action -> decision, 3 nodos)
       -> decisiongraph.StartRun
       -> decisiongraph.RecordVerification   (label derivado del verdict)
       -> decisiongraph.RecordTerminalDecision  (solo si verified/inferred)
```

El grafo es deliberadamente mínimo: un nodo `goal` (la tarea), un nodo `candidate_action` (el resultado del intento) y un nodo `decision` (el veredicto). El orquestador no descompone en hipótesis/múltiples candidatos hoy, así que registrar una descomposición más rica habría sido evidencia fabricada, no real.

Reutilización explícita en vez de invención:
- `ReasoningPolicySchemaVersion`/`ReasoningPolicyHash` se leen del `CanonicalBundle` que `contextengine/canonical.Provider` ya carga y hashea para `docs/canonical/reasoning-assurance.yaml` — no se hashea el archivo una segunda vez.
- `BudgetLimits.MaxModelCalls`/`MaxReplans`/`MaxOutputTokens`/`Deadline` se derivan de `executive.Limits`, que ya existía. Los campos sin análogo (`MaxNodes`, `MaxDepth`, `MaxParallelNodes`, `MaxInputTokens`, `MaxVerifications`) son constantes provisionales nuevas en el propio adaptador, documentadas como tales — no se promovieron a política canónica porque nada hoy lo justifica.

## Bug encontrado y corregido: `AppendGraph` nunca devolvía el mapeo de IDs de nodo

`decision_graph_nodes.id` es una columna de identidad global (no reiniciada por `run`), distinta del `logical_node_id` que el llamador elige. El store de Postgres ya calculaba internamente el mapeo `logical_id -> id_real` al insertar los nodos, pero `AppendGraph` nunca lo devolvía — `GraphVersion` solo traía metadata agregada (conteo, hash, profundidad). Cualquier llamador que intentara `RecordVerification`/`RecordTerminalDecision` después de `AppendGraph` no tenía forma de saber a qué IDs reales dirigirse, salvo por casualidad (en el primer `Run` de una base de datos recién truncada, la secuencia de identidad global empieza en 1, así que los IDs lógicos 1/2/3 coinciden por accidente con los reales — pero el segundo `Run` ya no).

Esto se descubrió con el primer corrido real contra Postgres 17 (no con fakes): `TestR23PostgreSQLProjectsWorkerEvidenceAndClosesDAGRace` pasaba su primer `resume` y fallaba el segundo con `decision graph record not found: verification node target`.

Fix: se agregó `GraphVersion.NodeIDs map[int64]int64` (aditivo, no rompe compatibilidad — los llamadores que solo usan `AppendGraph` para anexar y nunca verifican pueden ignorar el campo), poblado por `internal/decisiongraph/postgres/store.go` a partir del mapeo que ya calculaba. El adaptador de esta rama lo usa para resolver los IDs reales antes de llamar `RecordVerification`/`RecordTerminalDecision`.

Dos bugs más chicos encontrados en el mismo ciclo de verificación real:
- `LogicalName` en el `CanonicalBundle` de `contextengine/canonical` viene prefijado con `"docs/canonical/"` (`"docs/canonical/reasoning-assurance.yaml"`, no `"reasoning-assurance.yaml"`) — el adaptador buscaba el nombre sin prefijo y fallaba con "not found in canonical bundle".
- `VerificationRecord.ReasonCodes` nil se serializa a JSON `null`, y `decision_verifications.reason_codes` tiene `CHECK (jsonb_typeof(reason_codes) = 'array')` — Postgres rechazaba el insert. Fix: `ReasonCodes: []string{}` explícito.

Ninguno de los tres bugs era detectable con fakes; los tres aparecieron solo al correr contra PostgreSQL 17 real.

## Tests

Unitarios + build (verde):

```bash
gofmt -l .
go vet ./...
go build ./...
go test ./internal/executive/... ./internal/decisiongraph/...
go test -race -short ./internal/executive/... ./internal/decisiongraph/...
bash scripts/check-executive-fitness.sh
```

PostgreSQL 17 real (contenedor `r23-integration-pg`, reutilizado de la sesión de R23/R24):

```bash
export ORG_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:35432/explorarte_test?sslmode=disable'
go test -tags=integration ./internal/executive/... -v
```

Las 24 pruebas del paquete pasan, incluyendo la nueva `TestExecutivePostgreSQL17RecordsDecisionGraphTraceForEveryAttempt`, que corre una tarea real de principio a fin y verifica directamente contra las tablas (no solo "no hubo error") que las 6 llamadas a `completion.Verify` de esa corrida dejaron exactamente 6 `decision_graph_runs` en `succeeded`, cada uno con 3 nodos, un `decision_records` con `verification_label='verified'`, y `decision_node_id ≠ selected_candidate_node_id`. Verificado también manualmente con `orgctl decision trace <run_id>` contra las filas reales.

**Nota sobre fallos preexistentes, no causados por esta rama** (confirmado corriendo `git stash` contra la base `c1d15c0` sin mis cambios, con el mismo resultado):
- `go vet -tags=integration ./...`: `internal/contextengine/postgres/integration_test.go:493` referencia `contextengine.ProjectContext`, que no existe — preexistente.
- `internal/decisiongraph/postgres` y `internal/decisiongraphtrace` (sus tests de integración): `current migration=18, want 17` — aserción de conteo de migraciones desactualizada desde que se agregó la migración 000018, preexistente.
- `internal/completion/postgres`: `duplicate key value violates unique constraint "staging_artifacts_digest_key"` — preexistente, no relacionado con `decisiongraph`.
- `bash scripts/check-decisiongraph-fitness.sh`: `unauthorized canonical change: docs/canonical/model-egress-policy.yaml` — preexistente; ese archivo lo cambió R24 legítimamente y el script no tiene una allowlist para esa rama.

Ninguno de estos cuatro toca código que esta rama modificó (`internal/executive`, `internal/executive/runtimeadapter`, `internal/decisiongraph/records.go`, `internal/decisiongraph/postgres/store.go`).

## Riesgos residuales

- El grafo registrado es intencionalmente mínimo (3 nodos). Si una rama futura necesita capturar pasos de razonamiento intermedios reales, hace falta que el orquestador primero produzca esos pasos — no tiene sentido enriquecer el grafo antes de eso.
- `evaluation`/`improvement` siguen sin consumir estas trazas automáticamente — esta rama solo las produce. Conectar el consumo es el paso siguiente de la cadena (ver el documento de arquitectura, sección N).
- Las constantes de `BudgetLimits` sin análogo en `executive.Limits` (`MaxNodes=32`, `MaxDepth=8`, `MaxParallelNodes=4`, `MaxInputTokens=200000`, `MaxVerifications=4`) son un punto de partida razonable, no una política revisada — viven en `internal/executive/runtimeadapter/decisions.go`, no en un YAML canónico, a propósito.

## Comandos exactos de reproducción

```bash
git fetch origin
git checkout feat/25-executive-decision-trace-wiring

gofmt -l .
go vet ./...
go build ./...
go test ./internal/executive/... ./internal/decisiongraph/...
go test -race -short ./internal/executive/... ./internal/decisiongraph/...
bash scripts/check-executive-fitness.sh

export ORG_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:35432/explorarte_test?sslmode=disable'
go test -tags=integration ./internal/executive/... -v -count=1
```

No se abrió PR. No se hizo merge a `main`.
