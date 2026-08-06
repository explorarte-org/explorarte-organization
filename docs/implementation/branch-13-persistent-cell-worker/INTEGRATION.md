# Rama 13 — Persistent cell worker

## Base y alcance

- Base exacta: `c34e0f489ee84de99ba61fb89a75062752c4f065` (squash-merge de la Rama 11), rebaseada tras el squash-merge de la Rama 12 a `main`: `73b2b612e002b90f04ef3c4230a22560d65ef0ca`.
- Rama: `feat/13-persistent-cell-worker`.
- Objetivo: un proceso duradero (`orgctl model worker run`) que reemplaza la invocación manual repetida (`orgctl model invocation dispatch <id>`) por un loop persistente que selecciona invocaciones elegibles para un execution principal y las despacha, con backoff exponencial con jitter, límite de concurrencia, apagado ordenado y recuperación sin estado local tras un reinicio o caída.
- Fuera de alcance: memoria, RAG, `internal/contextengine/providers.go` (no se toca), scheduler multi-principal, selección automática entre principals, tools, completion de tareas. La numeración original de la Rama 07 (Branch 11 memoria / Branch 12 RAG) quedó obsoleta cuando las Ramas 08–12 se reasignaron a runtime de modelos, egress, dispatcher assignments, identidad y el primer adapter real (confirmado con el worker de Rama 12).

## Fuentes de verdad

No introduce documentos canónicos nuevos ni migraciones. Consume, sin modificarlos:

- Estado durable de Ramas 08–12 (`model_invocations`, `model_dispatcher_assignments`, `model_execution_principals`) — el `WorkSource` real (`internal/cellworker/postgres`) lee estas tablas directamente vía SQL; no pasa por `internal/modelegress`/`internal/modelidentity`, que solo se evalúan dentro de `Dispatch` (Rama 08–11), no en la selección de candidatos.
- `runtime.Dispatch` (`*modelruntime.DispatchService`, Ramas 08–12) es el único punto por el que este paquete toca provider, egress o identidad — nunca directamente.

No se necesitó ninguna política propia de worker/scheduler: la elegibilidad se expresa completa como una consulta SQL sobre tablas ya existentes.

## Separación de responsabilidades (`internal/cellworker`)

- **`Dispatcher`** (`interfaces.go`): puerto estrecho, idéntico en firma a `modelruntime.DispatchService.Dispatch(ctx context.Context, invocationID int64) (modelruntime.DispatchResult, error)`. `*modelruntime.DispatchService` lo satisface sin ningún shim — se pasa directo desde el bootstrap de `cmd/orgctl`.
- **`WorkSource`**: `ListEligible(ctx, principalKey string, limit int) ([]int64, error)`. La implementación real (`internal/cellworker/postgres.Store`) selecciona, como máximo `limit`, los IDs de invocación más bajos con `status IN ('requested','claimed')` cuyo `execution_principal_id` apunta a un `model_execution_principals` activo con ese `principal_key`, acotado por `organization_id`. Nunca devuelve invocaciones legacy sin pin (`execution_principal_id IS NULL`) — esas se reconcilian aparte, vía `orgctl model invocation reconcile`, no las recoge un worker persistente.
- **`Clock`**: abstrae `time.Now`/`time.Sleep`; `SystemClock` es la implementación productiva.
- **`Worker.Run(ctx)`**: loop de poll → dispatch. Backoff exponencial con jitter completo (`backoff.go`) ante ausencia de trabajo o error de `ListEligible`; reset a `MinBackoff` en cuanto vuelve a haber trabajo. Concurrencia acotada por un semáforo de tamaño `Config.Concurrency`. Apagado ordenado: al cancelar el contexto de `Run`, el loop deja de aceptar trabajo nuevo pero espera (`sync.WaitGroup`) a que todo despacho en curso termine, sobre un contexto propio desacoplado (`context.WithoutCancel` + `Config.ShutdownGrace`) para no abortar un despacho ya iniciado solo porque el proceso está cerrando.
- **Sin estado local persistente.** Elegibilidad, claims y cuota de despacho son responsabilidad exclusiva del estado durable en PostgreSQL (Ramas 08–12). Recuperación tras una caída o reinicio es, literalmente, volver a invocar el comando — probado explícitamente (`TestWorkerRecoveryAfterRestartIsStateless`).

## CLI y configuración

`orgctl model worker run` (`cmd/orgctl/worker.go`) es un proceso nuevo, explícito y separado — **no** se agrega al loop de `orgd`; `cmd/orgd` e `internal/app` quedan intactos, así que `orgd` sigue sin despachar nunca. Usa el mismo apagado por señal que `orgd` (`signal.NotifyContext(SIGINT, SIGTERM)`). Despacha como `ORG_MODEL_EXECUTION_PRINCIPAL_KEY` (Rama 10), sin leer ni parsear esa variable dos veces — la toma ya resuelta de `modelruntime.RuntimeConfig`.

