# P0_FIX_AUDIT.md

Auditoría de causa raíz de los bloqueantes P0 descubiertos en el canario de curación DeepSeek (r7/r8). **Solo auditoría y diseño — no se implementó ningún cambio de código todavía.** No se ejecuta r9 hasta aprobación explícita.

---

## A. HEAD / branch / dirty state / migration tip

- HEAD: `7cd60785683cb197b3941974d1727311447af4fa`
- Branch: `feat/bootstrap-closure-observability-prolog` (no pusheado a GitHub remoto — no asumido actualizado)
- Working tree dirty (sin cambios desde el último reporte, nada tocado en esta auditoría):
  ```
  M cmd/orgctl/corpus.go
  M compose.yaml
  M internal/modelruntime/adapter/deepseek/adapter.go
  M internal/modelruntime/adapter/deepseek/adapter_test.go
  ?? cmd/orgctl/corpus_cluster_cli.go
  ?? cmd/orgctl/corpus_enrich_cli.go
  ?? cmd/orgctl/corpus_semantic_cli.go
  ?? docs/reports/  (CURATION_CANARY_REPORT.md, nuevo de esta sesión)
  ?? internal/corpuscluster/
  ?? internal/corpuscuration/
  ?? internal/corpusenrich/
  ?? internal/corpussemantic/
  ```
- Migration tip: `000036_add_chunk_provenance` (sin cambios).

---

## B. Reconciliación exacta: 137 invocaciones internas vs 113 requests del provider

Confirmado contra `model_invocations` (hoy, UTC):

- **137** invocaciones internas con `provider_id='deepseek'`
- **16** `succeeded`
- **121** `failed`
- DeepSeek dashboard: **113** requests reales, $1.36, 9,377,828 tokens

**Gap de 137 vs 113 = 24 invocaciones que NO deberían contar como "request real al provider".** Estas corresponden a la fase `AdapterFailureBeforeRequest` del adapter (`internal/modelruntime/adapter/deepseek/adapter.go`, líneas 129/135/139/148/152/164) — casos donde el request nunca salió del host: `provider_scope_invalid`, `circuit_open`, `credential_unavailable`, `request_encoding_failed`, `request_build_failed`. Estas SÍ deben tener costo $0 (correcto que no aparezcan en el dashboard de DeepSeek). No se verificó el conteo exacto de estas 24 por código de error en esta pasada (posible siguiente paso de verificación, no bloqueante para el diseño del fix).

Las **113 restantes SÍ llegaron al provider** — de esas, 16 tuvieron éxito y accounting correcto; **las otras ~97 son las que generan el problema central de este documento**.

---

## C. Causa raíz en código: por qué se pierde el usage post-provider

Encontrada con precisión exacta, en `internal/modelruntime/dispatch_service.go`.

El reservo de CostGate se libera automáticamente vía un `defer` (línea 472-475):

```go
reservationApplied, reservationSettled := false, false
...
defer func() {
    if reservationApplied && !reservationSettled {
        _ = s.costGate.Release(context.WithoutCancel(ctx), costReservation, s.clock.Now())
    }
}()
```

`reservationSettled` se marca `true` explícitamente en las ramas que SÍ deben preservar la reserva: `AdapterFailureAmbiguous` (línea 561), el camino de cancelación confirmada, el fallback "ambiguous_external_outcome" (línea 603), tras `MarkResponseReceived` (línea 644), y en `FailAfterResponse`/normalización (línea 652).

**El bug**: la rama `AdapterFailureResponseReceived` (línea 550-554, que llama a `s.store.RejectProviderResponse(...)`) **nunca marca `reservationSettled = true`**. Por construcción, esta fase es la prueba MÁS fuerte de que el provider fue alcanzado (headers y body ya se recibieron) — y sin embargo es exactamente la rama que, por omisión del flag, cae en el `defer` y se libera como si el request nunca hubiera salido. Esto es lo opuesto a la intención documentada en el propio comentario del código dos ramas más abajo ("the provider may have processed and billed this call — releasing the reservation here would hand back money that was possibly already spent").

`AdapterFailureResponseReceived` cubre, en el adapter DeepSeek, estos error_codes (todos con response HTTP ya recibida):
```
response_read_failed
response_json_invalid
response_choice_count_invalid
response_content_invalid
tool_call_name_missing
response_content_filtered
response_truncated_empty
+ errores HTTP no-2xx del provider (clase/código dinámico vía parseProviderError)
```

