# Rama 26 — Executive Closure Lesson Job

## Estado

- Base: `feat/25-executive-decision-trace-wiring` en `a45382f` (ya verificada, sin merge a `main`).
- Rama: `feat/26-executive-closure-lesson-job`.
- SHA final verificado: `36a8731`.
- Migración propia: ninguna.
- No hay PR ni merge a `main` en este estado.

## Objetivo

Rama 25 hizo que `internal/executive` dejara un `decisiongraph.Run` durable por cada intento de tarea verificado. Nadie leía esas trazas. Esta rama construye el consumidor: dado un `run_id`, decide si contiene una lección que valga la pena revisar, y si es así propone un `memory.Entry` candidato — a través de la misma gobernanza (`memory.Manager`, el flujo candidate→review de `memory-policy.yaml`, los permisos de `capability-matrix.yaml`) que ya existe para cualquier otra fuente de memoria organizacional.

```text
orgctl postrun propose-lesson <run-id>
  -> decisiongraphtrace.Store.RunSummary(run_id)         [task_id, attempt_id, trace_hash, terminal_at]
  -> completion.Service.Verify(task_id, attempt_id)      [re-derivación real e independiente]
     -> Verdict == pass?  -> skipped_pass (no hay problema que registrar)
     -> RoleResolver.AssignedRoleID(task_id)
     -> memory.Manager.Propose(...)                      [misma autorización, mismo dedup que cualquier otra memoria]
        -> permiso denegado (rol sin memory.propose)  -> skipped_role_not_eligible
        -> ya procesado (mismo idempotency key)        -> reused
        -> nuevo                                        -> proposed
```

## Decisiones de diseño (cada una verificada leyendo el código real, no asumida)

1. **No usa `internal/evaluation.Service`.** Ese paquete está pensado para comparar baseline vs. candidato contra un `EvaluationSuite` con casos ponderados — no para "esta traza suelta, ¿tiene una lección?". Forzar cada intento por un suite/caso fabricado habría sido exactamente el tipo de evidencia manufacturada que `evaluation.SuiteComparisonResult.Validate` existe para rechazar. La señal de veredicto que este job necesita ya existe, directo de `internal/completion`.
2. **Re-verifica en vez de intentar recuperar el razonamiento original.** `internal/decisiongraph` nunca guarda el payload crudo de un nodo ni razonamiento privado (`reasoning-assurance.yaml`: `store_private_chain_of_thought: false`) — la traza solo trae hashes. `completion.Service.Verify` es una re-derivación independiente y determinista (mismo patrón que ya usa `shadowverifier`); llamarla de nuevo reproduce un `VerificationResult{Verdict, Obligations}` real, con `ObligationResult.Detail` genuino por obligación — no inventado.
3. **Solo propone cuando el veredicto no es `pass`.** `memory.Entry` exige `Problem`/`Correction` no vacíos — ese esquema está pensado para "algo que corregir"; inventar un "problema" para una corrida limpia habría sido evidencia deshonesta.
4. **El rol decide, no la lógica de este job.** `capability-matrix.yaml`: `memory.propose` está otorgado a `department_leadership` y `specialist`, **no** a `executive` (la clase de `empresa/ceo`). Este job propone como el rol real que ejecutó el intento (`task.AssignedRoleID`), vía `memory.Manager.Propose`, que ya llama al gate de autorización real. Cuando ese rol es el cierre del propio CEO, `Propose` devuelve `authorization.ErrCapabilityDenied` — tratado como "no elegible", no como fallo. No es un workaround: es la matriz de capacidades haciendo exactamente lo que está diseñada para hacer.
5. **Idempotente por construcción** — `memory.Manager.Propose` ya deduplica vía `IdempotencyKey`. Se usa el `run_id` como idempotency key y (determinísticamente) como `Entry.ID`.

## Bug encontrado y corregido: `AttestedAt` con reloj de pared rompía la idempotencia

`memory.Entry.CanonicalHash()` incluye el struct `Admission` completo (comentario del propio código: excluye timestamps de ciclo de vida, pero `Admission.AttestedAt` no es uno de esos — es parte del contenido). La primera versión de este job calculaba `AttestedAt` con `time.Now()` en cada llamada — así que procesar el mismo `run_id` dos veces (exactamente el caso que la sección de idempotencia del test ejercita) producía un hash canónico distinto bajo la misma `IdempotencyKey`, y Postgres rechazaba la segunda llamada con `memory conflict: idempotency key already commits different memory content`.

Esto lo encontró el propio test de integración real, no una revisión de código — el mismo patrón que R23/R24/R25 ya establecieron.

Fix: `decisiongraphtrace.RunSummary` ahora también expone `TerminalAt` (de `decision_graph_runs.terminal_at`, `NOT NULL` para cualquier run en `succeeded`) — un timestamp real, estable y ligado a la propia corrida, no fabricado. `postrun.Service` usa ese valor para `AttestedAt` en vez de `time.Now()`. Reprocesar el mismo `run_id` produce ahora exactamente el mismo hash canónico, y el segundo intento resuelve limpio como `reused`.

## Tests

Unitarios (fakes) + build:

```bash
gofmt -l .
go vet ./...
go build ./...
go test ./internal/executive/postrun/... ./internal/decisiongraphtrace/... ./cmd/orgctl/...
go test -race -short ./internal/executive/... ./internal/decisiongraphtrace/... ./internal/decisiongraph/... ./cmd/orgctl/...
```