```text
ORG_MODEL_WORKER_BATCH_SIZE=10
ORG_MODEL_WORKER_CONCURRENCY=1
ORG_MODEL_WORKER_MIN_BACKOFF=1s
ORG_MODEL_WORKER_MAX_BACKOFF=1m
ORG_MODEL_WORKER_SHUTDOWN_GRACE=30s
```

## Modelo de amenazas

- El worker no decide qué invocaciones son legítimas de despachar; delega enteramente en `WorkSource` (acotado por organización y estado activo del principal) y en las validaciones server-side ya existentes dentro de `Dispatch` (assignment vigente, cuota, identidad, egress). Comprometer el `WorkSource` en el peor caso amplía o reduce el conjunto de candidatos — nunca evita las validaciones de `Dispatch`.
- `Dispatcher.Dispatch` corre sobre un contexto acotado únicamente por `ShutdownGrace` tras la cancelación — un adapter que cuelgue indefinidamente bloquea el apagado ordenado hasta ese timeout, nunca más.
- Sin superficie de shell, red directa ni credenciales en `internal/cellworker` (verificado por `scripts/check-cellworker-fitness.sh`); el paquete tampoco puede nombrar un provider concreto (`openaicompat`, `deepseek`, etc.) ni importar `internal/secrets`.

## Migración

Ninguna. El worker no requiere estado propio; lee tablas ya creadas por Ramas 08–12.

## Rollback

No hay migración que revertir. Rollback operacional: dejar de ejecutar el proceso `orgctl model worker run` (por ejemplo, deteniendo el servicio/contenedor que lo corre); las invocaciones que haya dejado en `claimed` sin `send_started` quedan sujetas al mismo `orgctl model invocation reconcile` que ya cubre cualquier claim huérfano (Ramas 08–09), sin cambios de esquema.

## Tests

- `go test -race -count=5 ./internal/cellworker/...`: 15 tests contra fakes (`fakeDispatcher`, `fakeWorkSource`, `fakeClock`) — despacho de trabajo elegible, límite de concurrencia, backoff ante ausencia de trabajo/error, apagado ordenado que drena sin abandonar, recuperación sin estado, validación de `Config`/`LoadConfig`, propiedades del backoff.
- `internal/cellworker/postgres` (`//go:build integration`, `make test-cellworker-integration`): contra PostgreSQL 17 real — devuelve solo invocaciones `requested`/`claimed` pinneadas al principal activo indicado; nunca devuelve las de un principal deshabilitado; nunca devuelve invocaciones legacy sin pin; respeta `limit`; principal desconocido devuelve vacío, no error; rechaza `principalKey` vacío y `limit` no positivo.
- `scripts/check-cellworker-fitness.sh`: sin `net/http`, sin shell/subproceso, sin conocimiento de un provider concreto, sin import de `internal/secrets`, sin dependencia directa de `pgx` en el core puro, firma de `Dispatcher` exacta, sin migraciones ni cambios canónicos, `cmd/orgd`/`internal/app`/el adapter de Rama 12 intactos.
- `gofmt -l`, `go vet ./...`, `make verify` (fmt, vet, unit, race, todos los fitness scripts, cross build) limpios. `make verify-all` — todas las suites de integración en verde salvo un flake preexistente y ya conocido en `internal/modelruntime/postgres` (`claim_token_is_hashed_and_concurrent_claim_has_one_winner`, `global_concurrency_and_safe_reconcile_never_redispatch`; una carrera de Postgres sensible a CPU compartida, no relacionada con este código — pasa limpio en corrida aislada y en `ci.yml`).

## Riesgos residuales

- No hay todavía scheduler ni selección entre múltiples principals activos — deliberadamente fuera de alcance (ver Rama 10 INTEGRATION.md).
- El `ShutdownGrace` es un límite fijo por configuración; no hay cancelación cooperativa más fina dentro de un despacho en curso.
- El `WorkSource` no pagina más allá de `LIMIT`/orden por `id`; con backlog muy grande y `BatchSize` chico, invocaciones antiguas siempre se sirven primero (FIFO por ID), sin prioridad configurable.

## Siguiente rama compatible

A definir. Candidatos identificados hasta ahora: scheduler/selección multi-principal (ver Rama 10 INTEGRATION.md), memoria y RAG (renumeración pendiente, ver nota de Rama 12 sobre la Rama 07).