Esto se confirmó empíricamente contra la DB: las invocaciones 129, 132, 138, 139, 140, 141, 144, 145, 147 (todas `response_truncated_empty`) tienen un evento `released` en `provider_wallet_events` — tratadas como gratis, incorrectamente.

**Un segundo bug, distinto, en el camino de `response_normalization_failed`**: ese error_code NO viene del adapter — viene del propio `dispatch_service.go`, en un paso posterior (`s.normalizer.Normalize(...)` fallando DESPUÉS de que `MarkResponseReceived` ya tuvo éxito), vía `FailAfterResponse` (línea ~628). Este camino SÍ marca `reservationSettled = true` correctamente (línea 652) — la reserva NO se libera. Pero tampoco se resuelve nunca a ningún estado financiero explícito: queda `reserved` para siempre, sin ruta de reconciliación. Confirmado empíricamente: invocaciones 133 y 148 tienen `reserved` sin `released` ni `committed`.

**Conclusión de causa raíz**: no es un solo bug, son dos comportamientos distintos, ambos incorrectos respecto al principio pedido:
1. `AdapterFailureResponseReceived` → auto-liberado como si fuera gratis (falso: el provider sí respondió).
2. `FailAfterResponse`/normalización → atascado en `reserved` para siempre, sin conversión a un estado explícito reconciliable.

---

## D. Lista exacta de error paths afectados

| error_code / fase | origen en código | reservationSettled hoy | comportamiento hoy | correcto según principio pedido |
|---|---|---|---|---|
| `credential_unavailable`, `circuit_open`, `provider_scope_invalid`, `request_encoding_failed`, `request_build_failed` | adapter, `AdapterFailureBeforeRequest` | no seteado (release por defecto) | RELEASE | ✅ correcto — `NOT_SENT` |
| `response_read_failed` | adapter, `AdapterFailureResponseReceived` | **no seteado** | RELEASE (bug) | ❌ debe ser `ESTIMATED_PENDING_RECONCILIATION` |
| `response_truncated_empty` | adapter, `AdapterFailureResponseReceived` | **no seteado** | RELEASE (bug, confirmado en DB) | ❌ igual |
| `response_json_invalid` | adapter, `AdapterFailureResponseReceived` | **no seteado** | RELEASE (bug) | ❌ igual |
| `response_choice_count_invalid` | adapter, `AdapterFailureResponseReceived` | **no seteado** | RELEASE (bug) | ❌ igual |
| `response_content_invalid` | adapter, `AdapterFailureResponseReceived` | **no seteado** | RELEASE (bug) | ❌ igual |
| `tool_call_name_missing` | adapter, `AdapterFailureResponseReceived` | **no seteado** | RELEASE (bug) | ❌ igual |
| `response_content_filtered` | adapter, `AdapterFailureResponseReceived` | **no seteado** | RELEASE (bug) | ❌ igual |
| HTTP no-2xx del provider | adapter, `AdapterFailureResponseReceived` | **no seteado** | RELEASE (bug) | ❌ igual |
| `response_normalization_failed` | dispatch_service, `FailAfterResponse` | sí (línea 652) | atascado en `reserved` para siempre | ❌ debe convertirse a `ESTIMATED_PENDING_RECONCILIATION` explícito, no quedar ambiguo |
| timeout/transport ambiguo | adapter, `AdapterFailureAmbiguous` | sí (línea 561) | queda `reserved`/ambiguous | ✅ ya correcto en intención, mismo gap de "nunca se resuelve" que el anterior |
| `adapter_error_after_send` / `provider_timeout_after_send` | dispatch_service, fallback tras adapterErr no clasificado | sí (línea 603) | queda ambiguous | ✅ ya correcto en intención, mismo gap |
| CostGate rejection (presupuesto insuficiente) | `s.costGate.Reserve` fallando antes del dispatch | n/a (nunca se reserva) | no aplica reserva | ✅ correcto, no llega al provider |
| context_drift / authorization rejection | capas anteriores al dispatch | n/a | no llega a reservar/dispatch | ✅ correcto, no llega al provider |

