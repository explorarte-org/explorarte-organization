# R10_CANARY_REPORT.md

Fase 6 de R10: ejecución real de los mismos 15 clusters de r9.1, con el Context Compiler activo (perfil `research.corpus_curate/v1`, proyección `role-catalog.yaml → entrada propia del rol`) contra DeepSeek V4 Flash real. Config congelada idéntica a r9.1 salvo el contexto (`rubric_version=v1`, `cluster_algorithm_version` igual, `model_route` igual, mismos 15 cluster IDs, mismos Work sets).

## Bug encontrado y corregido antes de la ejecución real

El primer smoke (1 cluster) falló con `context_render_hash_mismatch`: `dispatch_service.go` recalcula el hash del render justo antes de despachar y lo compara contra el hash guardado en el snapshot al construirlo — un control de integridad real, no un bug preexistente. Mi primera versión de la activación proyectaba el contenido solo en el punto de render (`RenderContextSnapshot`) sin actualizar el hash de referencia (`GetContextSnapshot`), así que los dos dejaban de coincidir. Corregido para que ambos deriven de la misma proyección — verificado con un segundo smoke limpio (57% de reducción real, cero errores) antes de lanzar los 15.

## Identidad de tareas (Gate C) — igual de limpio que r9.1

**15/15 task_ids únicos (147-161), 0 reuso, 0 mismatch. Las 15 tareas terminaron solas en estado terminal** (11 `completed` + 4 `failed`), **0 limpieza manual**.

## Resultado

| idx | cluster_id | size | resultado | input_tokens |
|---|---|---:|---|---:|
| 1 | scluster-ee06984a5f0f49d5 | 2 | OK | 32,536 |
| 2 | scluster-b5ba715ff2f3636f | 6 | OK | 39,919 |
| 3 | scluster-da421180ed2349f2 | 18 | OK | 58,013 |
| 4 | scluster-ec8115783df9722f | 2 | OK | 33,335 |
| 5 | scluster-a16533182030ccd4 | 8 | **FAILED** | — |
| 6 | scluster-b1900dc3fd6d43c2 | 16 | OK | 58,061 |
| 7 | scluster-ba5e63822655c3d4 | 2 | OK | 31,075 |
| 8 | scluster-787c72467109c079 | 8 | OK | 42,095 |
| 9 | scluster-7049df7250cc08a4 | 3 | OK | 34,401 |
| 10 | scluster-36a561ff6d2429da | 7 | OK | 41,910 |
| 11 | scluster-40c4d59b6cfd6490 | 2 | **FAILED** | — |
| 12 | scluster-aac0f99841969e76 | 5 | **FAILED** | — |
| 13 | scluster-ff2a66fd75f35298 | 2 | OK | 33,382 |
| 14 | scluster-164835480d9c2ce2 | 8 | OK | 42,237 |
| 15 | scluster-9f9da5855df592ce | 2 | **FAILED** | — |

**11/15 = 73.3% terminal-valid** (vs 12/15 = 80% en r9.1). **Las 4 fallas son 100% `response_truncated_empty` puro** — sin typos de `cluster_id`, sin problemas de hash, sin identidad. No hay ninguna nueva categoría de fallo introducida por la reducción de contexto.

## Accounting (Gate A) — 100% visibilidad mantenida

23 invocaciones totales (11 éxito + 12 intentos fallidos), **100% `provider_reported=true`, `actual_provider_reported`/`actual`**. Costo real total comprometido: **$0.16531130**.

## Cache

`prompt_cache_hit_tokens=0` en el 100% de las 23 invocaciones — sigue confirmando 0% cache-hit, consistente con r9.1. La reducción de contexto en sí no cambia esto (el problema de fondo, campos dinámicos mezclados en lo que debería ser prefijo estable, sigue sin resolverse — es un cambio de `renderer.go` separado, documentado en `R10_DESIGN_AUDIT.md` sección J, no hecho en esta fase).

## Calidad (11 clusters exitosos)

| tier | count |
|---|---:|
| P0 | 23 |
| P1 | 46 |
| silver_only | 4 |
| review_required | 0 (tier) |
| needs_deep_review (flag) | 1 |

73 Works evaluados en total, todos con tier exacto (0 faltantes — el contrato semántico de Gate D siguió aplicando sin cambios). El único `needs_deep_review=true` es en el cluster context `scluster-787c72467109c079`, mismo patrón que en r8/r9/r9.1 (misma familia de Works, misma señal de incertidumbre real).

## Veredicto de esta fase: **PASS_WITH_CHANGES**

- **Correctness**: limpio — identidad exacta, 0 regresión de accounting, 0 regresión de completitud de output, autoridad no tocada (confirmado en el shadow report), precedencia no tocada.
- **Quality**: sin evidencia de degradación atribuible a la reducción de contexto — 0 critical false negatives nuevos (mismo patrón de tiers, misma discriminación redundante/complementario que las corridas anteriores).
- **Economy**: reducción real y sustancial (ver `R9_VS_R10_COMPARISON.md` para el detalle exacto).
- **Reliability**: 73.3% vs 80% — una caída de exactamente 1 cluster sobre 15. Con n=15 y comportamiento estocástico ya documentado del provider (DeepSeek ya mostraba tasas de `response_truncated_empty` variables entre corridas idénticas de r8→r9→r9.1 sin cambiar nada de contexto), **esto no es evidencia suficiente para atribuir la caída a la reducción de contexto** — necesitaría repetirse varias veces para separar señal de ruido. Se reporta con honestidad, no se ignora ni se sobre-interpreta.

No llega a PASS puro porque la pregunta central de R10 (¿la reducción de contexto mejora, empeora, o no afecta la reliability?) queda sin respuesta concluyente con un solo run de 15 — exactamente el escenario "R9.1 11/15, R10 11-ish/15" que el propio diseño anticipó como resultado ambiguo, no data suficiente para decidir. Ver comparación completa.
