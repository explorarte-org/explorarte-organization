# DEEPSEEK_VS_MIMO_QUALITY_AUDIT.md

Auditoría offline de calidad, 100% sobre datos ya persistidos en Postgres (`model_invocation_results.json_output`, recuperado por `invocation_id`/`task_id`, nunca re-solicitado a ningún provider). Cero llamadas nuevas a DeepSeek o MiMo. Cero chain-of-thought almacenado o expuesto — todo el análisis usa exclusivamente `tier`, `confidence`, `needs_deep_review`, `redundancy_group`, `unique_contribution` (que es ya la conclusión final del modelo, nunca su razonamiento interno) y metadata estructural (`finish_reason`, `output_tokens`, `max_output_tokens`, `error_code`).

## 0. Cluster_id contract audit (punto 1 del pedido)

**Hallazgo: NO hay bug. El invariante ya está correctamente implementado desde R9.1 fix 2, con test de regresión ya existente.**

Código real revisado: `internal/corpuscuration/corpuscuration_output_contract.go`, función `ValidateCurationOutputContract`. El campo `ClusterIDMismatch` se marca (`violation.ClusterIDMismatch = true`) cuando `output.ClusterID != expectedClusterID`, pero **nunca establece `found = true` por sí solo** — es explícitamente diagnóstico/informacional (comentario en el código, línea ~62: *"R9.1 fix 2: cluster_id removed from generative responsibility... A mismatch here is recorded for audit/telemetry but never causes found=true / a rejection on its own"*). El único mecanismo real de identidad de cluster es la igualdad exacta del conjunto de `work_id` (`expected == seen`), independiente de lo que el modelo escriba en `cluster_id`.

Test de regresión ya existente: `internal/corpuscuration/corpuscuration_output_contract_test.go:100`, `TestValidateCurationOutputContract_ClusterIDMismatchAloneIsAcceptedButNoted` — verifica exactamente que un `cluster_id` incorrecto SIN violaciones de Work set es **aceptado**. No se necesita agregar ningún test nuevo; el invariante pedido ya está codificado y probado.

**Verificación de que los rechazos reales de los smokes MiMo fueron por Work faltante, no por `cluster_id`:**
- Cluster `scluster-787c72467109c079` (invocación 237, primer intento del canario de 15): `curation_output_contract_violation` = `{cluster_id_mismatch: true, missing_work_ids: ["work-02601"]}` — `found=true` fue disparado por `missing_work_ids`, no por el mismatch.
- Cluster `scluster-a16533182030ccd4` (invocación 245, smoke4 intento 1): mismo patrón — `{cluster_id_mismatch: true, missing_work_ids: ["work-03174"]}`.

Ambos casos: el `cluster_id_mismatch` está presente pero es 100% no-causal en la decisión de rechazo — confirmado leyendo el código real, no inferido. **Conclusión: no se corrige nada, no se repite el canario, se documenta el hallazgo (este mismo).**

## 1. Paired Quality Set

11 clusters válidos en AMBOS providers (subconjunto exacto de los 15 clusters de R10 que DeepSeek terminó `terminal-valid`; MiMo también los completó todos): `scluster-ee06984a5f0f49d5`, `scluster-b5ba715ff2f3636f`, `scluster-da421180ed2349f2`, `scluster-ec8115783df9722f`, `scluster-b1900dc3fd6d43c2`, `scluster-ba5e63822655c3d4`, `scluster-787c72467109c079`, `scluster-7049df7250cc08a4`, `scluster-36a561ff6d2429da`, `scluster-ff2a66fd75f35298`, `scluster-164835480d9c2ce2`.

**73 Works pareados** (mismo conjunto exacto de `work_id` en ambos providers en cada uno de los 11 clusters — verificado programáticamente, 0 mismatches de Work set).

### Transition matrix (ds_tier → mm_tier, n=73)

| DeepSeek \ MiMo | P0 | P1 | silver_only | review_required |
|---|---:|---:|---:|---:|
| **P0** | 8 | 11 | 2 | 2 |
| **P1** | 5 | 37 | 4 | 0 |
| **silver_only** | 0 | 2 | 1 | 1 |
| **review_required** | 0 | 0 | 0 | 0 |

**Acuerdo exacto de tier: 46/73 = 63.0%.** 27 desacuerdos, de los cuales la mayoría (16) son movimientos P0↔P1 (adyacentes, ambos tiers "retener" en la semántica del sistema) — el desacuerdo de tier adyacente entre dos providers curando de forma independiente sobre un rubric subjetivo es esperable y no es por sí mismo evidencia de falla; lo que sí importa es la dirección hacia tiers de descarte, auditada en la sección 2.