---

## E. Cómo distinguir pre-provider vs provider-reaching (ya existe, subutilizado)

La distinción **ya existe en el dominio** como `modelruntime.AdapterError.Phase`, con tres valores: `AdapterFailureBeforeRequest`, `AdapterFailureResponseReceived`, `AdapterFailureAmbiguous`. Esto es exactamente el mapeo que se necesita:

- `AdapterFailureBeforeRequest` → `provider_reached = false` → NOT_SENT → release correcto.
- `AdapterFailureResponseReceived` → `provider_reached = true`, con o sin `usage` parseable → hoy tratado mal (ver C/D).
- `AdapterFailureAmbiguous` → `provider_reached = unknown` (timeout/transport) → ya se mantiene reservado, pero sin resolución final.

No hace falta inventar un mecanismo nuevo de clasificación — el gap es que el **manejo financiero (el `defer`/`reservationSettled`) no está alineado 1:1 con esta clasificación ya existente.**

---

## F. Modelo de estados financieros propuesto

Extender (no reemplazar) el vocabulario existente en `internal/modelruntime`/`internal/costledger`, agregando un campo explícito de **cost provenance** y un estado financiero intermedio:

```go
type CostProvenance string
const (
    CostActualProviderReported CostProvenance = "actual_provider_reported"
    CostEstimatedLocally       CostProvenance = "estimated_locally"
    CostReconciledProvider     CostProvenance = "reconciled_provider"
    CostUnknown                CostProvenance = "unknown"
)

type FinancialOutcome string
const (
    FinancialActual                       FinancialOutcome = "actual"
    FinancialEstimatedPendingReconciliation FinancialOutcome = "estimated_pending_reconciliation"
    FinancialReconciled                   FinancialOutcome = "reconciled"
    FinancialReleased                     FinancialOutcome = "released_not_sent"
)
```

Regla de mapeo (reemplaza el `defer`-release-por-omisión actual):

```
AdapterFailureBeforeRequest        -> FinancialReleased (release real, sin cambios de comportamiento)
AdapterFailureResponseReceived
    + usage parseable en el raw body -> FinancialActual (commit con el usage real, aunque el schema/JSON de negocio haya fallado después)
    + usage NO parseable             -> FinancialEstimatedPendingReconciliation (conservative estimate, ver G)
FailAfterResponse (normalización)    -> FinancialEstimatedPendingReconciliation (ya tenemos el raw usage de la respuesta adapter — normalmente SÍ hay usage disponible aquí, ya que la falla es de nuestro parser de negocio, no del adapter; debe promoverse a FinancialActual si el usage del adapter está presente)
AdapterFailureAmbiguous              -> FinancialEstimatedPendingReconciliation (transport ambiguo, sin certeza)
```

Nota importante encontrada en el propio código: `RawResponse` (lo que el adapter retorna incluso en fallos post-JSON-envelope) probablemente YA contiene el `chatUsage` parseado del envelope HTTP externo en algunos casos — específicamente, `response_choice_count_invalid`, `response_content_invalid`, `tool_call_name_missing`, `response_content_filtered` ocurren DESPUÉS de que `json.Unmarshal(responseBody, &decoded)` ya tuvo éxito (línea ~199 del adapter), lo que significa que `decoded.Usage` (PromptTokens/CompletionTokens) **ya está disponible en memoria en el momento del error** — simplemente no se está propagando hacia el `ProviderOutcome`/`AdapterError` en esos casos. Esto es una oportunidad real de mover varios de estos casos de "estimado" a "actual conocido" sin esperar ninguna reconciliación externa — el dato ya lo tenemos, solo falta no descartarlo.

`response_truncated_empty` y `response_read_failed` sí son genuinamente casos donde el usage puede no estar disponible (respuesta truncada a nivel de bytes, antes de completar el JSON) — esos quedan como `FinancialEstimatedPendingReconciliation` real.

---

## G. ¿Necesita migración?

Sí, mínima y aditiva:

