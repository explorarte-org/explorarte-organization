# DEEPSEEK_VS_MIMO_CURATION_CANARY.md

**Revisión post-auditoría (reemplaza la versión anterior).** Mismos 15 clusters de R10 (mismo cluster_id, mismos Work sets, mismo `ContextProfile research.corpus_curate/v1`, mismo `rubric_version=v1`, threshold 0.88, mismo validador de contrato exacto, misma política de reintentos máx 2/cluster). Único cambio: provider/model. Todos los números de esta versión vienen directamente de `model_invocations`/`model_provider_outcomes`/`model_invocation_usage`/`model_invocation_results` en Postgres — evidencia real, re-verificada, no de los campos "error" del driver local (que en algún caso etiquetaban mal la causa exacta — ver auditoría de cluster_id abajo).

## 1. Reliability

| Métrica | DeepSeek R10 | MiMo-V2.5 | Delta |
|---|---:|---:|---:|
| Clusters terminal-valid | 11/15 (73.3%) | 14/15 (93.3%) | +3 clusters |
| Requests reales al provider (15-cluster run) | 23 | 19 | -17.4% |
| Requests/accepted outcome | 23/11 = 2.09 | 19/14 = 1.36 | -34.9% |

## 2. NO CROSS-PROVIDER TOKEN CLAIM — corrección obligatoria

Los conteos de tokens `input_tokens`/`output_tokens` que persiste cada provider **no son directamente comparables entre DeepSeek y MiMo** — usan tokenizers distintos, y un mismo texto puede tokenizar a cantidades diferentes según el vocabulario de cada modelo. La versión anterior de este reporte afirmó "31.6% menos tokens = 31.6% más eficiente" como conclusión inter-provider — **esa afirmación se retira**. Los conteos de tokens se mantienen como telemetría intra-provider únicamente (sirven para ver la propia reducción de contexto de cada provider corrida a corrida, no para comparar un provider contra otro).

**Métrica válida entre providers: bytes de respuesta reales en el wire** (`response_content_bytes`, persistido para el 100% de las invocaciones reales de ambos providers — no es una inferencia, es el tamaño real del payload HTTP de respuesta capturado por el adapter):

| Métrica | DeepSeek R10 (23 req) | MiMo (19 req, solo los 15 clusters) | Delta |
|---|---:|---:|---:|
| Bytes de respuesta totales | 596,800 | 303,035 | -49.2% |
| Bytes/accepted outcome | 596,800/11 = 54,255 | 303,035/14 = 21,645 | -60.1% |

Esta sí es una comparación válida (unidad física común, no dependiente de tokenizer). Es consistente con la dirección de la comparación de tokens (MiMo emite respuestas más compactas), pero la magnitud exacta reportada ahora es la de bytes, no la de tokens.

**Requests/accepted outcome** (unidad neutral, no depende de tokenizer ni de precio): DeepSeek 2.09, MiMo 1.36 — **-34.9%**, dato sólido e inter-provider-comparable.

## 3. Wall time vs latencia — separados

**Wall time del batch** (tiempo de pared observado para todo el lote, incluye espera entre dispatches del driver, no es latencia por-request):
- DeepSeek: 24m42s (18:14:43→18:39:25)
- MiMo: 17m18s (20:09:55→20:27:13)

Esto es **throughput de batch observado**, no "latencia 30% menor" (afirmación retirada de la versión anterior).

**Latencia real por request** (`created_at`→`updated_at` de cada `model_invocations`, medida en Postgres, ambos providers, incluye tiempo de red+cola+inferencia+normalización):

| Métrica | DeepSeek R10 (n=23) | MiMo (n=19, solo 15 clusters) |
|---|---:|---:|
| Latencia media | 57.8s | 48.0s |
| Latencia p50 | 50.0s | 42.5s |
| Latencia p95 | 90.2s | 72.3s |
| Latencia min/max | 11.7s / 145.5s | 25.7s / 88.6s |

MiMo muestra latencia por-request menor en media/p50/p95 en esta muestra — reportado como observación descriptiva de n pequeño, no como hallazgo estadísticamente robusto.

## 4. Cache

`prompt_cache_hit_tokens` **sí fue verificado para los 19 requests reales del canario de 15 clusters** (no extrapolado de los smokes):

- MiMo: cache hit constante de 192 tokens en cada uno de los 19 requests (mismo valor absoluto sin importar el tamaño del cluster) → `cache_hit_ratio = 3,648 / 830,983 = 0.44%`. Consistente con un prefijo fijo pequeño cacheado (probablemente las instrucciones del sistema) mientras el payload dinámico grande (lista de Works) nunca cachea — mismo patrón estructural ya documentado para DeepSeek en `R10_CANARY_REPORT.md` (campos dinámicos mezclados en lo que debería ser un prefijo estable).
- DeepSeek R10: `cache_hit_ratio = 0%` (0/900,589, ya confirmado en R10).

Ninguno de los dos providers logra un cache-hit significativo en este canario — MiMo tiene un cache-hit real pero marginal (0.44%), DeepSeek 0%.

## 5. Sentinels de DeepSeek R10 — recuperación por MiMo

