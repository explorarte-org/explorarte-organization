# R10_3_NEGATIVE_TIER_ADJUDICATION.md

Parte B de R10.3. Estrategia evaluada: MiMo-V2.5 como curador primario, DeepSeek V4 Flash como adjudicador ciego de sus tiers negativos (`silver_only`/`review_required`).

## Candidate set — derivado de Postgres, no hardcodeado

Consulta directa sobre `mimo15_results.jsonl` (fuente: `curation_output` persistido de las 14 invocaciones MiMo válidas de R10.2). Resultado: **10 Works candidatos en 6 clusters** (coincide con el conteo de la auditoría de calidad anterior, pero se re-derivó desde la fuente persistida, no se asumió el número).

| cluster | candidatos | mimo_tier |
|---|---|---|
| scluster-b5ba715ff2f3636f | work-00684, work-01370, work-02895 | review_required (los 3) |
| scluster-ec8115783df9722f | work-03893 | silver_only |
| scluster-b1900dc3fd6d43c2 | work-03496 | silver_only |
| scluster-787c72467109c079 | work-00594, work-01307 | silver_only (ambos) |
| scluster-7049df7250cc08a4 | work-02043 | silver_only |
| scluster-36a561ff6d2429da | work-02134, work-02736 | silver_only (ambos) |

## Aislamiento del anchoring — verificado en el código del driver

`adjudication_driver.py` construye el payload del adjudicador exclusivamente desde `build_cluster_input()` (título/abstract/año/venue/topics/identificadores canónicos — la misma fuente de metadata canónica que usa el curador primario) y `candidate_work_ids` (solo los `work_id`, sin tier/confidence/unique_contribution de MiMo). El prompt (`ADJUDICATION_RUBRIC`) no menciona a MiMo, no menciona la existencia de una clasificación previa más allá de "a separate, independent curation process already reviewed this... you are NOT told what that process decided." **Verificado por inspección directa del payload real enviado** (no solo del código): el `instructions_payload` persistido en cada `budgetcal_task_*.json`/`adjtask_*.json` contiene únicamente `rubric`, `cluster` (metadata canónica), y `candidate_work_ids` — cero campos de MiMo.

## Ejecución real

6 tareas de adjudicación (una por cluster con candidatos, agrupadas — nunca 1 request por Work), **7 requests DeepSeek reales totales** (5 clusters en 1 intento, 1 cluster en 2 intentos por `response_truncated_empty` en el primero) — dentro del cap de 12.

| cluster | attempts | terminal_valid | invocation_id(s) | input_tok | output_tok |
|---|---:|---|---|---:|---:|
| scluster-b5ba715ff2f3636f | 1 | ✅ | 263 | 39,475 | 1,416 |
| scluster-ec8115783df9722f | 1 | ✅ | 264 | 32,886 | 954 |
| scluster-b1900dc3fd6d43c2 | 1 | ✅ | 265 | 57,553 | 2,148 |
| scluster-787c72467109c079 | 1 | ✅ | 266 | 41,632 | 2,806 |
| scluster-7049df7250cc08a4 | 1 | ✅ | 267 | 33,890 | 3,641 |
| scluster-36a561ff6d2429da | 2 (1 falla, 1 ok) | ✅ | 268(fail)/269 | 41,395 | 7,706 |

**6/6 clusters terminal-valid, 100% contrato de salida exacto** (candidate_work_ids == decided work_ids en los 6, validado vía `orgctl curation validate-adjudication`).

## Decisiones (10/10, exact set cumplido)

| cluster | work_id | decisión | confianza | criterion_tags |
|---|---|---|---:|---|
| scluster-b5ba715ff2f3636f | work-00684 | KEEP | 0.95 | foundational, original_method, historical_lineage |
| scluster-b5ba715ff2f3636f | work-01370 | KEEP | 0.96 | original_method, unique_methodology, benchmark |
| scluster-b5ba715ff2f3636f | work-02895 | KEEP | 0.92 | original_method, unique_methodology, complementary |
| scluster-ec8115783df9722f | **work-03893** | **KEEP** | **0.92** | **foundational, original_method, benchmark, unique_methodology** |
| scluster-b1900dc3fd6d43c2 | work-03496 | KEEP | 0.82 | benchmark, complementary |
| scluster-787c72467109c079 | work-00594 | KEEP | 0.93 | original_method, unique_methodology, complementary |
| scluster-787c72467109c079 | work-01307 | KEEP | 0.92 | original_method, unique_methodology, complementary |
| scluster-7049df7250cc08a4 | work-02043 | KEEP | 0.80 | original_method, benchmark, complementary |
| scluster-36a561ff6d2429da | work-02134 | DISCARD | 0.82 | redundant, peripheral |
| scluster-36a561ff6d2429da | work-02736 | KEEP | 0.88 | original_method, unique_methodology |

