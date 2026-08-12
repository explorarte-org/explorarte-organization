# R10_4_FINAL_VERDICT.md

Veredicto formal de R10.4 (Prompt Cache & Provider Rendering V1). Basado en `R10_4_PROVIDER_RENDER_AUDIT.md`, `R10_4_SHADOW_DETERMINISM_REPORT.md`, `R10_4_DEEPSEEK_CACHE_CANARY.md`.

## Resumen de evidencia

```
Causa raíz confirmada (Fase 1, código real):     metadata de auditoría (snapshot_id/task_ref/request_hash) serializada
                                                    antes del contenido en el único mensaje `user`, contaminando el
                                                    prefijo desde el primer byte.

Shadow determinism (15/15 snapshots R10):         stable_prefix_hash cardinality = 1   (ideal exacto)
                                                    dynamic_suffix_hash cardinality = 15  (ideal exacto)
                                                    provider_render_hash cardinality = 15 (ideal exacto)
                                                    fallback count = 0

Authority/content-equivalence gate:               PASS -- 0 pérdida de contenido, diferencia de bytes
                                                    explicada 100% por el header nuevo + separadores deterministas

Canario real (5 clusters DeepSeek):               5/5 terminal-valid, 0 reintentos, 5/10 requests usados
                                                    stable_prefix_hash idéntico en las 5 invocaciones reales
                                                    cache hit tokens: 0 -> 1,792 -> 7,552 -> 7,552 -> 7,552
                                                    cache_hit_ratio_total = 41.1%
                                                    costo real total: $0.03692164 (vs $0.06648474 en R10 para
                                                    los mismos 5 clusters -- -44.5%, NO atribuido solo a cache,
                                                    ver desglose en el reporte del canario)
```

## PROVIDER_RENDER_V1_STATUS

**`ADOPT`**

Criterios cumplidos: separación `AuditEnvelope`/`StablePrefix`/`DynamicSuffix` implementada sin tocar `internal/contextengine` core (capa aditiva, `Snapshot`/`Segment`/`Service.Render` intactos); fuente única determinista (`resolveRender` en `internal/modelruntime/bootstrap/runtime.go`) usada tanto para el hash de integridad pre-dispatch como para el render real de dispatch, evitando reabrir el bug `context_render_hash_mismatch` de R10; 28 tests de determinismo/integridad/autoridad, todos verdes; shadow determinism perfecto (cardinalidades exactas esperadas); gate de autoridad pasado con evidencia byte-exacta; canario real sin regresión de contrato, lifecycle, ni accounting; fallback explícito y observable implementado (0 veces disparado en esta fase, pero el mecanismo existe y está probado). Activado únicamente para `research.corpus_curate/v1`, tal como exige la sección 52 del pedido -- no se generalizó a ningún otro task class.

## DEEPSEEK_PROMPT_CACHE_STATUS

**`WORKING`**

Criterios de `PASS`/`STRONG_PASS` (sección 38-39): StablePrefix determinista (✅, confirmado en shadow Y en producción real), autoridad/correctness intactos (✅), requests posteriores muestran cache hits reales (✅, requests 2-5 de 5), una porción material del input reutilizable recibe hits (✅, 41.1% del total, con 63% en el request mediano-3 y un patrón estable de 7,552 tokens constantes en 3 de 5 requests), sin regresión de calidad/lifecycle (✅). Se clasifica como `WORKING` (no `STRONG_PASS`) por prudencia dada la muestra pequeña (n=5, un solo canario) -- el patrón es limpio y consistente con un cache funcionando correctamente, pero una sola corrida de 5 no es suficiente para el nivel de confianza que `STRONG_PASS` implicaría sin repetición. No se declara `INCONCLUSIVE_PROVIDER` porque SÍ hubo cache hits reales medidos, no ceros; no se declara `NOT_WORKING` porque el patrón observado (0→parcial→estable) es exactamente la firma esperada de un prefix cache calentándose, no ruido.

## Separación de decisiones (sección 51, ambas son válidas independientemente)

Es posible, y así ocurrió en este caso, que `PROVIDER_RENDER_V1_STATUS=ADOPT` con `DEEPSEEK_PROMPT_CACHE_STATUS` en un estado menos que `STRONG_PASS` -- la arquitectura (separación AuditEnvelope/StablePrefix/DynamicSuffix, determinismo, integridad) es correcta y se adopta independientemente de cuán bien un provider específico aproveche el prefijo estable resultante. En este caso ambas señales fueron positivas, pero se registran y evalúan por separado tal como exige el pedido.

## Distinción económica final -- no sobredeclarar

El ahorro de costo real observado (-44.5%, $0.06648 → $0.03692 para los mismos 5 clusters) combina DOS efectos distintos, medidos y reportados por separado en `R10_4_DEEPSEEK_CACHE_CANARY.md`:
1. **Reducción de volumen lógico de entrada** (la mayor parte del efecto): eliminar del prompt la metadata de auditoría que nunca fue necesaria para el modelo -- confirmado por el propio provider (`input_tokens` reportado cayó de 207,889 a 59,477 para los mismos 5 clusters, incluso antes de contar el cache).
2. **Cache hits reales** (efecto adicional, menor en magnitud pero genuino): 24,448 de esos 59,477 tokens de entrada fueron cache hits reales, reportados por el provider, no inferidos.

Nunca se afirma "el cache redujo los tokens en 71%" -- esa cifra es la reducción de volumen lógico, un efecto arquitectónico separado del cache.

## No se hizo

No se ejecutó Full Silver (R10.3 permanece congelado con `FULL_SILVER_READY=NO`, sin reinterpretar). No se llamó a MiMo en ninguna fase de R10.4. No se cambió el ruteo productivo de ningún rol más allá de activar `ProviderRender v1` para `research.corpus_curate/v1` (que ya usa DeepSeek como `primary` sin cambios -- la activación es de la capa de rendering, no del provider). No se implementó `MIMO_PRIMARY_DEEPSEEK_ADJUDICATOR`. No Kaggle, no MiniMax, no Skills/Tools, no Memory OS, no reducción agresiva adicional de contexto, no cambio de rubric/clustering/thresholds, no relajación de ningún validator.

## Hard caps -- verificación final

```
DeepSeek: 5 requests reales usados (canario) de 10 permitidos
MiMo:     0 requests (por diseño)
```

`go test ./...` verde (incluye los 28 tests nuevos de `ProviderRender` más el ajuste mecánico del golden-tip de migraciones 39→40). Imágenes reconstruidas y desplegadas antes de cada fase con llamadas reales. 0 STOP conditions disparadas en ninguna fase.

## STOP

R10.4 cierra aquí. `ProviderRender v1` queda adoptado y activo solo para `research.corpus_curate/v1`. No se avanza a Full Silver, no se generaliza a otros task classes, no se integra ningún provider/roadmap fuera de alcance. Pendiente de decisión del owner sobre próximos pasos (repetir el canario a mayor escala para confirmar `STRONG_PASS`, evaluar la estrategia `MIMO_PRIMARY_DEEPSEEK_ADJUDICATOR` de R10.3, o abordar la capacidad del Token Plan de MiMo antes de cualquier Full Silver).


---

**Historical runtime evidence referenced by R9–R10.5 was destroyed in the development-database incident of 2026-08-12. The reports and committed implementation remain intact, but the referenced database rows are no longer independently queryable.**