1. `provider_wallet_events.kind` — hoy `reserved|committed|released` (inferido del uso observado). Agregar un cuarto valor: `estimated_pending_reconciliation` (o extender `committed` con una columna `provenance`/`is_estimated boolean` — a decidir en el diseño detallado, cualquiera de las dos formas es aditiva y no rompe filas existentes).
2. `model_invocation_usage` — agregar columnas nullable: `cache_hit_tokens`, `cache_miss_tokens` (punto 17/Q), y posiblemente `usage_provenance` (`actual_provider_reported` / `estimated_locally`).
3. Nueva tabla ligera opcional `provider_usage_reconciliation` para el punto de agregación diaria (ver H/Q): `date, provider_id, internal_estimated_total_usd, provider_reported_total_usd, delta_abs, delta_ratio, reconciled_at, source`.
4. Ninguna migración toca `rag_knowledge_chunks`, autorización, execution identity, ni ningún esquema fuera de `internal/modelruntime`/`internal/costledger`.

No se diseñó el DDL exacto todavía — se deja para la fase de implementación, después de aprobación.

---

## H. Cómo tratará CostGate el unreconciled spend

Hoy `budget remaining` se calcula (inferido de `internal/costledger` + `Gate.Reserve`) contra `committed` real. Propuesta mínima, conservadora, sin romper el ledger actual:

```
effective_spend_for_gate = committed_actual + sum(estimated_pending_reconciliation, usando el monto YA RESERVADO como cota conservadora, no un nuevo cálculo)
```

Es decir: en vez de "liberar y olvidar" o "dejar reservado sin usar en el cálculo de presupuesto", el monto que ya estaba `reserved` para una invocación que terminó en `estimated_pending_reconciliation` simplemente **deja de liberarse** y sigue contando contra el presupuesto disponible hasta que se reconcilie (manual o automáticamente). Esto no requiere inventar una nueva fórmula de estimación de costo — reutiliza el número que `CostGate.Reserve` ya calculó antes del dispatch (`estimatedUSD`, visible en `gate.go` línea 58), que ya es conservador por diseño (basado en `max_output_tokens`, el techo, no el promedio).

**Riesgo documentado, no resuelto en este audit**: si `max_output_tokens` es muy superior al uso real típico, este método sobreestima el costo real acumulado de las llamadas fallidas — es intencionalmente conservador (fail-closed hacia "creer que costó más, no menos"), coherente con el principio del punto 4 del pedido ("no asumir $0"), pero puede sobrestimar el presupuesto consumido. Se documenta como trade-off aceptado, no como defecto.

---

## I. ¿Existe mecanismo oficial de DeepSeek para reconciliar uso/costo posterior?

Verificado contra la documentación oficial de DeepSeek (`api-docs.deepseek.com`, endpoint "Get User Balance"), no contra fuentes de terceros:

**No existe un endpoint oficial de reconciliación por request_id o de historial de uso por llamada.** El único endpoint de cuenta disponible (`GET /user/balance`) devuelve únicamente un snapshot del balance actual (`is_available`, `total_balance`, `granted_balance`, `topped_up_balance`) — sin historial de transacciones, sin desglose por request, sin fecha de expiración de grants, sin tabla de precios.

**Lo que sí es viable, y se propone como mecanismo de reconciliación agregada (no por invocación)**: sondear `/user/balance` antes y después de una ventana de tiempo (p. ej. antes/después de r9) y usar el delta de `topped_up_balance`/`total_balance` como una segunda fuente de verdad agregada, comparable contra `internal_estimated_day_total`. Esto NO permite reconciliar una invocación individual, pero sí permite detectar drift agregado sin depender de scraping frágil del dashboard — es un mecanismo oficial y soportado, solo que de granularidad de cuenta, no de request.

Para reconciliación real por invocación, la única fuente sigue siendo la respuesta HTTP misma del momento del dispatch — de ahí la importancia del punto F (no descartar el `usage` que el envelope YA trae en varios de los casos de fallo).

---

## J. Causa raíz del task claim cascade

Confirmada con precisión en `internal/tasks`:

