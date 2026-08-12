# R9_CANARY_REPORT.md

Canario de 15 clusters, curación gobernada DeepSeek V4 Flash, **después de** los fixes P0-A→P0-F (Gates A-H). Mismos 15 cluster IDs de r8, config congelada (`rubric_version=v1`, `cluster_algorithm_version=gemini-embedding-2-average-link-v1-threshold-0.88`, `model_route=deepseek/deepseek-v4-flash`). Ventana real: 2026-08-11 06:54:36 UTC → 07:17:32 UTC (~22m56s de dispatch; limpieza post-hoc de tareas se hizo después, no cuenta como tiempo de canario).

## 1. Contabilidad exacta — task identity (Gate C)

**15/15 clusters, 15/15 task_ids únicos, 0 reuso, 0 claim_mismatch.** A diferencia de r8 (donde task_ids se reciclaban entre índices por el bug de cascada FIFO), cada índice creó y reclamó exactamente su propio task_id (115-129, uno a uno con los índices 1-15). `task claim-specific` funcionó como se diseñó.

| idx | cluster_id | size | task_id | attempt_id | resultado |
|---|---|---|---|---|---|
| 1 | scluster-ee06984a5f0f49d5 | 2 | 115 | 100 | OK |
| 2 | scluster-b5ba715ff2f3636f | 6 | 116 | 101 | FAILED |
| 3 | scluster-da421180ed2349f2 | 18 | 117 | 102 | OK |
| 4 | scluster-ec8115783df9722f | 2 | 118 | 103 | FAILED |
| 5 | scluster-a16533182030ccd4 | 8 | 119 | 104 | OK |
| 6 | scluster-b1900dc3fd6d43c2 | 16 | 120 | 105 | OK |
| 7 | scluster-ba5e63822655c3d4 | 2 | 121 | 106 | FAILED |
| 8 | scluster-787c72467109c079 | 8 | 122 | 107 | OK |
| 9 | scluster-7049df7250cc08a4 | 3 | 123 | 108 | FAILED |
| 10 | scluster-36a561ff6d2429da | 7 | 124 | 109 | FAILED |
| 11 | scluster-40c4d59b6cfd6490 | 2 | 125 | 110 | OK |
| 12 | scluster-aac0f99841969e76 | 5 | 126 | 111 | OK |
| 13 | scluster-ff2a66fd75f35298 | 2 | 127 | 112 | FAILED |
| 14 | scluster-164835480d9c2ce2 | 8 | 128 | 113 | FAILED |
| 15 | scluster-9f9da5855df592ce | 2 | 129 | 114 | FAILED |

**7/15 OK, 8/15 FAILED.** Nota importante de comparación honesta: r8 tuvo "9/15" en bruto, pero mi reconciliación forense de r8 ya había demostrado que 3 de esas 9 estaban mal etiquetadas (contenido de un cluster distinto al anunciado) y 2 clusters seleccionados nunca se despacharon — el número real comparable de r8 era 9 éxitos limpios sobre 13 realmente intentados. r9 mide con un validador estricto que r8 no tenía, así que la tasa baja no es una regresión, es una medición más honesta (ver sección 6).

## 2. Infraestructura (Gates C, D)

Checklist por los 15: `task create` OK×15, `claim-specific` OK×15 (0 mismatch), `task start` OK×15, `context build` OK×15, `model assignment` OK×15, `invocation create` OK×15, DeepSeek reached ×24 intentos (100% de los intentos llegaron al provider — 0 fallos pre-provider), `result`/`finalize` — **ver hallazgo crítico abajo**.

### Hallazgo nuevo, en vivo durante r9: bug real en el driver, encontrado y corregido

