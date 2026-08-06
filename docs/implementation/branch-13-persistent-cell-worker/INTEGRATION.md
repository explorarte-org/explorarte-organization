# Rama 13 — Persistent cell worker

## Estado de este documento

Parcial y en curso. Documenta el core aislado del worker persistente (`internal/cellworker`), construido y verificado íntegramente contra fakes. El wiring productivo (Postgres `WorkSource`, migración 000011... 000012, composition root en `cmd/orgd`/`internal/app`, `Makefile`, `ci.yml`) queda explícitamente fuera hasta que la Rama 12 (`feat/12-model-provider-adapter`) se fusione a `main`, por acuerdo explícito con ese worker: un worker persistente contra un adapter todavía no definido no tiene valor real, y ambas ramas comparten la responsabilidad de no pisarse archivos mientras avanzan en paralelo. Este documento se completa (SHA final, migración, rollback, siguiente rama) recién en el PR final.

## Base y alcance

- Base exacta: `c34e0f489ee84de99ba61fb89a75062752c4f065` (squash-merge de la Rama 11 a `main`).
- Rama: `feat/13-persistent-cell-worker`.
- Objetivo: introducir un proceso duradero que reemplace la invocación manual (`orgctl model invocation dispatch <id>`) por un loop persistente que selecciona trabajo elegible para un execution principal y lo despacha, con backoff, límite de concurrencia, apagado ordenado y recuperación sin estado local tras un reinicio o caída.
- Fuera de alcance en esta rama: memoria, RAG, `internal/contextengine/providers.go` (no se toca), scheduler multi-principal, selección automática entre principals, tools, completion de tareas. Confirmado con el worker de Rama 12: la numeración original de la Rama 07 (Branch 11 memoria / Branch 12 RAG) quedó obsoleta cuando las Ramas 08–11 se reasignaron a runtime de modelos, egress, dispatcher assignments e identidad.

## Fuentes de verdad

No introduce documentos canónicos nuevos. Consume, sin modificarlos:

- `docs/canonical/model-routing.yaml` y `docs/canonical/model-egress-policy.yaml` — indirectamente, a través del `Dispatcher` (Rama 12), nunca leídos directamente por este paquete.
- Estado durable de Ramas 08–11 (`model_invocations`, `model_dispatch_attempts`, `model_dispatcher_assignments`, `model_execution_principals`, identidad criptográfica) — el futuro `WorkSource` real consulta contra ese estado, pero el core de esta rama no lo toca todavía.

Si en el wiring final se necesita una política propia de worker/scheduler, se propone solo si no cabe en un documento canónico existente, y se muestra antes de crearla (acordado con Rama 12).

## Separación de responsabilidades (`internal/cellworker`)

- **`Dispatcher`** (`interfaces.go`): puerto estrecho, idéntico en firma a `modelruntime.DispatchService.Dispatch(ctx context.Context, invocationID int64) (modelruntime.DispatchResult, error)`. El worker nunca importa el adapter concreto, credenciales, egress ni identidad — todo eso vive detrás de este puerto, propiedad de Rama 12.
- **`WorkSource`**: `ListEligible(ctx, principalKey string, limit int) ([]int64, error)`. Selecciona invocaciones elegibles para el principal dado. La implementación real (consulta Postgres sobre `model_invocations`/`model_dispatcher_assignments` en estado `requested`/`claimed` con assignment vigente) se añade en el wiring final; hoy solo existen fakes de prueba.
- **`Clock`**: abstrae `time.Now`/`time.Sleep` para que el loop de backoff sea determinístico en tests; `SystemClock` es la implementación productiva.
- **`Worker.Run(ctx)`**: loop de poll → dispatch. Backoff exponencial con jitter completo (`backoff.go`) cuando no hay trabajo elegible o `ListEligible` falla; se resetea a `MinBackoff` en cuanto vuelve a haber trabajo. Concurrencia acotada por un semáforo de tamaño `Config.Concurrency`. Apagado ordenado: al cancelarse el contexto de `Run`, el loop deja de aceptar trabajo nuevo pero espera (`sync.WaitGroup`) a que todo despacho en curso termine, usando un contexto propio desacoplado (`context.WithoutCancel` + `Config.ShutdownGrace`) para que un despacho ya iniciado no se aborte a mitad de camino solo porque el proceso está cerrando.
- **Sin estado local persistente.** El worker no guarda nada entre invocaciones de `Run`: la elegibilidad, los claims y la cuota de despacho son responsabilidad exclusiva del estado durable en PostgreSQL (Ramas 08–11). Recuperación tras una caída o reinicio es, literalmente, volver a llamar `Run` — se probó explícitamente (`TestWorkerRecoveryAfterRestartIsStateless`) construyendo un `Worker` nuevo contra el mismo `WorkSource`/`Dispatcher` y verificando que sigue progresando sin ningún paso de recuperación especial.

## Modelo de amenazas (parcial)

- El worker no decide qué invocaciones son legítimas de despachar; delega esa decisión enteramente a `WorkSource` y a las validaciones server-side ya existentes (assignment vigente, cuota, identidad). Un `WorkSource` fake o comprometido en tests no puede afectar producción porque el wiring productivo aún no existe.
- `Dispatcher.Dispatch` recibe un contexto con `ShutdownGrace` como único límite de tiempo tras la cancelación — un adapter que cuelgue indefinidamente bloquea el apagado ordenado hasta ese timeout, nunca más. Pendiente de revisar junto con los timeouts de transporte de Rama 12 cuando se conecten.
- No hay superficie de ejecución de shell, red directa ni credenciales en este paquete — ninguna de las prohibiciones ya establecidas por Ramas 08–12 se toca ni se relaja.

## Migración

Pendiente. Reservada `000012` para el wiring final (tras Rama 12, que usa `000011`), a definir cuando se agregue el `WorkSource` real y cualquier tabla de soporte necesaria (si la hiciera falta; el worker en sí no requiere estado propio).

## Tests

`go test -race -count=5 ./internal/cellworker/...`: 15 tests, todos contra fakes (`fakeDispatcher`, `fakeWorkSource`, `fakeClock`), cubriendo: despacho de trabajo elegible, respeto estricto del límite de concurrencia, backoff ante ausencia de trabajo y ante error de `ListEligible`, apagado ordenado que drena despacho en curso sin abandonarlo, recuperación sin estado tras un "reinicio" simulado, límites y validación de `Config`, y las propiedades del backoff (rango, cota máxima, reset) de forma aislada. `gofmt -l`, `go vet` y `go build ./...` limpios.

## Riesgos residuales

- El `WorkSource` real (consulta Postgres) no existe todavía; su forma exacta depende del esquema final de Rama 12 (`model_provider_requests`, `model_provider_outcomes`) y de cómo se exprese "elegible para este principal" contra `model_dispatcher_assignments`.
- No hay todavía scheduler ni selección entre múltiples principals activos — deliberadamente fuera de alcance, ver Rama 10 INTEGRATION.md.
- El `ShutdownGrace` es un límite fijo por configuración; no hay cancelación cooperativa más fina dentro de un despacho en curso.

## Siguiente rama compatible

A definir tras cerrar el wiring de esta rama. Candidatos ya identificados en runs previas: scheduler/selección multi-principal (Rama 10), memoria y RAG (renumeración pendiente, ver nota de Rama 12 sobre la Rama 07).
