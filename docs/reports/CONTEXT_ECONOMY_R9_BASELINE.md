# CONTEXT_ECONOMY_R9_BASELINE.md

Baseline de composición de contexto para `research.corpus_curate`, medido sobre las 15 invocaciones reales de r9 (primer intento de cada cluster). Esto es diagnóstico puro — nada del Context Engine se tocó.

## 1. Piso fijo confirmado, consistente con r8

| cluster (Works) | input_tokens (1er intento) |
|---|---|
| context `scluster-ba5e63822655c3d4` (1 Work tras dedup) | 74,101 — referencia (piso) |
| rag `scluster-ee06984a5f0f49d5` (2) | 75,558 |
| memory `scluster-ec8115783df9722f` (2) | 76,375 |
| symbolic `scluster-40c4d59b6cfd6490` (2) | 75,879 |
| agents `scluster-ff2a66fd75f35298` (2) | 76,418 |
| edge `scluster-9f9da5855df592ce` (2) | 75,841 |
| efficiency `scluster-7049df7250cc08a4` (3) | 77,438 |
| symbolic `scluster-aac0f99841969e76` (5) | 79,838 |
| rag `scluster-b5ba715ff2f3636f` (6) | 82,945 |
| efficiency `scluster-36a561ff6d2429da` (7) | 84,944 |
| memory `scluster-a16533182030ccd4` (8) | 84,953 |
| context `scluster-787c72467109c079` (8) | 85,129 |
| agents `scluster-164835480d9c2ce2` (8) | 85,270 |
| memory `scluster-b1900dc3fd6d43c2` (16) | 101,084 |
| rag `scluster-da421180ed2349f2` (18) | 101,033 |

Regresión lineal simple (piso + marginal/Work): **piso ≈ 74,100 tokens** (99.9% consistente con el ~73,000 medido en r8, confirmando que es un artefacto estructural del Context Engine, no ruido de una sola medición), **marginal ≈ 1,200-1,700 tokens/Work** (mediana ~1,380), con cierta dispersión (728-1,690) probablemente por longitud variable de abstract entre Works.

Para el cluster más pequeño posible (1 Work), el piso fijo es **>98% del input total**. Para el más grande (18 Works), sigue siendo **~73% del input total**.

## 2. Cache-hit real: 0%

Dato empírico directo (no inferido) de la telemetría nueva de Gate A: **`prompt_cache_hit_tokens=0` en el 100% de las 12 invocaciones donde DeepSeek reportó ese campo** (todas del lado de fallo, por el gap de wiring documentado en el camino de éxito — ver `P0_FIX_EVIDENCE.md`). Esto confirma cuantitativamente lo que ya se sospechaba: el "prefijo estable" de ~74k tokens **no se está beneficiando de prompt caching en absoluto** — con altísima probabilidad porque contiene datos dinámicos (task_id, timestamps, correlation_id, request_id) mezclados dentro de lo que debería ser un prefijo byte-idéntico entre llamadas.

## 3. Segmentos — hipótesis cualitativa (sin inspección directa del snapshot esta ronda)

No se hizo un desglose segmento-por-segmento del `context_snapshot` real esta vez (queda para el diseño de R10, sección correspondiente del `R10_DESIGN_AUDIT.md`). Lo que sí se puede afirmar con evidencia directa:

- **Candidatos a contenido fijo/constante**: contexto canónico de organización/seguridad, perfil del rol `investigacion/research_worker_hourly`, políticas organizacionales — dado que el piso (~74k) es consistente entre 15 clusters de dominios completamente distintos (rag/memory/context/efficiency/symbolic/agents/edge), es prácticamente seguro que esto NO varía por dominio de la tarea, es contexto genérico de organización aplicado uniformemente.
- **Candidato a contenido dinámico problemático dentro del "prefijo"**: cualquier ID/timestamp que cambie entre llamadas y esté mezclado con el contenido canónico — impide el cache-hit medido en el punto 2.
- **Contenido claramente dinámico y correcto de estarlo**: el payload del cluster (título/abstract/año/venue/topics por Work) — esto explica el marginal de ~1,200-1,700 tokens/Work, y es exactamente lo que SÍ debe cambiar entre llamadas.

## 4. Números que R10 debe superar (no metas arbitrarias, mediciones)

- Piso fijo actual: **~74,100 tokens**
- Cache-hit actual sobre ese piso: **0%**
- Costo real total de 24 intentos de r9: **$0.308** (100% contabilizado, ver `R9_CANARY_REPORT.md`)
- cost por Work evaluado: ~$0.0052 (usando el total de 59 Works evaluados en los 7 clusters exitosos contra el costo total de los 24 intentos, incluyendo fallos — más alto que r8 porque ahora se ve el costo real de los reintentos)

No se elimina nada todavía. Este documento es exclusivamente el punto de partida medido para el diseño de R10.


---

**Historical runtime evidence referenced by R9–R10.5 was destroyed in the development-database incident of 2026-08-12. The reports and committed implementation remain intact, but the referenced database rows are no longer independently queryable.**