Los tests unitarios de `internal/executive/postrun/service_test.go` cubren los cuatro desenlaces (`proposed`, `reused`, `skipped_pass`, `skipped_role_not_eligible`) con fakes, incluyendo una aserción explícita de que una obligación *verificada* nunca aparece en el texto del `Problem` propuesto (solo las contradichas/desconocidas).

PostgreSQL 17 real (contenedor `r23-integration-pg`, puerto `35432`, reutilizado de sesiones anteriores):

```bash
export ORG_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:35432/explorarte_test?sslmode=disable'
go test -tags=integration ./internal/executive/postrun -count=1 -v
```

`TestPostrunProcessRunAgainstRealPostgres` construye tres escenarios con datos reales (tarea + requirement + evidencia insertados directamente, sin CEO orquestando — mismo patrón que ya usan `internal/completion/postgres/integration_test.go` y `internal/decisiongraphtrace/integration_test.go`), produce el `decisiongraph.Run` real vía el mismo adaptador de producción de Rama 25 (`runtimeadapter.DecisionGraph.RecordAttemptDecision`), y verifica contra las tablas reales (`organizational_memory_versions`/`organizational_memory_entries`):

- Rol `ingenieria_ia/qa`, requirement sin evidencia → veredicto real de completitud no-pass → candidato propuesto, con `role_id`/`source_run_id`/`status='candidate'` correctos; segunda llamada al mismo `run_id` → `reused`, sin fila duplicada.
- Rol `ingenieria_ia/qa`, requirement satisfecho con evidencia real → veredicto real `pass` → `skipped_pass`, cero filas en `organizational_memory_versions`.
- Rol `empresa/ceo`, requirement sin evidencia → veredicto real no-pass, pero `memory.propose` no está otorgado a la clase `executive` → `skipped_role_not_eligible`, cero filas.

Smoke manual con el binario real:

```bash
go build -o /tmp/orgctl ./cmd/orgctl
export ORG_DATABASE_URL='postgres://postgres:postgres@localhost:35432/explorarte_test?sslmode=disable'
export ORG_ENVIRONMENT=test
/tmp/orgctl postrun propose-lesson --json <run-id>
```

Corrido contra un `run_id` real producido por `TestExecutivePostgreSQL17EndToEndAndRestart` (intento de `ingenieria_ia/qa` con veredicto real `pass`) — devolvió `{"Kind":"skipped_pass"}` correctamente, sin fila creada.

**Nota sobre el pitfall de invocaciones combinadas (ya documentado en R24):** `go test -tags=integration ./internal/executive/...` con comodín arrastra también `internal/executive/postrun` (subpaquete) a la MISMA invocación combinada, y produce el mismo `SQLSTATE 40001` espurio que R24 ya documentó — no es una regresión de esta rama (confirmado corriendo `internal/executive/postrun` solo, inmediatamente después, en verde). Reproducir siempre con rutas de paquete exactas, una invocación por paquete:

```bash
go test -tags=integration ./internal/executive -count=1
go test -tags=integration ./internal/executive/postrun -count=1
```

**Fallos preexistentes, no causados por esta rama** (mismo patrón de verificación que R25: confirmado que no son nuevos):
- `bash scripts/check-executive-fitness.sh` ahora falla con `"R14 internals changed by R23"`. Ese script codifica invariantes específicos de la era R23 (`internal/decisiongraph` no debía tocarse, comparado contra el tip de `main` previo a R23) — Rama 25 ya extendió legítimamente `internal/decisiongraph/records.go`/`postgres/store.go` (el fix de `GraphVersion.NodeIDs`, documentado en su propio INTEGRATION.md) como parte de su alcance real, y esta rama hereda esa extensión. El script no se actualizó para reconocer R25/R26 como ramas posteriores autorizadas a tocar ese paquete — es una condición preexistente desde R25, no algo que esta rama causó, y no es mío decidir unilateralmente cómo debería actualizarse ese script.
- `internal/decisiongraphtrace`/`internal/decisiongraph/postgres` (sus propios tests de integración): `current migration=18, want 17` — ya documentado como preexistente en el INTEGRATION.md de R25.

## Riesgos residuales

- El texto de `Correction` es siempre el mismo placeholder honesto ("pendiente de revisión humana") — a propósito: este job observa y propone, no redacta la corrección. Un humano la completa antes de `approved`.
- No hay batch/poller automático — cada `run_id` se procesa a mano vía `orgctl postrun propose-lesson`, según lo confirmado con el usuario. Si el volumen de corridas reales lo justifica, ese es el siguiente ladrillo, no este.
- La categoría de la memoria propuesta es fija (`completion_verification`) — no hay taxonomía de categorías todavía; no hay evidencia hoy de que se necesite una.

## Comandos exactos de reproducción

```bash
git fetch origin
git checkout feat/26-executive-closure-lesson-job

gofmt -l .
go vet ./...
go build ./...
go test ./internal/executive/postrun/... ./internal/decisiongraphtrace/... ./cmd/orgctl/...
go test -race -short ./internal/executive/... ./internal/decisiongraphtrace/... ./internal/decisiongraph/... ./cmd/orgctl/...

export ORG_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:35432/explorarte_test?sslmode=disable'
go test -tags=integration ./internal/executive -count=1
go test -tags=integration ./internal/executive/postrun -count=1 -v
```

No se abrió PR. No se hizo merge a `main`.