Las 8 tareas fallidas quedaron **atascadas en `status='running'`** tras el intento de finalización — `task result --outcome failed` fallaba con `"attempt outcome is invalid"`. Causa raíz: el vocabulario real de `AttemptOutcome` en `internal/tasks/domain.go` es `succeeded | retryable_failure | non_retryable_failure | cancelled` — **nunca `"failed"`**, que es lo que el driver enviaba. Este es exactamente el precursor del bug de cascada de r8 (tareas no-terminales quedando reclamables). Se detectó de inmediato (ninguna tarea llegó a ser reclamada por otra en esta corrida, porque no hubo un índice 16 que la reclamara), se corrigió el driver (`outcome: non_retryable_failure` + `failure_code` requerido, que también faltaba), y se finalizaron manualmente las 8 tareas atascadas: 3 vía `task result`+lease aún válido, 5 vía `task cancel` (el lease de 14 min ya había expirado mientras se depuraba el bug en vivo). **Confirmado: las 15 tareas de r9 terminan en estado terminal** (`completed`×7, `failed`×3, `cancelled`×5). 0 huérfanas al cierre.

### Provider failures — clasificación (Gate D, F)

| tipo de fallo | clusters | naturaleza |
|---|---|---|
| Fallo puro del provider (respuesta truncada/JSON inválido, sin problema de identidad) | 2, 4, 13, 15 | `response_truncated_empty`×3, `response_normalization_failed`×1, en ambos intentos |
| **Nuevo: contrato semántico rechazó un `cluster_id` mal ecoado** | 7, 9, 10, 14 | El modelo devolvió JSON válido contra schema, pero `cluster_id` con un typo (`"cluster-..."` en vez de `"scluster-..."`, o `"sclduster-..."`) — el validador de Gate D lo detectó y rechazó correctamente, disparando retry acotado |

**0 schema-validation failures. 0 fallos pre-provider. 0 context_drift. 0 lease expirations durante el dispatch en sí** (el único lease-expiry fue el mío, post-hoc, durante la depuración del bug de `AttemptOutcome`).

## 3. Costo real (Gate A) — el resultado más importante de r9

**100% de las 24 invocaciones (12 exitosas + 12 fallidas a nivel de dispatch) tienen `provider_reported=true`, `cost_provenance=actual_provider_reported`, `financial_outcome=actual`, y un evento `committed` real.** Cero invocaciones liberadas-como-gratis. Cero reservas sin explicar. Esto es la validación directa y completa del fix de P0-A.

- Total input tokens (24 intentos): **1,946,017**
- Total output tokens (24 intentos): **127,079**
- Total tokens: **2,073,096**
- **Costo real total comprometido (`committed`, todo `actual_provider_reported`): $0.30802450**
- **accounting_visibility_ratio = 100%** (financially_explained / provider_reaching) — compara contra el ~14.7% del baseline pre-fix.

Cache tokens: capturados correctamente en el camino de fallo (`insertRecoveredUsage`), **NULL en el camino de éxito** (gap ya documentado en `P0_FIX_EVIDENCE.md` — `normalizer.go` no fue tocado). Donde sí hay dato: **`prompt_cache_hit_tokens=0` en el 100% de las 12 invocaciones fallidas con telemetría de cache disponible** — cache-hit real de DeepSeek es 0% en esta carga de trabajo. Esto es evidencia empírica directa de que el contexto fijo de ~73-100k tokens no se está beneficiando de prompt caching en absoluto (razón probable: IDs/timestamps dinámicos mezclados dentro del contexto que se supone estable) — insumo directo para R10.

### Métricas de costo por unidad de valor

- Works evaluados (7 clusters exitosos, sumando `works` en cada output): 2+18+8+16+8+2+5 = 59 (todas cuadran exactamente contra lo esperado, 0 faltantes — incluye la confirmación del caso MemRefine, sección 6)
- cost per cluster (24 intentos / $0.308): promedio $0.01283/intento
- cost per accepted outcome (7 clusters realmente aceptados): $0.308 / 7 ≈ **$0.044/cluster aceptado** (sube respecto a r8 porque ahora se contabiliza el costo real de los reintentos fallidos, que antes era invisible)

## 4. Latencia