1. El driver de canario (`canary15_driver_v2.py`) llama `task claim --worker ... --role ... --batch 1` — un claim genérico "dame la próxima disponible", no un claim por ID específico.
2. Cuando un cluster agota reintentos, el driver **nunca llama `task result`/`task finalize`** en su rama de error (`run_one_cluster`, rama `else: record["final_status"] = "FAILED_AFTER_RETRIES"`) — la tarea queda en estado no terminal (`running`/`leased`), reclamable de nuevo.
3. `internal/tasks/postgres/queue.go`'s `Claim` hace exactamente lo que se le pide: entrega la tarea `ready` más antigua disponible (`ORDER BY` implícito de la cola, `FOR UPDATE SKIP LOCKED`) — comportamiento CORRECTO de un claim genérico tipo cola.
4. Efecto: el siguiente índice del loop del driver, milisegundos después, reclama la tarea vieja abandonada del índice anterior en vez de la recién creada para sí mismo — cascada de desfase de 1 posición, propagándose indefinidamente una vez que la primera tarea queda huérfana.

**Esto NO es un bug del runtime de producción (`internal/tasks`) — es un mal uso del claim genérico desde un script de orquestación que necesitaba claim-por-ID y no lo tenía disponible en el CLI.**

Hallazgo positivo: **`internal/tasks/postgres/specific_claim.go` ya existe** — `Store.ClaimSpecific(ctx, taskID, request, validate, outboxMaxAttempts)`, con SQL `WHERE id=$1 AND organization_id=$2 AND status='ready' AND attempt_count<max_attempts AND available_at<=clock_timestamp() ... FOR UPDATE SKIP LOCKED`. El mecanismo determinista pedido en la sección 10 del pedido **ya está implementado a nivel de store/service** — simplemente no está expuesto por el CLI `orgctl` (`cmd/orgctl/tasks.go` solo expone `claim` genérico, no una variante por ID).

---

## K. Fix exacto para task identity

1. Exponer `ClaimSpecific` en `cmd/orgctl/tasks.go` como un nuevo subcomando, p. ej. `task claim-specific <task_id> --worker --role --lease --json` (aditivo, no reemplaza el `claim` genérico existente que otros flujos de producción sí puedan necesitar).
2. Reescribir el driver de canario para usar `task claim-specific <task_id_creado>` en vez de `task claim --batch 1`.
3. Como defensa adicional (invariante pedida explícitamente): incluso usando claim-por-ID, verificar tras el claim que `claimed.task.id == created.task.id` — si por cualquier razón no coincide (no debería ocurrir con `ClaimSpecific`, pero el invariante se pide expreso), **abortar ese cluster inmediatamente (FAIL CLOSED)**, nunca "usar el reclamado como fuente de verdad" (el comportamiento actual, que es precisamente lo que generó el desastre de identidad de r8).
4. Tests de regresión (ver P): `created_task_id == claimed_task_id` bajo concurrencia simulada de una tarea vieja no terminada.

---

## L. Fix exacto para output completeness (P0-C)

Agregar una validación semántica de dominio en la capa de curación (`internal/corpuscuration` o el punto donde el driver interpreta `curation_output`), **después** de la validación de JSON Schema existente, verificando:

```
output.cluster_id == expected_cluster_id
set(output.work_id for w in output.works) == set(expected_work_ids)
  -> ni faltantes, ni extra, ni duplicados, ni desconocidos
para cada Work: exactamente un tier, tier ∈ {P0, P1, silver_only, review_required}
```

Si falla cualquiera de estas condiciones: nuevo error_code de dominio, p. ej. `curation_output_contract_invalid` — dispara **bounded retry** (mismo cluster, mismo input, nueva invocación), y si persiste tras los reintentos permitidos, **terminal failure explícito** (nunca "succeeded" parcial, que es exactamente lo que pasó con el cluster de 8 Works que volvió con 7 — la invocación quedó marcada `succeeded` porque el JSON era válido contra el schema, aunque semánticamente incompleto).

Esta validación es puramente determinística (comparación de conjuntos), no requiere otra llamada al modelo para validarse a sí misma.

---

## M. Fix exacto para dedup metadata merge (P0-E)

Hallazgo de causa raíz adicional, no reportado antes: **`internal/corpuscuration.CollapseDuplicateWorksInCluster` (Go, ya escrito y testeado) nunca se invocó en r8** — el driver de canario tiene su **propia reimplementación en Python** (`collapse_duplicates()` en `canary15_driver_v2.py`) con la misma lógica de "primero lexicográfico gana", y es ESA la que se ejecutó. El bug de GraphReader (`work-00195` sin abstract elegido sobre `work-01212` con abstract) ocurrió en la reimplementación Python, no en el Go tal como estaba documentado.