## 2. High-risk transitions (DeepSeek P0/P1 → MiMo silver_only/review_required)

8 casos totales, todos en clusters distintos, clasificados individualmente contra los criterios de la sección 6 del pedido (foundational/original/benchmark/unique methodology/historically important/only representative):

| cluster | work_id | ds_tier→mm_tier | ds_conf/mm_conf | mm_ndr | Veredicto |
|---|---|---|---|---|---|
| scluster-ec8115783df9722f | work-03893 | P0→silver_only | 0.97/0.80 | **False** | **CRITICAL_FALSE_NEGATIVE** |
| scluster-b1900dc3fd6d43c2 | work-03496 | P0→silver_only | 0.95/0.70 | True | REVIEW_NEEDED |
| scluster-b5ba715ff2f3636f | work-01370 | P0→review_required | 0.93/0.60 | True | REVIEW_NEEDED |
| scluster-b5ba715ff2f3636f | work-02895 | P0→review_required | 0.90/0.65 | True | REVIEW_NEEDED |
| scluster-36a561ff6d2429da | work-02134 | P1→silver_only | 0.80/0.80 | False | SAFE_DIFFERENCE |
| scluster-36a561ff6d2429da | work-02736 | P1→silver_only | 0.85/0.80 | False | SAFE_DIFFERENCE |
| scluster-7049df7250cc08a4 | work-02043 | P1→silver_only | 0.95/0.80 | False | SAFE_DIFFERENCE |
| scluster-787c72467109c079 | work-01307 | P1→silver_only | 0.88/0.80 | False | SAFE_DIFFERENCE |

### Detalle del CRITICAL_FALSE_NEGATIVE

**`scluster-ec8115783df9722f` / `work-03893`**: DeepSeek lo tiereó P0 con confianza 0.97, describiéndolo como *"the canonical empirical benchmark across five memory stores"* — cumple el criterio explícito **"benchmark"** de la sección 6. MiMo lo bajó a `silver_only` con confianza 0.80 (alta, no marginal) y **sin flag `needs_deep_review`** — es decir, MiMo demuestra en su propio `unique_contribution` (*"Empirical study showing memory stores have limited effect on new problems, with Git recommended for auditability, history, and merging agent memories"*) que **comprendió correctamente el hallazgo central del paper**, pero lo clasificó como descartable, con confianza, sin señalizar incertidumbre. En un flujo downstream que solo promueve P0/P1 a Silver completo, este paper se perdería silenciosamente. Este es el único caso de los 8 donde MiMo está simultáneamente (a) en desacuerdo de dos escalones de prioridad con DeepSeek, (b) confiado en su propio juicio, y (c) sobre un paper que cumple un criterio explícito de importancia estructural (benchmark). **Cuenta como 1 regresión de calidad real, no se descarta.**

### Detalle de los REVIEW_NEEDED (3 casos)

Los 3 casos restantes de mayor magnitud (`work-03496`, `work-01370`, `work-02895`) comparten un patrón distinto: MiMo bajó confianza sustancialmente (0.60-0.70 vs 0.90-0.95 de DeepSeek) **y** se auto-marcó `needs_deep_review=True` en los tres. Esto es el mecanismo de salvaguarda funcionando como está diseñado — MiMo no está "seguro y equivocado" en estos tres casos, está expresando incertidumbre real y enrutando a revisión humana en vez de decidir silenciosamente. No se cuentan como false negative crítico porque el sistema de flags los captura correctamente, pero merecen revisión humana antes de confiar en el tier asignado (motivo explícito por el que existe el flag).

### Detalle de los SAFE_DIFFERENCE (4 casos)

Los 4 casos restantes son degradaciones P1→silver con confianza estable en ambos providers (sin flag de incertidumbre) y una justificación de MiMo consistente con juicio de redundancia dentro del cluster (p.ej. *"similar to other pipelined works"*, *"redundant with foundational MLA method"*) — en los 4 casos, la propia descripción de DeepSeek ya encuadraba el Work como *"complementary"* o *"baseline"* más que como aporte fundacional independiente. Es un desacuerdo legítimo de agrupamiento/redundancia, no un caso donde MiMo perdió el valor real del trabajo.

## 3. MiMo silver/review audit — Gate principal de calidad

Total MiMo `silver_only=7` + `review_required=3` = 10 Works en todo el canario de 15 clusters.

