# R10_4_DEEPSEEK_CACHE_CANARY.md

Canario real, 5 clusters DeepSeek V4 Flash, mismo actor (`investigacion/research_worker_hourly`, ruta productiva sin cambios), misma `ContextProfile research.corpus_curate/v1`, mismo rubric, mismo validador, misma política de reintentos (máx 2). Única variable: `ProviderRender v1` activo (vs legacy en R10). Ventana continua, sin delays artificiales, sin warmup falso: 2026-08-12T00:37:06Z → 2026-08-12T00:41:44Z (~4m38s).

## Selección de clusters

2 pequeños + 2 medianos + 1 grande, los 5 ya `terminal-valid` en DeepSeek R10 (ningún sentinel):

```
scluster-ee06984a5f0f49d5   (2 Works,  pequeño)
scluster-ec8115783df9722f   (2 Works,  pequeño)
scluster-787c72467109c079   (8 Works,  mediano)
scluster-36a561ff6d2429da   (7 Works,  mediano)
scluster-da421180ed2349f2   (18 Works, grande)
```

## Tabla completa

| req | cluster | attempt | terminal | input tok | cache hit | cache miss | hit % (de ese req) | prefix bytes | dynamic bytes | latencia | costo real | status |
|---|---|---:|---|---:|---:|---:|---:|---:|---:|---:|---:|---|
| 1 | ee06984a5f0f49d5 | 1 | ✅ | 9,526 | 0 | 9,526 | 0.0% | 27,123 | 6,810 | 20.4s | $0.00429492 | succeeded |
| 2 | ec8115783df9722f | 1 | ✅ | 9,742 | 1,792 | 7,950 | 18.4% | 27,123 | 7,692 | 14.2s | $0.00410564 | succeeded |
| 3 | 787c72467109c079 | 1 | ✅ | 11,989 | 7,552 | 4,437 | 63.0% | 27,123 | 18,015 | 57.0s | $0.00829948 | succeeded |
| 4 | 36a561ff6d2429da | 1 | ✅ | 11,949 | 7,552 | 4,397 | 63.2% | 27,123 | 17,835 | 39.4s | $0.00749336 | succeeded |
| 5 | da421180ed2349f2 | 1 | ✅ | 16,271 | 7,552 | 8,719 | 46.4% | 27,123 | 37,426 | 94.6s | $0.01272824 | succeeded |

**5/5 terminal-valid, 5/5 en el primer intento (0 reintentos), 5 requests reales usados de 10 permitidos.** `stable_prefix_hash` idéntico en las 5 invocaciones (`7f39338b94dddbb5ad43e3b2377ba7e7a9af554e2c897a69f6fa70b2ee584b5e`), `fallback_to_legacy=false` en las 5, `provider_render_version="research-corpus-curate-render/v1"` en las 5 — confirmado directamente en `model_invocation_render_telemetry`, no inferido.

## Patrón request inicial vs posteriores (sección 14/36 del pedido)

- **Request 1**: 0 hit / 9,526 miss — 100% miss, exactamente lo esperado ("no exigir que request #1 tenga cache hit").
- **Request 2**: 1,792 hit / 7,950 miss — hit parcial, consistente con un cache todavía "calentando" del lado del provider.
- **Requests 3, 4, 5**: **7,552 hit tokens, constante y exacto en los tres**, pese a que los inputs totales de esos tres requests son distintos (11,989 / 11,949 / 16,271) — la porción reutilizada es fija (el StablePrefix), la porción no cacheada crece con el tamaño del cluster (DynamicSuffix). Este es exactamente el patrón esperado de un prefix-cache real funcionando: la MISMA porción fija del prompt (el StablePrefix idéntico) se reutiliza sin importar qué tan grande sea el resto.

**No se atribuye causalidad fuerte con n=5** (instrucción explícita) — pero el patrón (0 → parcial → constante) es exactamente la forma esperada de un prefix cache calentándose y estabilizándose, no ruido aleatorio.

## Cache efficiency (sección 30)