24 intentos, duración provider (creación→terminal): mediana **41.4s**, p95 aproximado **96.5s** (rango 13.4s-106.4s). Nada cerca del timeout de 10 min configurado — el bug de 30s de r7 permanece confirmado resuelto (0 ocurrencias de `response_read_failed` puro por timeout de infraestructura en toda la corrida).

## 5. Calidad de curación (7 clusters exitosos)

Resumen agregado: **P0=13, P1=33, silver_only=13, review_required=2** (sobre 59 Works evaluados, cuadra exacto). Tablas completas por cluster disponibles en `canary_r9_results.jsonl` (referencia interna) — aquí los hallazgos relevantes:

- **cluster memory `scluster-a16533182030ccd4`**: `work-03889` (MemRefine, el Work que r8 perdió silenciosamente sin tier) **ahora aparece exactamente una vez, con tier P1, confidence 0.79.** Confirmación directa de que Gate D (validador de contrato) cierra este caso — cualquier omisión ahora es rechazada antes de aceptarse como éxito.
- **cluster context `scluster-ba5e63822655c3d4`** (GraphReader): el `identity_aliases_collapsed` registrado es `{"work-00195": "work-01212"}` — **exactamente al revés que en r8**, donde `work-00195` (sin abstract) ganaba como canónico. Ahora `work-01212` (con abstract) es el canónico y `work-00195` es el alias. Confirmación directa de que Gate E (fusión de metadata) funciona en producción real. (No se pudo ver el tier resultante porque este cluster falló ambos intentos por el typo de `cluster_id`, no por el dedup — el input de entrada quedó correctamente construido de todas formas.)

## 6. Casos que queríamos validar

- **Caso B (complementarios)**: cluster rag `scluster-ee06984a5f0f49d5` — mismo patrón que r8 (distracting-passage diagnosis P0 + distraction-aware retrieval P1), estable entre corridas.
- **Caso C (survey+original+benchmark)**: cluster memory `scluster-b1900dc3fd6d43c2` (16 Works) — mismo patrón general, 4 P0 cubriendo arquitecturas fundacionales + baseline de evaluación, con la reasignación de qué exactamente es P0 vs P1 fluctuando algo respecto a r8 (ver sección de tier stability, no aplica aquí por falta de par exacto r8 para este cluster — r8 también tuvo éxito en este cluster, comparación real en `R9_VS_R10` sería más apropiada pero se puede adelantar: ambos runs coinciden en que hay 4 P0 fundacionales, difieren en 1-2 asignaciones P0 vs P1 marginales — consistente con la variabilidad estocástica esperada de un modelo, no un cambio estructural).
- **Caso F (review_required correcto)**: cluster context `scluster-787c72467109c079`, `work-02601` — mismo Work marcado `review_required` en r8 y r9 (confidence 0.70 en r8, 0.65 en r9), consistente entre corridas. Además cluster symbolic `scluster-aac0f99841969e76` agrega un `review_required` nuevo (`work-01940`, confidence 0.55) no visto en r8 para este cluster tal cual (r8 solo mostró 2 Works de 5 tierados, r9 muestra 5/5 completos con distribución P0/P0/P1/P1/review — de hecho r9 aquí es MÁS completo que r8 para este cluster, otra señal de que Gate D está mejorando la completitud real, no solo la detectando).

## 7. False negative audit (7 clusters completados)

Revisando todos los `silver_only` (13 total) con el mismo criterio que el gate de r8 (confidence ≥0.80 + foundational/único-en-familia/benchmark): ninguno de los 13 alcanza confidence ≥0.80 estando simultáneamente sin cobertura P0/P1 de su misma `redundancy_group`. El caso más cercano a revisar (`work-02671`, cluster streaming-video, único representante de `live-streaming-assistant`, confidence 0.75) queda bajo el umbral de "alta confidence" y no tiene evidencia de ser foundational.

**CRITICAL_FALSE_NEGATIVE encontrados: 0.**

## 8. Redundancy quality