**9 KEEP, 1 DISCARD, 0 REVIEW.**

## Test crítico — sección 19-20 del pedido

**`work-03893` ≠ DISCARD: CUMPLIDO.** El adjudicador ciego (sin ver el tier/confidence/needs_deep_review de MiMo, sin ver el veredicto CRITICAL_FALSE_NEGATIVE de la auditoría) llegó independientemente a `KEEP` con confianza 0.92, calificándolo `foundational` + `benchmark` — la misma lectura que hizo DeepSeek en R10 originalmente (P0, confianza 0.97), de forma completamente ciega e independiente. Esto es la evidencia más fuerte posible de que el guardrail funciona para el caso que motivó esta fase.

## Métricas de la sección 21 (contra `DEEPSEEK_VS_MIMO_QUALITY_AUDIT.md`)

| categoría offline | n | decisión del adjudicador |
|---|---:|---|
| CRITICAL_FALSE_NEGATIVE | 1 (work-03893) | KEEP (1/1 = **critical_FN_recall = 100%**) |
| REVIEW_NEEDED | 3 (work-01370, work-02895, work-03496) | KEEP los 3 (0/3 pasó a DISCARD — **ninguna regresión crítica conocida pasó silenciosamente**) |
| SAFE_DIFFERENCE | 4 (work-02134, work-02736, work-02043, work-01307) | DISCARD 1 (work-02134), KEEP 3 |
| sin categoría de riesgo (acuerdo silver↔silver) | 2 (work-00684 nota: en realidad era REVIEW_NEEDED, ver arriba; work-00594) | work-00594: KEEP |

`safe_discard_precision` (sobre el único DISCARD real, work-02134, que la auditoría offline ya había calificado SAFE_DIFFERENCE): **1/1 = 100%**, con la salvedad explícita de `n=1` — no es una muestra suficiente para generalizar una tasa de precisión.

`review_escalation_rate = 0/10 = 0%` — el adjudicador nunca usó `REVIEW` en esta corrida; todas sus decisiones fueron confiadas (KEEP o DISCARD), no hubo casos de incertidumbre explícita de su parte.

`KEEP rate = 90%`, `DISCARD rate = 10%`, `REVIEW rate = 0%`.

## Lectura honesta — sección 22, no usar DeepSeek como verdad absoluta

**Hallazgo importante que templa el resultado**: el adjudicador revirtió a KEEP el 90% de las 10 decisiones negativas de MiMo, incluyendo 3 de los 4 casos ya clasificados SAFE_DIFFERENCE por la auditoría offline (donde MiMo y el propio texto de DeepSeek en R10 ya sugerían redundancia legítima). Esto es consistente con un patrón ya documentado en `DEEPSEEK_VS_MIMO_QUALITY_AUDIT.md`: DeepSeek tiende a tierear de forma más generosa que MiMo en general (más P0/P1, menos degradaciones) — no es evidencia de que MiMo estuviera "equivocado" en esos 3 casos, es consistente con una diferencia de calibración/generosidad entre providers, no necesariamente con corrección objetiva superior de DeepSeek.

**Implicación estratégica real**: si esta adjudicación se desplegara tal como está diseñada (todo `silver_only`/`review_required` de MiMo pasa por DeepSeek), el efecto práctico observado en esta muestra sería que **la gran mayoría del filtrado de redundancia que MiMo intenta hacer terminaría revertido** — el sistema combinado se comportaría más como "casi todo se retiene" que como una curación negativa genuinamente dual. Esto protege exitosamente contra el false negative crítico (el objetivo principal), pero a costa de que el valor de "MiMo filtra redundancia" se diluye significativamente en la práctica, al menos en esta muestra de n=10. No se puede generalizar esta proporción exacta (90% KEEP) más allá de esta corrida específica sin repetir el experimento a mayor escala.

## Contabilidad

7 requests DeepSeek reales para Parte B (dentro del cap de 12), costo real total (suma de `provider_wallet_events` para invocation_id 263-269): $0.08646218 (ver `R10_3_MIMO_CREDIT_CALIBRATION.md` para el consolidado de toda la fase R10.3). 0 requests MiMo en esta parte (por diseño — el adjudicador nunca vuelve a tocar a MiMo).