| cluster_id | DeepSeek R10 | MiMo | resultado |
|---|---|---|---|
| scluster-a16533182030ccd4 | FAILED (`response_truncated_empty`) | OK (P0:2 P1:6) | **recuperado** |
| scluster-40c4d59b6cfd6490 | FAILED (`response_truncated_empty`) | OK (P0:1 P1:1) | **recuperado** |
| scluster-aac0f99841969e76 | FAILED (`response_truncated_empty`) | OK (P0:1 P1:4, 2 needs_deep_review) | **recuperado** |
| scluster-9f9da5855df592ce | FAILED (`response_truncated_empty`) | FAILED (ver sección "falla común" en `DEEPSEEK_VS_MIMO_QUALITY_AUDIT.md`) | no recuperado |

3/4 sentinels recuperados con completitud exacta de Works, sin fallos de contrato, en ambos casos con tiers plausibles (auditado en el reporte de calidad).

## 6. Calidad — ver reporte dedicado

El detalle completo (tabla pareada de 73 Works en los 11 clusters válidos en ambos providers, matriz de transición de tiers, transiciones de alto riesgo auditadas, false-negative gate, análisis de needs_deep_review, auditoría de los 3 clusters recuperados, y análisis de la falla común) está en **`docs/reports/DEEPSEEK_VS_MIMO_QUALITY_AUDIT.md`**. Resumen: **63.0% de acuerdo exacto de tier** en el set pareado (46/73), **1 critical false negative confirmado** (`scluster-ec8115783df9722f`/`work-03893`, un paper de benchmark degradado con confianza sin flag de revisión).

## 7. TOKEN PLAN RESOURCE ACCOUNTING

Separación explícita de las tres capas de contabilidad, ninguna sustituye a la otra:

**A. Provider usage (medido, real, por request)**
- MiMo: 25 requests reales totales en la fase (6 smoke + 19 del canario de 15 clusters), 1,118,404 input tokens + 105,790 output tokens (telemetría intra-provider, no comparable en magnitud absoluta con DeepSeek — ver sección 2).
- DeepSeek R10: 23 requests, 900,589 input tokens + 140,103 output tokens.

**B. Subscription resource usage (Token Plan — unidad de cuota separada)**
```
billing_mode: subscription
provider_resource_unit: credit
provider_resource_consumed: 106,134,737
source: mimo_dashboard
observed_at: 2026-08-11 (fecha exacta de observación del dashboard no capturada con timestamp preciso — reportado por el owner fuera de esta sesión de herramientas)
measurement_scope: UNKNOWN — no se pudo verificar si el contador corresponde exclusivamente a los 25 requests de esta fase R10.2 o si es acumulado de toda la cuenta/API key/histórico del Token Plan.
credit_semantics: unknown
```
**No se asume `1 credit = 1 token`.** El valor bruto (106,134,737) es ~87x mayor que el total de tokens reales medidos en TODA la fase (1,224,194 tokens = 1,118,404 input + 105,790 output de los 25 requests reales de MiMo). Esa desproporción es evidencia circunstancial de que **el contador probablemente NO está aislado a este run** — pudo ser: (a) acumulado de toda la cuenta/histórico del Token Plan (no solo esta API key), (b) una unidad de cuota mucho más fina que el token (créditos ≠ tokens 1:1, con multiplicador desconocido, posiblemente distinto por input/output/reasoning/cache), o (c) ambas cosas combinadas. Ninguna de estas hipótesis se puede confirmar sin documentación oficial de la fórmula credit→usage o sin acceso a un endpoint de cuenta que no fue verificado en esta sesión (ver `MIMO_V25_INTEGRATION_AUDIT.md` sección I: rate limits/cuota ya estaban marcados NO VERIFICADO).

Dado que el `measurement_scope` no coincide de forma verificable con los 25 requests de este canario: **`credits/request = N/A`, `credits/accepted_execution = N/A`** (instrucción explícita: no calcular si el alcance no coincide).

**C. Cash accounting (real, sin fabricar)**
```
DeepSeek R10: actual PAYG cost = $0.16531130 (100% provider-reported, 100% visibilidad, $0 unreconciled)
MiMo:         actual incremental cash charge = subscription-covered (NO "$0" sin qualifier —
               el gasto marginal real de estos 25 requests fue absorbido por una suscripción
               ya prepagada, no "no costó nada"; el costo real de la suscripción es un gasto
               fijo separado, fuera del alcance de esta comparación por-invocación)
```
No se afirma que un menor conteo de tokens/bytes de MiMo implique automáticamente menor costo inter-provider — no existe una tasa de conversión verificada entre el consumo de MiMo (tokens, bytes, o créditos) y un costo comparable en dólares al PAYG de DeepSeek.

**Comparación económica válida, sin fabricar dólares**: 19 vs 23 provider requests, 14 vs 11 accepted outcomes, 1.36 vs 2.09 requests/outcome, recurso de suscripción consumido (créditos, semántica no verificada) vs DeepSeek PAYG real $0.16531130.

## No se hizo

No se realizó ninguna llamada adicional al provider para esta auditoría (100% offline, sobre datos ya persistidos). No se corrió Full Silver. No se cambió el ruteo por defecto de ningún rol — MiMo sigue siendo challenger, DeepSeek intacto. Ver `docs/reports/R10_2_FINAL_VERDICT.md` para el veredicto formal.