Fix propuesto, en dos partes:

1. **Cambiar el criterio de selección de canónico** (en ambas implementaciones, o preferiblemente eliminando la duplicación consolidando en una sola): en vez de "lexicográficamente menor gana", usar una prioridad determinística explícita:
   ```
   1º: abstract presente y no degenerado > abstract ausente
   2º: título verificado por Semantic Scholar (title_source=semantic_scholar) > source_title crudo
   3º: si sigue empatado, lexicográficamente menor (determinismo de última instancia)
   ```
2. **Fusionar metadata, no solo elegir un ganador**: el resultado de la colapsación debe preservar identificadores de AMBOS aliases (unión de DOI/arXiv/S2/ACL, no solo los del canónico elegido) y dejar registro de la proveniencia de cada alias — el pedido original lo especifica como "union compatible" + "preservar all source provenance/aliases", que hoy `WorkIdentity`/`CollapseDuplicateWorksInCluster` no modela (solo produce `canonical[]`/`aliasOf map[string]string`, sin fusión de campos).
3. **Eliminar la reimplementación Python del driver**, exponiendo la función Go real vía CLI (mismo patrón que K) para que el driver de canario deje de mantener una segunda copia de esta lógica que puede volver a divergir.

Test específico pedido (GraphReader-like): dos Works con el mismo título normalizado, uno con abstract presente y arXiv ID, otro con abstract ausente y DOI únicamente — el canónico resultante debe tener abstract presente Y ambos identificadores (DOI+arXiv) fusionados.

---

## N. Telemetry exacta de provider failures (P0-D)

Persistir por invocación fallida (extensión de `model_dispatch_attempts`/`model_invocations`, o una tabla nueva `model_provider_failure_telemetry` ligada 1:1 a `invocation_id`):

```
provider_request_id
http_status
finish_reason            (si el envelope lo trae, ej. "length"/"content_filter"/"stop")
response_content_bytes   (longitud, no el contenido)
raw_response_hash        (ya existe como ResponseHash — reusar)
usage_available (bool)
input_tokens_if_available
output_tokens_if_available
cache_hit_tokens_if_available
cache_miss_tokens_if_available
response_format          (json_object/text/tool_calling)
max_output_tokens
request_duration
cluster_work_count
error_classification      (los error_code ya existentes)
provider_reached (bool)   (derivado 1:1 de AdapterError.Phase)
```

Para JSON malformado específicamente (cubre tanto `response_json_invalid` a nivel adapter como `response_normalization_failed` a nivel dispatch_service):
```
json_error_class
json_error_offset
starts_with_json_object (bool)
ends_with_json_object (bool)
```

**Explícitamente NO persistir**: prompt completo, respuesta completa del modelo, chain-of-thought, secretos — coherente con la disciplina de auditoría ya establecida en `internal/corpuscuration` (`unique_contribution` corto, nunca razonamiento interno).

`response_content_bytes`, `raw_response_hash` y el propio `HTTPStatus`/`ProviderRequestID` **ya se capturan hoy** en `ProviderOutcome` (confirmado en el código leído para C/D) — el gap real es que no se persisten de forma consultable/agregable fuera del blob de auditoría genérico, y `finish_reason`/cache tokens no se parsean en absoluto todavía (ver Q).

---

## O. Cómo persistir runtime/adapter build identity (P0-F)

Hoy `model_invocations` no tiene ningún campo de build/versión de código. Propuesta mínima:

1. Nueva columna `runtime_build_sha TEXT` en `model_invocations` (o tabla de metadata de dispatch), poblada al arrancar el proceso `model-worker` desde una variable de entorno/`ldflags` inyectada en el build de Docker (patrón estándar Go: `-ldflags "-X main.buildSHA=$(git rev-parse HEAD)"`), NO calculada en runtime por invocación (constante por proceso).
2. `adapter_id`/`adapter_version`: `adapter_id` ya existe implícitamente (el `provider_id`/`provider_model_id` lo identifican); falta un `adapter_version` explícito — puede derivarse igual que `runtime_build_sha` (mismo build), o versionarse independientemente si el adapter cambia con más frecuencia que el resto del binario (decisión de diseño pendiente, no bloqueante).
3. Esto es aditivo, no requiere cambiar la lógica de dispatch — solo agregar el dato al `INSERT`/`UPDATE` ya existente de `model_invocations`.