```
cache_hit_ratio_total = (0+1792+7552+7552+7552) / (9526+7950+4437+4397+8719) + hits
                        = 24,448 / 59,477 = 41.1%
```

Comparación aproximada de estructura (NO equivalencia token/byte exacta, solo referencia): `stable_prefix_bytes / provider_visible_bytes` por request varía de 42% (request 5, cluster grande) a 80% (request 1, cluster pequeño) — el prefijo fijo es una fracción decreciente del total a medida que crece el cluster, consistente con que el cache hit real (41.1% de tokens) cae en ese mismo rango de magnitud.

## Latencia

```
p50 (n=5): 39.4s
rango observado: 14.2s - 94.6s
```

Con n=5 no se calcula un p95 robusto — se reporta el rango observado. No hay un patrón claro de latencia menor en cache-hits vs miss en esta muestra (el request 5, con cache hit real, tuvo la latencia más alta de los 5 — dominada por el tamaño del cluster grande, no por el cache). **No se atribuye causalidad latencia↔cache con n=5** (instrucción explícita).

## NO SOBREDECLARAR AHORRO — sección 44, distinción obligatoria

Comparando estos mismos 5 clusters exactos contra sus propias invocaciones reales en R10 (legacy render, mismo actor/rubric/policy, `invocation_id` 198/203/208/211/202):

| | R10 (legacy render) | R10.4 (ProviderRender v1) | Delta |
|---|---:|---:|---:|
| input tokens totales | 207,889 | 59,477 | **-71.4%** |
| cache hit tokens | 0 | 24,448 | — |
| costo real total | $0.06648474 | $0.03692164 | **-44.5%** |

**Esta caída de 207,889→59,477 tokens NO es atribuible al cache** — el cache explica solo 24,448 de esos tokens (los que SÍ se cachearon en R10.4, comparados contra el mismo cluster en R10 que tuvo 0% cache). La gran mayoría de la reducción (207,889 - 59,477 - 24,448 aproximadamente, la diferencia de "logical input volume") viene de que `ProviderRender v1` elimina el envoltorio JSON de auditoría (`snapshot_id`, `task_ref`, `request_hash`, y los ~10 campos de metadata por segmento -- `ordinal`, `authority_priority`, `source_kind`, `content_hash`, `byte_count`, etc. -- repetidos en cada uno de los ~14 segmentos) que el render legacy sí serializaba dentro del prompt. Esto es una reducción real de **volumen lógico de entrada** (contenido que nunca fue necesario para el modelo, ya confirmado en el audit de Fase 1), medida de forma directa por el propio provider (`input_tokens` reportado), separada y adicional al efecto de cache.

**Métrica económica objetivo (sección 45)**:
```
cache_miss_tokens / accepted_outcome = 35,029 / 5 = 7,005.8
actual USD / accepted_outcome        = $0.03692164 / 5 = $0.00738433
```

No se calculó un costo hipotético "todo-miss" con precios verificados -- no se pudo confirmar rápidamente la tabla de precios exacta de DeepSeek en esta sesión y la instrucción exige no fabricar esa comparación sin datos de precio verificados; se reporta el ahorro real medido (costo actual antes/después) en su lugar, que es un dato 100% real, no derivado.

## Calidad y contrato — sin regresión

5/5 clusters con contrato de salida exacto cumplido (Work set completo, tiers válidos), 5/5 tareas finalizadas automáticamente (`task_finalized=true`), 0 limpieza manual, 0 pérdida de visibilidad de accounting (los 5 `provider_wallet_events` completos y reales). No se repitió la auditoría completa de calidad de R10.2 (no requerido por el pedido) — tier_counts observados: P0:7 P1:23 silver_only:7 review_required:0 across los 5 clusters, sin ninguna anomalía evidente frente a los tiers ya conocidos de R10 para estos mismos clusters.

## Contabilidad

5 requests DeepSeek reales, costo real total $0.03692164, dentro del cap de 10. 0 requests MiMo (por diseño, sección 17-18 del pedido).