**Los 10 están 100% dentro del set pareado (73 Works).** Los 3 clusters recuperados (15 Works: `scluster-a16533182030ccd4`, `scluster-40c4d59b6cfd6490`, `scluster-aac0f99841969e76`) tienen **0 `silver_only`, 0 `review_required`** — cobertura completa en P0/P1 únicamente (verificado directamente en `tier_counts` de cada cluster).

**`critical_false_negative_count = 1`** (ver sección 2). De los 10 Works silver/review: 1 critical_false_negative, 3 review_needed (auto-flageados, legítimos), 4 safe_difference (redundancia), y 2 adicionales (`work-00684` silver→review, `work-02394` de `da421180ed2349f2` silver→P1 — este último es una MEJORA de tier, no un riesgo) sin patrón de riesgo.

## 4. needs_deep_review analysis — 8 flags MiMo

| cluster | work_id | mm_tier | conf | Clasificación |
|---|---|---|---:|---|
| scluster-b5ba715ff2f3636f | work-00684 | review_required | 0.50 | MiMo_more_conservative (DeepSeek también lo tiereó bajo: silver_only, sin flag — acuerdo direccional) |
| scluster-b5ba715ff2f3636f | work-01370 | review_required | 0.60 | MiMo_more_conservative (ver critical/review sección 2) |
| scluster-b5ba715ff2f3636f | work-02895 | review_required | 0.65 | MiMo_more_conservative (ver critical/review sección 2) |
| scluster-b1900dc3fd6d43c2 | work-02671 | P1 | 0.75 | legitimate_ambiguity (mismo tier que DeepSeek, P1, solo con menos confianza) |
| scluster-b1900dc3fd6d43c2 | work-03406 | P1 | 0.75 | legitimate_ambiguity (mismo tier que DeepSeek, P1) |
| scluster-b1900dc3fd6d43c2 | work-03496 | silver_only | 0.70 | possible_quality_problem (ver REVIEW_NEEDED sección 2) |
| scluster-aac0f99841969e76 | work-01893 | P1 | 0.72 | recovered_cluster_only (sin comparación DeepSeek posible — DeepSeek falló todo el cluster) |
| scluster-aac0f99841969e76 | work-01940 | P1 | 0.70 | recovered_cluster_only (ídem) |

`also_uncertain_in_DeepSeek`: **0 coincidencias exactas** — pero nota relevante en dirección inversa: DeepSeek marcó `needs_deep_review=True` en 1 Work del set pareado (`scluster-787c72467109c079`/`work-02601`) donde MiMo **no** flageó incertidumbre (tier P1 en ambos, de acuerdo, MiMo con confianza 0.85 vs 0.82 de DeepSeek) — MiMo fue más decisivo, no menos, en el único caso donde DeepSeek dudó. Es la nota exacta que ya documentaba `R10_CANARY_REPORT.md` como "mismo patrón que r8/r9/r9.1" — la incertidumbre de DeepSeek en este work_id específico es consistente a través de corridas.

**Conclusión del gate: más `needs_deep_review` NO se traduce en peor calidad.** 5 de los 8 flags son legítima cautela bien dirigida (3 hacia el único critical/review real, 2 hacia clusters recuperados sin contraparte de comparación); ninguno de los 8 flags está asociado a un caso donde MiMo estuviera equivocado con confianza.

## 5. Recovered coverage audit — los 3 clusters que DeepSeek perdió

| cluster | Works | completeness | tiers | needs_deep_review | redundancy |
|---|---:|---|---|---:|---|
| scluster-a16533182030ccd4 | 8/8 exactos | ✅ contrato exacto | P0:2 P1:6 | 0 | 4 grupos de redundancia distintos, sin colapso indebido observado |
| scluster-40c4d59b6cfd6490 | 2/2 exactos | ✅ contrato exacto | P0:1 P1:1 | 0 | cluster mínimo, sin señal de riesgo |
| scluster-aac0f99841969e76 | 5/5 exactos | ✅ contrato exacto | P0:1 P1:4 | 2 (legítima cautela, ambos tiereados P1 positivamente, no descartados) | sin colapso indebido observado |

Completitud exacta de Works en los 3 (15/15 Works totales, 0 faltantes, 0 duplicados, 0 tier inválido — contrato de salida cumplido al 100%). Tiers plausibles: ninguno de los 15 Works cayó en `silver_only`/`review_required` — 100% P0/P1. **Sin evidencia de falso negativo crítico en la cobertura recuperada.** Estos 3 clusters representan una ventaja de cobertura real de MiMo (contenido que DeepSeek nunca llegó a tierear en absoluto), pero — por instrucción explícita del pedido — **no forman parte del tier-paired comparison** de la sección 1-2, solo se reportan como cobertura adicional.