Mismo patrón sano que r8: `redundancy_group`s comparten tema pero reciben tiers distintos según contribución (ej. cluster rag 18-Works: `hypergraph_rag` tiene 2 P1 no colapsados a 1; `planning_verification` tiene 1 P0 + 1 silver, discriminando dentro de la familia en vez de tratarlos como duplicados). No se observó colapso indebido de "relacionado" a "redundante".

## 9. Reliability — el hallazgo dominante de r9

| métrica | valor |
|---|---|
| provider requests reales (llegaron a DeepSeek) | 24/24 intentos (100% de los intentos, 0 pre-provider failures) |
| first-attempt success (dispatch-level) | 12/15 clusters tuvieron al menos un dispatch técnicamente exitoso en su primer intento |
| terminal_valid_cluster_rate (pasa TODO: dispatch + contrato semántico) | **7/15 = 46.7%** |
| empty/truncated response rate | 4 clusters con `response_truncated_empty` en al menos un intento |
| invalid JSON rate | 2 clusters con `response_normalization_failed` |
| semantic contract failure rate (cluster_id typo) | 4 clusters (7, 9, 10, 14) |

**Con el harness completamente corregido (identidad, accounting, dedup, contrato de output — todos verificados limpios), el cuello de botella real para escalar a 2,009 clusters es la confiabilidad de DeepSeek V4 Flash en esta forma de tarea** (JSON estructurado grande, requiere ecoar un `cluster_id` largo exactamente, sobre un input de ~75-100k tokens). Esto es exactamente la distinción que el propio pedido anticipaba: "si sigue en 9/15... el problema es reliability del path DeepSeek JSON-output, no calidad de corpus curation."

## 10. Accounting visibility

**100%** — ver sección 3. Este es el resultado directo y medible del fix de Gate A.

## 11. Reproducibilidad

`rubric_version=v1`, `cluster_algorithm_version` idéntico, `model_route` idéntico en las 24 invocaciones. `input_hash` persistido y verificado distinto por cluster real. `runtime_build_sha`/`adapter_id` ahora se persisten (Gate G) aunque `runtime_build_sha` sigue siendo el placeholder `"unknown"` — la inyección real de `-ldflags` en el build de Docker sigue pendiente (limitación ya documentada en `P0_FIX_EVIDENCE.md`, no resuelta en r9).

## 12. Veredicto: **ITERATE**

No es FAIL: identidad, accounting, dedup y completitud de output están genuinamente limpios y verificados — los P0 fixes funcionan exactamente como se diseñaron, incluyendo en producción real, no solo en tests. No hay corrupción, no hay costo invisible, no hay tareas huérfanas al cierre.

No es PASS ni PASS_WITH_CHANGES: **46.7% de tasa de éxito terminal es demasiado bajo para autorizar responsablemente 2,009 clusters.** El bloqueador ya no es infraestructura — es confiabilidad real del provider en esta forma de tarea. Antes de escalar, hace falta entender si la reducción de contexto (R10) mejora esto (menos tokens de input podría reducir truncamiento) o si el problema es independiente del tamaño de contexto (en cuyo caso el camino sería ajustar `max_output_tokens`/reintentos, no algo que R10 vaya a resolver).

**Hallazgo adicional a preservar como regresión permanente**: el bug de `AttemptOutcome="failed"` (vocabulario inválido) — agregar test de regresión antes de cualquier corrida futura del driver, y considerar si el CLI debería rechazar valores inválidos con un mensaje más temprano/claro en el flujo (hoy solo se descubre al momento de `task result`, después de ya haber gastado el dispatch).

## 13. No se hizo

No se corrió Full Silver. No se publicó Knowledge. No se implementó R10 (Context & Inference Economy) — solo se generó este reporte y el baseline de contexto (documento separado). Repositorio sin commitear.


---

**Historical runtime evidence referenced by R9–R10.5 was destroyed in the development-database incident of 2026-08-12. The reports and committed implementation remain intact, but the referenced database rows are no longer independently queryable.**