---

## P. Tests a implementar (mapeo directo a la lista pedida en la sección 29)

**Accounting**
1. Pre-provider reject (`AdapterFailureBeforeRequest`) → reservation released. *(ya pasa hoy, test de regresión para no romperlo)*
2. Provider success + usage → commit actual. *(ya pasa hoy, test de regresión)*
3. Provider reached + malformed response + usage disponible en el envelope → account actual (nuevo comportamiento, hoy no existe).
4. Provider reached + malformed response + usage NO disponible → `estimated_pending_reconciliation` explícito (nuevo).
5. Read timeout después de enviar → NOT free (cubre `AdapterFailureAmbiguous`, ya no se libera hoy — test de regresión + verificar que llega a un estado resoluble, no solo "ambiguous" eterno).
6. Connection failure antes de enviar → release. *(ya pasa, regresión)*
7. No debe quedar ninguna fila `reserved` sin explicación indefinida — test de que TODA invocación terminal tiene un `financial_outcome` explícito.
8. Cálculo de presupuesto disponible incluye el monto conservador no reconciliado (punto H).
9. Reconciliación convierte `estimated` → `reconciled actual` cuando el dato se vuelve disponible (para el camino de agregación por balance-delta del punto I).

**Tasks**
10. `created_task_id == claimed_task_id` (usando `claim-specific`).
11. Claim mismatch aborta (si se produjera pese a `claim-specific`, fail-closed).
12. Tarea fallida terminal (vía `task result --outcome failed` + `task finalize`, no manipulación de DB).
13. Tarea fallida terminal NO es reclamada por el siguiente cluster.

**Curation**
14-18. Missing Work rechazado / extra Work rechazado / duplicate Work rechazado / cluster incorrecto rechazado / conjunto exacto de Works aceptado.

**Dedup**
19. Duplicado con mejor abstract retiene el mejor abstract (caso GraphReader).
20. Identificadores/proveniencia fusionados (unión DOI+arXiv+S2+ACL, no reemplazo).

**Reproducibility**
21. `runtime_build_sha` persistido.
22. `adapter_version` persistido.

---

## Q. Confirmación: Context Engine no tocado

Confirmado — ningún archivo de `internal/contextengine`, ningún prompt, ningún parámetro de `context build`, ninguna política de contexto fue leído más allá de lo ya auditado en el reporte anterior (composición agregada de tokens), y nada fue modificado. Todo el trabajo de esta sesión fue lectura de código en `internal/modelruntime`, `internal/tasks`, `internal/corpuscuration`, `internal/costledger`, más verificación externa contra la documentación oficial de DeepSeek. Cero bytes escritos en el repositorio real del VPS — los únicos artefactos nuevos son `docs/reports/CURATION_CANARY_REPORT.md` (del turno anterior) y este mismo documento, que se entregará como archivo de reporte, no como cambio de código.

## R. Confirmación: r9 NO se ejecuta hasta aprobación

Confirmado. No se lanzó ningún proceso, no se creó ninguna tarea nueva, no se hizo ningún dispatch a DeepSeek durante esta auditoría. r9 queda bloqueado hasta que:
1. Se apruebe explícitamente el diseño de este documento (o se indiquen correcciones).
2. Se implementen los fixes P0-A a P0-F.
3. Pasen los tests de la sección P (Gates A-E del pedido original).

---

## Nota adicional no solicitada explícitamente pero relevante para la decisión

El **cache telemetry** (punto 17) es más barato de lo esperado: el struct `chatUsage` del adapter DeepSeek hoy solo decodifica `prompt_tokens`/`completion_tokens` — si DeepSeek envía `prompt_cache_hit_tokens`/`prompt_cache_miss_tokens` en el mismo objeto `usage` (patrón estándar de su API pública, no verificado contra una respuesta real en esta auditoría, solo contra documentación de terceros), agregar esos dos campos al struct Go es un cambio de una línea, sin migración de comportamiento — la migración de schema (dos columnas nuevas en `model_invocation_usage`) es lo único no trivial. Se documenta, no se prioriza sobre los P0 financieros/de identidad, tal como pide el punto 17 del pedido.