## 6. Common failure — `scluster-9f9da5855df592ce`

Análisis offline exclusivamente estructural (`model_provider_outcomes`), cero contenido de reasoning almacenado o expuesto.

**DeepSeek** (invocación 218, 219 en R10): ambos intentos `response_truncated_empty`, `finish_reason=length`, `output_tokens == max_output_tokens` (3000/2999) — el modelo agotó el presupuesto de tokens antes de emitir contenido JSON válido/completo.

**MiMo** (invocación 259, 260 en R10.2): mismo patrón estructural en ambos intentos —
- Intento 1 (id 259): `outcome_classification=provider_rejected`, `error_code=response_truncated_empty`, `finish_reason=length`, `output_tokens=3000=max_output_tokens` exacto → **idéntico modo de fallo al de DeepSeek**: presupuesto agotado antes de contenido válido, no una etiqueta distinta como reportó erróneamente el campo `error` del driver local en el registro crudo (ver nota abajo).
- Intento 2 (id 260): `outcome_classification=response_received` (JSON parseable a nivel de sintaxis básica, pasó el primer filtro), `finish_reason=length`, `output_tokens=3000=max_output_tokens` exacto — el validador de dominio (`ValidateCurationOutputContract`, vía el CLI) determinó que el JSON estaba incompleto/mal formado tras el corte, consistente con **estructura JSON inacabada por truncamiento a mitad de generación** (unfinished structure), no con contaminación de markdown (el prompt anti-fence estaba activo, y `response_format=json_object` estaba fijado en ambos intentos) ni con corrupción de sintaxis no relacionada al truncamiento.

**Nota de discrepancia**: el registro `canary15_results.jsonl` local etiquetó este fallo con el string `"model runtime: model response rejected: invalid JSON response ... error_code=response_normalization_failed"` — ese es el mensaje de más alto nivel que el runtime devolvió al driver, pero la clasificación real y más específica persistida en `model_provider_outcomes.error_code` para el intento 1 es `response_truncated_empty` (idéntica a DeepSeek). Documentado aquí como corrección de la caracterización anterior ("falla distinta" era impreciso) — **es el mismo modo de fallo raíz (truncamiento por presupuesto) en ambos providers**, solo que el segundo intento de MiMo llegó un paso más lejos (produjo bytes de JSON parcial en vez de vacío) antes de fallar la validación de dominio.

**Causa mecánica probable (documentada, no confirmada sin acceso a reasoning_content, que nunca se persiste)**: el cluster tiene solo 2 Works — el `max_output_tokens` calculado por la fórmula de escalado (`max(3000, min(16000, 600+1200*n_works))`) da el piso mínimo, 3000, para clusters de 1-2 Works. La sección E de `MIMO_V25_INTEGRATION_AUDIT.md` ya documentó, con evidencia empírica, que el modo `thinking` de MiMo (habilitado por defecto) consume `completion_tokens` reales antes de la respuesta final — un presupuesto de 3000 diseñado sin margen para razonamiento es la explicación estructural más simple y ya anticipada por el propio audit, consistente con que DeepSeek (que no tiene el mismo modo de razonamiento por defecto) también falla exactamente en el mismo piso de presupuesto mínimo. No se puede confirmar con certeza sin inspeccionar `reasoning_content` (que el sistema correctamente nunca persiste) — se documenta como hipótesis estructural razonable, no como hecho verificado.

## 7. Resumen del gate de calidad

- **Critical false negatives: 1** (`scluster-ec8115783df9722f`/`work-03893`), confirmado con evidencia directa (confianza alta de ambos providers, sin flag de incertidumbre de MiMo, paper explícitamente calificado "benchmark" por DeepSeek).
- **Cobertura recuperada (3 clusters, 15 Works): 0 critical false negatives**, contrato exacto, 100% P0/P1.
- **Falla común (1 cluster): mismo modo de fallo raíz en ambos providers** (truncamiento por presupuesto), MiMo con un intento adicional que produjo JSON parcial en vez de vacío — sin ventaja ni desventaja clara.
- **Acuerdo de tier en el set pareado: 63.0%**, mayoría de desacuerdos son movimientos adyacentes P0↔P1, no degradaciones de riesgo.
