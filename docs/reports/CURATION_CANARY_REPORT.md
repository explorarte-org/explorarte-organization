# CURATION_CANARY_REPORT.md

Canario de 15 clusters heterogéneos, curación gobernada vía DeepSeek V4 Flash (`department_worker` scope, lifecycle real: task→claim→start→assignment→context build→invocation→dispatch→result→finalize→accounting).

Run: `r8` (config congelada: `rubric_version=v1`, `cluster_algorithm_version=gemini-embedding-2-average-link-v1-threshold-0.88`, `model_route=deepseek/deepseek-v4-flash`).
Ventana real: 2026-08-11 05:05:35 UTC → 05:25:43 UTC (1207s / ~20.1 min).
Costo real committed: **$0.12337108** (9 invocaciones exitosas). Reservado total (incl. fallidos): $0.15478106.

**Hallazgo crítico que domina este reporte**: no fueron 15 dispatches limpios a 15 clusters distintos. Hubo un bug real de identidad de tareas (sección 2.1) que hizo que 2 clusters seleccionados nunca fueran enviados al modelo, y que 3 de los resultados "exitosos" quedaran etiquetados bajo el índice equivocado (contenido correcto, etiqueta incorrecta). Todo lo demás en este reporte se construyó corrigiendo esa mala etiqueta contra la identidad real devuelta por el propio modelo (`curation_output.cluster_id`) y contra la cadena de `claim_mismatch` registrada por el driver.

---

## 1. CONTABILIDAD EXACTA

### 1.1 Tabla por índice de ejecución (como se lanzó)

| idx | grupo seleccionado | cluster_id seleccionado | size real | task creado | task usado (claim) | attempt_id | invocation_id(s) | input_hash | resultado |
|---|---|---|---|---|---|---|---|---|---|
| 1 | rag | scluster-ee06984a5f0f49d5 | 2 | 100 | 100 (propio) | 85 | 129 | 6bda6155… | FAILED (`response_truncated_empty`) |
| 2 | rag | scluster-b5ba715ff2f3636f | 6 | 101 | 101 (propio) | 86 | 130 | b2ed493f… | OK |
| 3 | rag | scluster-da421180ed2349f2 | 18 | 102 | 102 (propio) | 87 | 131 | bb7b5d9d… | OK |
| 4 | memory | scluster-ec8115783df9722f | 2 | 103 | 103 (propio) | 88 | 132, 133 | 9058779e… | FAILED (`response_normalization_failed`, 2 intentos) |
| 5 | memory | scluster-a16533182030ccd4 | 8 | 104 | 104 (propio) | 89 | 134 | f1b1bbcd… | OK (**incompleto**, ver 1.3) |
| 6 | memory | scluster-b1900dc3fd6d43c2 | 16 | 105 | 105 (propio) | 90 | 135 | 005e6281… | OK |
| 7 | context | scluster-ba5e63822655c3d4 | 2 | 106 | 106 (propio) | 91 | 136 | 8d0a3d24… | OK (1 duplicado determinístico colapsado) |
| 8 | context | scluster-787c72467109c079 | 8 | 107 | 107 (propio) | 92 | 137 | d8890609… | OK |
| 9 | efficiency | scluster-7049df7250cc08a4 | 3 | 108 | 108 (propio) | 93 | 138, 139 | 76d06eee… | FAILED (`response_truncated_empty`, 2 intentos) |
| 10 | efficiency | scluster-36a561ff6d2429da | 7 | 109 | 109 (propio) | 94 | 140, 141 | 4b5fe8cc… | FAILED (`response_truncated_empty`, 2 intentos) |
| 11 | symbolic | scluster-40c4d59b6cfd6490 | 2 | 110 | **100** (ajeno, ver 2.1) | 95 | 142 | b6a9e2d0… | OK, pero **contenido real = cluster idx1 (rag)**, no symbolic |
| 12 | symbolic | scluster-aac0f99841969e76 | 5 | 111 | **110** (ajeno) | 96 | 143 | 23e00dce… | OK, pero **contenido real = cluster idx11/symbolic (40c4d59b6cfd6490)** |
| 13 | agents | scluster-ff2a66fd75f35298 | 2 | 112 | **111** (ajeno) | 97 | 144, 145 | 0adf5da4… | FAILED, pero **contenido real = cluster idx12/symbolic (aac0f99841969e76)** |
| 14 | agents | scluster-164835480d9c2ce2 | 8 | 113 | **112** (ajeno) | 98 | 146 | 63fb1c5d… | OK, pero **contenido real = cluster idx13/agents (ff2a66fd75f35298)** |
| 15 | edge | scluster-9f9da5855df592ce | 2 | 114 | **103** (ajeno) | 99 | 147, 148 | 458921b0… | FAILED, pero **contenido real = cluster idx4/memory (ec8115783df9722f), otra vez** |

### 1.2 Tabla corregida por identidad real de cluster (la que importa)

| cluster real | tema real | size | Works enviados | duplicados colapsados | resultado real | dónde quedó etiquetado |
|---|---|---|---|---|---|---|
| scluster-ee06984a5f0f49d5 | rag | 2 | 2 | 0 | **FALLÓ 1ra vez (idx1), ÉXITO 2da vez (idx11)** | idx1 (fail) + idx11 (éxito real) |
| scluster-b5ba715ff2f3636f | rag | 6 | 6 | 0 | ÉXITO limpio | idx2 |
| scluster-da421180ed2349f2 | rag | 18 | 18 | 0 | ÉXITO limpio | idx3 |
| scluster-ec8115783df9722f | memory | 2 | 2 | 0 | **FALLÓ 4 veces en total (idx4 x2, idx15 x2), nunca tuvo éxito** | idx4 + idx15 |
| scluster-a16533182030ccd4 | memory | 8 | 8 | 0 | ÉXITO pero **incompleto** (7/8 tiered) | idx5 |
| scluster-b1900dc3fd6d43c2 | memory | 16 | 16 | 0 | ÉXITO limpio | idx6 |
| scluster-ba5e63822655c3d4 | context | 2 | 1 | 1 (work-01212→work-00195) | ÉXITO limpio | idx7 |
| scluster-787c72467109c079 | context | 8 | 8 | 0 | ÉXITO limpio | idx8 |
| scluster-7049df7250cc08a4 | efficiency | 3 | 3 | 0 | FALLÓ, 2 intentos | idx9 |
| scluster-36a561ff6d2429da | efficiency | 7 | 7 | 0 | FALLÓ, 2 intentos | idx10 |
| scluster-40c4d59b6cfd6490 | symbolic | 2 | 2 | 0 | **ÉXITO real, pero etiquetado como idx12** | idx12 |
| scluster-aac0f99841969e76 | symbolic | 5 | ~5 (no confirmable, respuesta vacía) | ? | **FALLÓ, contenido vacío, etiquetado como idx13** | idx13 |
| scluster-ff2a66fd75f35298 | agents | 2 | 2 | 0 | **ÉXITO real, pero etiquetado como idx14** | idx14 |
| scluster-164835480d9c2ce2 | agents | 8 | — | — | **NUNCA DESPACHADO** (task 113 creado, jamás reclamado por nadie) | ninguno |
| scluster-9f9da5855df592ce | edge | 2 | — | — | **NUNCA DESPACHADO** (task 114 creado, jamás reclamado por nadie) | ninguno |

### 1.3 Resultado total (Works efectivamente tiered, sobre los 9 dispatches con `succeeded`)

Works enviados (deduped) en los 9 dispatches exitosos: **63**
Works efectivamente tiered en el output del modelo: **62**
Gap: **1 Work** — `work-03889` ("MemRefine: LLM-Guided Compression for Long-Term Agent Memory", cluster memory `scluster-a16533182030ccd4`) fue parte del payload enviado pero **no aparece en absoluto en el `works[]` de salida** del modelo. No fue tierado como nada — ni P0 ni P1 ni silver_only ni review_required. Esto es un incumplimiento de contrato del curator, no una decisión de tier. Se reporta explícitamente, no se oculta.

| Tier | Count |
|---|---|
| P0 | 21 |
| P1 | 32 |
| silver_only | 8 |
| review_required | 1 |
| **Suma** | **62** |
| needs_deep_review=true | 1 (el mismo Work marcado review_required, cluster context `scluster-787c72467109c079`) |

**21+32+8+1 = 62, cuadra exactamente contra los 62 Works efectivamente tiered** (no contra los 63 enviados — el faltante de 1 Work queda documentado arriba, no absorbido silenciosamente en ningún total).

---

## 2. INFRAESTRUCTURA

### 2.1 Hallazgo crítico: bug de reclamo de tareas en cascada

Causa raíz observada (no completamente diagnosticada a nivel de código en esta sesión — se documenta el comportamiento observado, se recomienda diagnóstico de código antes de la próxima canary):

Cuando el dispatch de un cluster termina en `FAILED_AFTER_RETRIES`, el driver (`canary15_driver_v2.py`) nunca llama `task result`/`task finalize` en la rama de fallo — solo lo hace en la rama de éxito. La tarea del cluster fallido queda en un estado reclamable (`ready`/similar) en vez de transicionar a un estado terminal (`dead_letter`/`failed`). El siguiente `task claim --batch 1` del loop, que se ejecuta segundos después (bien dentro de la ventana del lease de 14 min), reclama esa tarea vieja en vez de la recién creada para el cluster actual — el `task claim` no está pidiendo por ID, está pidiendo "la próxima disponible", y la cola FIFO entrega la más vieja primero.

Efecto observado: una vez que el primer cluster falló (idx1, cluster rag), el desfase se propagó en cascada — cada índice posterior desde el 11 en adelante terminó despachando el contenido del índice/tarea **anterior** en vez del propio, confirmado por 3 vías independientes: (a) el campo `claim_mismatch` que el propio driver ya registraba, (b) el `cluster_id` que el modelo devuelve dentro de su propio JSON de salida (el rubric le pide ecoarlo), que coincide exactamente con el cluster anterior, no con el seleccionado para ese índice, y (c) el contenido semántico de las `unique_contribution` (p. ej. "distracting-passage failure mode in RAG" bajo la etiqueta "symbolic" del idx11).

**Consecuencia real**: 2 de los 15 clusters seleccionados (`scluster-164835480d9c2ce2` agents, `scluster-9f9da5855df592ce` edge) nunca llegaron a DeepSeek — sus tareas quedaron creadas y huérfanas (114, 113), sin que ningún índice posterior las reclamara (no hubo un índice 16 para cerrar el ciclo). Esto **no es un fallo de calidad del modelo**, es un gap de infraestructura en el driver de canary (no en el runtime de producción `internal/tasks`/`internal/modelruntime`, que se comportó exactamente como está diseñado: entregó la tarea más vieja disponible cuando se le pidió una).

**Antes de correr cualquier canary adicional o Silver completo, esto debe corregirse**: el driver debe (a) finalizar explícitamente como `failed`/`dead_letter` toda tarea que agote sus reintentos, y/o (b) verificar que el `task_id` reclamado coincida con el creado y abortar/reintentar con una nueva idempotency key si no coincide, en vez de continuar silenciosamente "usando el reclamado como fuente de verdad" (que es lo que el driver hacía, registrado honestamente en `claim_mismatch`, pero sin que eso impidiera que la etiqueta del reporte quedara mal).

### 2.2 Checklist por cluster real (13 de 15 con al menos un dispatch real; 2 nunca despachados)

| cluster real | task create | claim | start | context build | assignment | authz | invocation create | egress | DeepSeek reached | result | finalize | accounting | audit |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| rag ee0698… | OK | OK (propio, 1er intento) / OK (ajeno, 2do) | OK | OK | OK | OK | OK | OK | Sí (2 veces) | 1er: no aplica (fail); 2do: OK | 2do: OK | 2do: OK | OK |
| rag b5ba71… | OK | OK | OK | OK | OK | OK | OK | OK | Sí | OK | OK | OK | OK |
| rag da4211… | OK | OK | OK | OK | OK | OK | OK | OK | Sí | OK | OK | OK | OK |
| memory ec8115… | OK | OK (propio x2, ajeno x2 vía idx15) | OK | OK | OK | OK | OK | OK | Sí (4 veces) | No (siempre fail) | No | **Reservas 133/148 nunca liberadas** (ver 2.3) | Parcial |
| memory a16533… | OK | OK | OK | OK | OK | OK | OK | OK | Sí | OK (parcial, ver 1.3) | OK | OK | OK |
| memory b1900d… | OK | OK | OK | OK | OK | OK | OK | OK | Sí | OK | OK | OK | OK |
| context ba5e63… | OK | OK | OK | OK | OK | OK | OK | OK | Sí | OK | OK | OK | OK |
| context 787c72… | OK | OK | OK | OK | OK | OK | OK | OK | Sí | OK | OK | OK | OK |
| efficiency 7049df… | OK | OK | OK | OK | OK | OK | OK | OK | Sí (2 veces) | No | No | Reservas liberadas OK | Parcial |
| efficiency 36a561… | OK | OK | OK | OK | OK | OK | OK | OK | Sí (2 veces) | No | No | Reservas liberadas OK | Parcial |
| symbolic 40c4d5… | OK | OK (ajeno) | OK | OK | OK | OK | OK | OK | Sí | OK | OK | OK | OK |
| symbolic aac0f9… | OK | OK (ajeno) | OK | OK | OK | OK | OK | OK | Sí (2 veces) | No (respuesta vacía) | No | **Reserva 133-equiv nunca liberada** (ver 2.3, invocation 148 es el equivalente en la otra cadena; para esta cadena específica: invocation 144/145 sí liberadas) | Parcial |
| agents ff2a66… | OK | OK (ajeno) | OK | OK | OK | OK | OK | OK | Sí | OK | OK | OK | OK |
| agents 164835… | OK | **Nunca reclamado** | — | — | — | — | — | — | **No** | — | — | — | — |
| edge 9f9da5… | OK | **Nunca reclamado** | — | — | — | — | — | — | **No** | — | — | — | — |

### 2.3 Accounting incompleto — segundo hallazgo real

Las invocaciones que terminaron con `error_code=response_normalization_failed` (JSON inválido, invocaciones 133 y 148) **quedaron con su reserva (`reserved`) sin un evento `released` correspondiente** — se verificó directamente contra `provider_wallet_events`. Las que terminaron con `response_truncated_empty` (129, 132, 138, 139, 140, 141, 144, 145, 147) sí tienen su `released` correcto. Monto atrapado en limbo: **$0.01280706** (2 invocaciones × ~$0.0064 cada una) — ni comprometido ni liberado. No es una pérdida de dinero real (el provider nunca cobró por esas dos llamadas, `provider_reported=false` para ambas), pero es un gap de contabilidad/reconciliación: el balance de la reserva del proyecto queda inconsistente hasta que algo libere esas dos filas. Se reporta como hallazgo de infraestructura, no se corrigió en esta sesión (instrucción explícita de no implementar).

### 2.4 Resumen numérico

- successful clusters (identidad real, dispatch real, terminal `succeeded`): **9 / 15 seleccionados**
- provider failures reales (dispatch llegó a DeepSeek, la respuesta falló): **4 clusters** (memory ec8115…, efficiency 7049df…, efficiency 36a561…, symbolic aac0f9…)
- nunca despachados (bug de infraestructura, no fallo de proveedor): **2 clusters** (agents 164835…, edge 9f9da5…)
- schema/local validation failures: 0 — ningún fallo fue por validación de schema local; todos los fallos fueron `response_truncated_empty` (contenido vacío del proveedor) o `response_normalization_failed` (JSON inválido del proveedor) — ambos **failures post-provider** (DeepSeek respondió, pero mal), distintos de `response_read_failed` (el bug de timeout de 30s de la sesión anterior, **0 ocurrencias en r8** — confirmado resuelto).
- context_drift failures: 0 observadas.
- lease expirations: 0 observadas explícitamente como error, pero el mecanismo de la sección 2.1 depende implícitamente de que una tarea vieja se vuelva reclamable antes de que su lease nominal de 14 min expire — esto sugiere que el estado de la tarea cambió a reclamable por otra vía (probablemente el propio fallo del dispatch dentro del ciclo de attempts de `internal/tasks`, no por expiración de lease) — **no diagnosticado a fondo, señalado como pendiente**.
- dead_letters: 0 — ninguna tarea transicionó a `dead_letter`; las fallidas quedaron reclamables en vez de morir, que es exactamente el bug de 2.1.
- retries: 9 dispatches usaron su único intento; 6 dispatches fallidos consumieron 2 intentos cada uno donde el driver lo permitió (excepto los 2 nunca despachados, que consumieron 0).

---

## 3. COSTO REAL

Fuente: `provider_wallet_events` (contabilidad real, no estimación) + `model_invocation_usage` (`provider_reported=true` en las 9 exitosas).

| idx (etiqueta) | cluster real | input_tokens | output_tokens | cache hit/miss | provider_cost committed | latencia (s) |
|---|---|---|---|---|---|---|
| 2 | rag b5ba71… | 82,947 | 7,283 | no capturado | $0.01365182 | 67.3 |
| 3 | rag da4211… | 101,040 | 11,021 | no capturado | $0.01723148 | 93.4 |
| 5 | memory a16533… | 84,958 | 7,523 | no capturado | $0.01400056 | 77.6 |
| 6 | memory b1900d… | 101,089 | 15,362 | no capturado | $0.01845382 | 137.6 |
| 7 | context ba5e63… | 73,079 | 2,175 | no capturado | $0.01084006 | 24.0 |
| 8 | context 787c72… | 85,121 | 9,910 | no capturado | $0.01469174 | 84.2 |
| 12 (real: symbolic 40c4d5…) | symbolic 40c4d5… | 75,622 | 2,827 | no capturado | $0.01137864 | 30.4 |
| 14 (real: agents ff2a66…) | agents ff2a66… | 76,413 | 3,106 | no capturado | $0.01156750 | 32.6 |
| — (idx11, real: rag ee0698…) | rag ee0698… | 75,871 | 3,334 | no capturado | $0.01155546 | 37.8 |

**`prompt_cache_hit_tokens`/`prompt_cache_miss_tokens`: NO se capturan actualmente.** `model_invocation_usage` solo tiene `input_tokens`/`output_tokens`/`total_tokens`/`provider_reported` — no hay columnas de cache. `internal/modelpricing.PriceTier` sí tiene capacidad de precio distinto para `CachedInputPriceNanosPerMillion`, pero nada en el pipeline de accounting de esta sesión reporta cuántos tokens del input real fueron cache-hit vs cache-miss. Gap real, a resolver antes de optimizar costo por caching.

### Totales (9 dispatches exitosos, reales)

- total input tokens: **756,140**
- total output tokens: **62,541**
- total provider cost (committed): **$0.12337108**
- mean cost/cluster: **$0.01371**
- median cost/cluster: **$0.01365** (el 5º de 9 valores ordenados)
- p95 cost/cluster: no estadísticamente significativo con n=9; el máximo observado es **$0.01845** (rag da4211…/memory b1900d…, los dos clusters más grandes en Works, 16-18)
- mean latency: **65.0 s**
- p95 latency: no significativo con n=9; máximo observado **137.6 s** (memory b1900d…, 16 Works, 15,362 tokens de output)

### Costo por unidad de valor

- cost per Work evaluated (62 Works efectivamente tiered): **$0.12337108 / 62 ≈ $0.00199 por Work**
- cost per P0/P1 retenido (21+32=53 Works retenidos): **$0.12337108 / 53 ≈ $0.00233 por P0/P1**
- cost per accepted/usable curation outcome (contando los 9 dispatches exitosos como "outcomes usables", ignorando el fallo interno de cluster5): **$0.12337108 / 9 ≈ $0.01371 por cluster curado**

Esto confirma numéricamente lo que ya sospechábamos del smoke test: el costo de tokens en sí es bajo (~$0.002/Work), el problema real de eficiencia no es el precio por token sino la sobrecarga de contexto fijo por llamada (sección 4).

---

## 4. CONTEXT WASTE

Comparando clusters de tamaño extremo (1 Work efectivo vs 18 Works efectivos):

- cluster context ba5e63… (1 Work tras dedup): **73,079 input tokens**
- cluster rag da4211… (18 Works): **101,040 input tokens**

Diferencia: 27,961 tokens para 17 Works adicionales de contenido real ≈ **~1,645 tokens/Work marginal**.

Repitiendo el cálculo marginal sobre los otros 7 puntos de datos (asumiendo que el "piso" fijo es el intercepto de una regresión lineal simple contra el número de Works enviados):

| cluster | Works | input_tokens | marginal tokens/Work sobre el piso de ~73,000 |
|---|---|---|---|
| context ba5e63… | 1 | 73,079 | — (referencia) |
| symbolic 40c4d5… | 2 | 75,622 | ~1,271 |
| rag ee0698… | 2 | 75,871 | ~1,396 |
| agents ff2a66… | 2 | 76,413 | ~1,667 |
| memory a16533… | 8 | 84,958 | ~1,485 |
| context 787c72… | 8 | 85,121 | ~1,505 |
| rag b5ba71… | 6 | 82,947 | ~1,645 |
| memory b1900d… | 16 | 101,089 | ~1,751 |
| rag da4211… | 18 | 101,040 | ~1,553 |

**Conclusión cuantitativa**: el input de cada invocación tiene un **piso fijo de ~73,000 tokens** que no depende del tamaño del cluster, más un costo marginal muy consistente de **~1,400-1,750 tokens por Work** del propio payload de curación. Para el cluster más pequeño posible del canario (1 Work), ese piso fijo representa **~99.9% del input total**. Para el cluster más grande (18 Works), sigue representando **~72% del input total**.

No se identificaron aún los sub-segmentos exactos dentro de ese piso fijo (eso requeriría inspeccionar el render completo del context snapshot, que no se hizo en esta sesión — se determinó el tamaño total, no su composición interna). Cualitativamente, y sin cambiar nada del Context Engine todavía:

- **Segmentos que parecen constantes** (candidatos, no confirmados byte a byte): contexto canónico de organización/seguridad, perfil del rol `investigacion/research_worker_hourly`, el texto del rubric de curación (~1,400 caracteres, ~350 tokens — pequeño, no explica el piso), y probablemente contexto organizacional/policy que el Context Engine adjunta a TODA tarea sin importar el dominio.
- **Segmentos que parecen crecer con el cluster**: el payload JSON de `cluster_semantic_metadata` + `works[]` (título, abstract, año, venue, tier, topics, identificadores por Work) — consistente con el ~1,400-1,750 tokens/Work marginal medido arriba.
- **Segmentos sospechosos de ser irrelevantes para esta tarea específica**: no se puede afirmar sin ver el desglose real, pero dado que ~73,000 tokens fijos es un orden de magnitud mayor que el propio rubric+schema+1 Work de contenido, es razonable sospechar que el Context Engine está adjuntando contexto organizacional amplio (histórico de decisiones, políticas de otros dominios, etc.) que no es necesario para una tarea de curación bibliográfica acotada.

Esto confirma y cuantifica con precisión el finding del smoke test anterior (~75k tokens) — **no se cambia el Context Engine ahora**, esto queda como baseline medido para la futura optimización de Context & Inference Economy.

---

## 5. CALIDAD DE CURACIÓN — tablas por cluster

*(Solo los 9 clusters con dispatch real exitoso. `conf.` = confidence del curator, 0-1. Sin chain-of-thought — `unique_contribution` es la justificación tal como la devolvió el modelo, verbatim, truncada donde es muy larga.)*

### rag — scluster-ee06984a5f0f49d5 (2 Works, ambos retenidos, complementarios)

| Work | título | tier | conf. | redundancy_group | needs_deep_review | motivo breve |
|---|---|---|---|---|---|---|
| work-02155 | The Distracting Effect: Understanding Irrelevant Passages in RAG | P0 | 0.90 | none | No | Diagnóstico canónico del problema de pasajes distractores en RAG; medida formal, robusto entre LLMs. |
| work-02507 | Beyond RAG vs. Long-Context: Learning Distraction-Aware Retrieval for Efficient Knowledge Grounding | P1 | 0.85 | none | No | Método complementario de retrieval consciente de distracción, del lado del retriever. |

### rag — scluster-b5ba715ff2f3636f (6 Works)

| Work | título | tier | conf. | redundancy_group | needs_deep_review |
|---|---|---|---|---|---|
| work-00684 | HyKGE: Hypothesis KG Enhanced Framework (medical) | silver_only | 0.82 | kg-rag-framework | No |
| work-01370 | Medical Graph RAG: Towards Safe Medical LLM via Graph RAG | P0 | 0.93 | kg-rag-framework | No |
| work-01598 | RiTeK: Dataset for Complex Reasoning over Textual KGs (medicine) | P1 | 0.88 | tkg-retrieval-benchmark | No |
| work-01818 | Towards Omni-RAG: Comprehensive RAG for LLMs in Medical Applications | P1 | 0.88 | source-planning-rag | No |
| work-01878 | MedRAG: RAG with KG-Elicited Reasoning for Healthcare Copilot | P1 | 0.87 | kg-rag-framework | No |
| work-02895 | Clinical KG Construction and Evaluation with Multi-LLMs via RAG | P0 | 0.92 | clinical-kg-construction | No |

### rag — scluster-da421180ed2349f2 (18 Works, cluster grande, cluster grande-y-coherente)

| Work | título | tier | conf. | redundancy_group |
|---|---|---|---|---|
| work-00109 | EfficientRAG: Efficient Retriever for Multi-Hop QA | P0 | 0.90 | efficient-iterative-retrieval |
| work-01213 | Generate-then-Ground in RAG for Multi-hop QA | P0 | 0.85 | generate-then-ground |
| work-01318 | Retrieve, Summarize, Plan: Iterative Multi-hop QA | P0 | 0.88 | retrieve-summarize-plan |
| work-01398 | Hierarchical RAG Model with Rethink for Multi-hop QA | P1 | 0.75 | hierarchical-rag |
| work-01927 | Mitigating Lost-in-Retrieval in Multi-Hop QA | P1 | 0.78 | graph-rag |
| work-01964 | KiRAG: Knowledge-Driven Iterative Retriever | P1 | 0.78 | graph-rag |
| work-02130 | Credible plan-driven RAG for Multi-hop QA | P1 | 0.76 | plan-driven-rag |
| work-02252 | ComposeRAG: Modular Composable RAG | P0 | 0.87 | modular-pipeline |
| work-02254 | Optimizing Question Semantic Space for Dynamic RAG | P1 | 0.75 | query-decomposition-semantic |
| work-02394 | Transforming Questions/Documents for Semantically Aligned RAG | silver_only | 0.62 | semantic-alignment |
| work-02403 | Cross-Granularity Hypergraph RAG for Multi-hop QA | P1 | 0.72 | graph-rag |
| work-02479 | HANRAG: Heuristic Accurate Noise-resistant RAG | P1 | 0.77 | noise-handling |
| work-02573 | SUBQRAG: Sub-Question Driven Dynamic Graph RAG | P1 | 0.72 | graph-rag |
| work-02599 | PRoH: Dynamic Planning/Reasoning over KG Hypergraphs for RAG | P1 | 0.78 | hypergraph-rag |
| work-03016 | Reasoning in Trees: Improving RAG for Multi-Hop QA | silver_only | 0.65 | tree-reasoning |
| work-03216 | HyperRAG: Reasoning N-ary Facts over Hypergraphs for RAG | P1 | 0.72 | hypergraph-rag |
| work-03227 | MultiCube-RAG for Multi-hop QA | silver_only | 0.62 | ontological-query-decomposition |
| work-03477 | PAR2-RAG: Planned Active Retrieval/Reasoning for Multi-Hop QA | P0 | 0.82 | plan-driven-rag |

### memory — scluster-a16533182030ccd4 (8 Works, **1 faltante**)

| Work | título | tier | conf. | redundancy_group |
|---|---|---|---|---|
| work-02940 | FlashMem: Distilling Intrinsic Latent Memory via Computation Reuse | P0 | 0.95 | none |
| work-03161 | Learning Query-Aware Budget-Tier Routing for Runtime Agent Memory | P1 | 0.90 | none |
| work-03174 | MemFly: On-the-Fly Memory Optimization via Information Bottleneck | P1 | 0.88 | none |
| work-03366 | NextMem: Towards Latent Factual Memory for LLM-based Agents | P1 | 0.88 | none |
| **work-03889** | **MemRefine: LLM-Guided Compression for Long-Term Agent Memory** | **AUSENTE — sin tier** | — | — |
| work-03709 | RecMem: Recurrence-based Memory Consolidation | P0 | 0.93 | none |
| work-03799 | ElasticMem: Latent Memory as a Learnable Resource for LLM Agents | P1 | 0.90 | none |
| work-04064 | Retain or Consolidate? Budget-Dependent Operator Selection | P1 | 0.85 | none |

### memory — scluster-b1900dc3fd6d43c2 (16 Works)

| Work | título | tier | conf. | redundancy_group |
|---|---|---|---|---|
| work-01080 | Streaming Long Video Understanding with LLMs | P0 | 0.96 | none |
| work-01193 | VideoLLM-online: Online Video LLM for Streaming Video | P0 | 0.94 | streaming-llm-training |
| work-01423 | VideoLLaMB: Long Streaming Video Understanding, Recurrent Memory Bridges | P0 | 0.92 | memory-token-compression |
| work-01836 | Streaming Video Understanding, Multi-round Interaction, Memory | P0 | 0.90 | streaming-dialogue-systems |
| work-01980 | Streaming Video QA with In-context Video KV-Cache Retrieval | P1 | 0.87 | kv-cache-retrieval |
| work-02026 | VideoScan: Efficient Streaming Video via Frame-level Semantic Carriers | P1 | 0.84 | memory-token-compression |
| work-02312 | InfiniPot-V: Memory-Constrained KV Cache Compression | P1 | 0.86 | kv-cache-compression |
| work-02534 | StreamForest: Online Video Understanding, Persistent Event Memory | P1 | 0.82 | hierarchical-memory |
| work-02586 | StreamingVLM: Real-Time Understanding for Infinite Video Streams | P1 | 0.84 | streaming-llm-training |
| work-02628 | StreamingTOM: Streaming Token Compression | P1 | 0.83 | kv-cache-compression |
| work-02671 | LiveStar: Live Streaming Assistant for Real-World Video Understanding | P1 | 0.78 | streaming-llm-training |
| work-02680 | StreamKV: Streaming Video QA, Segment-based KV Cache | silver_only | 0.68 | kv-cache-retrieval |
| work-03345 | Think While Watching: Online Streaming Segment-Level Memory | P1 | 0.80 | streaming-dialogue-systems |
| work-03406 | CurveStream: Curvature-Aware Hierarchical Visual Memory | silver_only | 0.65 | hierarchical-memory |
| work-03424 | StreamingEval: Unified Evaluation Protocol for Streaming Video | P0 | 0.91 | evaluation-baselines |
| work-03496 | A Simple Baseline for Streaming Video Understanding | P0 | 0.95 | evaluation-baselines |

### context — scluster-ba5e63822655c3d4 (2→1 tras colapso de duplicado)

| Work | título | tier | conf. | redundancy_group |
|---|---|---|---|---|
| work-00195 (canónico; alias work-01212 colapsado, mismo paper) | GraphReader: Building Graph-based Agent to Enhance Long-Context Abilities | P0 | 0.85 | none |

### context — scluster-787c72467109c079 (8 Works)

| Work | título | tier | conf. | needs_deep_review | redundancy_group |
|---|---|---|---|---|---|
| work-00384 | SpecInfer: Accelerating LLM Serving with Tree-based Speculative Inference | P0 | 0.98 | No | tree-verification |
| work-00478 | Accelerating LLM Inference with Staged Speculative Decoding | silver_only | 0.85 | No | tree-verification |
| work-00594 | SPEED: Speculative Pipelined Execution for Efficient Decoding | silver_only | 0.82 | No | pipelined-speculation |
| work-00817 | Sequoia: Scalable, Robust, Hardware-aware Speculative Decoding | P0 | 0.97 | No | tree-verification |
| work-01307 | PipeInfer: Async Pipelined Speculation | P1 | 0.90 | No | pipelined-speculation |
| work-01394 | MagicDec: Breaking Latency-Throughput Tradeoff, Speculative Decoding | P1 | 0.90 | No | throughput-analysis |
| work-01691 | SuffixDecoding: Extreme Speculative Decoding | P1 | 0.90 | No | cache-speculation |
| work-02601 | Mirror Speculative Decoding: Breaking the Serial Barrier | **review_required** | 0.70 | **Sí** | parallel-verification |

### symbolic — scluster-40c4d59b6cfd6490 (2 Works, real, etiquetado bajo idx12)

| Work | título | tier | conf. | redundancy_group |
|---|---|---|---|---|
| work-00007 | BeliefBank: Adding Memory to a Pre-Trained LM for Systematic Belief | P0 | 0.90 | symbolic-belief-consistency |
| work-00399 | Language Models with Rationality | P1 | 0.86 | symbolic-belief-consistency |

### agents — scluster-ff2a66fd75f35298 (2 Works, real, etiquetado bajo idx14)

| Work | título | tier | conf. | redundancy_group |
|---|---|---|---|---|
| work-03107 | When Agents "Misremember" Collectively: Mandela Effect in Multi-Agent LLM Systems | P0 | 0.95 | none |
| work-03759 | Memory-Induced Tool-Drift in LLM Agents | P1 | 0.95 | none |

---

## 6. CASOS QUE QUERÍAMOS VALIDAR

**A. Un solo paper fue suficiente, resto redundante**: No se observó ningún caso limpio de este tipo en los 9 clusters exitosos — ningún cluster de tamaño ≥2 terminó con exactamente 1 Work retenido y el resto descartado como puro duplicado. El caso más cercano es `context ba5e63822655c3d4`, pero ahí la reducción a 1 Work fue por **colapso determinístico de duplicado exacto** (misma paper, dos entradas del harvester), no por decisión semántica del curator sobre Works distintos.

**B. Varios sobrevivieron porque eran complementarios**: `rag scluster-ee06984a5f0f49d5` — 2 Works, ambos retenidos (P0+P1), explícitamente descritos por el curator como cubriendo "both halves of the distraction problem in RAG": uno diagnostica el problema (`work-02155`), el otro propone un método del lado del retriever para mitigarlo (`work-02507`). `symbolic scluster-40c4d59b6cfd6490` también: BeliefBank (memoria de creencias) + "Language Models with Rationality" (consistencia lógica), ambos bajo `redundancy_group=symbolic-belief-consistency` pero con tiers distintos (P0/P1), tratados como complementarios, no como duplicados.

**C. Survey + original/foundational + benchmark/evaluation**: `memory scluster-b1900dc3fd6d43c2` es el caso más claro — retiene 4 P0 que cubren streaming-video-understanding foundational (`work-01080`), online interaction (`work-01193`), memory bridges (`work-01423`), y **dos papers de evaluación/baseline separados** (`work-03424` StreamingEval, `work-03496` "A Simple Baseline for Streaming Video Understanding") bajo `redundancy_group=evaluation-baselines`, ambos retenidos como P0 — el curator reconoció correctamente que un baseline y un protocolo de evaluación no son redundantes entre sí ni con el trabajo metodológico principal.

**D. Foundational antiguo + mejora moderna**: `rag scluster-b5ba715ff2f3636f`: `work-01370` (Medical Graph RAG, 2024, P0) vs `work-01878`/`work-01818` (2025, P1) dentro del mismo `redundancy_group=kg-rag-framework` — el más antiguo/fundacional recibió P0, las variantes posteriores P1, coherente con jerarquía temporal esperada.

**E. Papers parecidos pero metodológicamente distintos**: `rag scluster-da421180ed2349f2` (el cluster de 18) tiene 3 pares dentro de `redundancy_group=graph-rag` (work-01927, work-01964, work-02403, work-02573 — 4 en realidad) que reciben tiers heterogéneos (P1 todos, pero confidences distintas 0.72-0.78) — el curator los agrupó por familia sin colapsarlos a uno solo, señal de que reconoció similitud temática sin forzar redundancia total.

**F. Caso ambiguo correctamente enviado a review_required**: `context scluster-787c72467109c079`, `work-02601` "Mirror Speculative Decoding: Breaking the Serial Barrier in LLM Inference" — único `review_required` de todo el canario, `needs_deep_review=true`, confidence 0.70 (la más baja del cluster). Comportamiento correcto según el rubric: baja confianza → `review_required`, no degradado silenciosamente a `silver_only`.

**G. needs_deep_review legítimo**: mismo caso que F — es el único ejemplo en el canario, y su bajo confidence relativo al resto del cluster (0.70 vs 0.82-0.98 de sus pares) sugiere una señal honesta de incertidumbre, no un fallback arbitrario.

No se pudo evaluar el caso de un cluster con `silver_only` mayoritario ni un cluster completamente rechazado, porque ningún cluster real del canario cayó en ese patrón entre los 9 exitosos.

---

## 7. FALSE NEGATIVE AUDIT

Revisión manual de todos los `silver_only` con confidence alta (≥0.80) y de Works que son foundational/únicos/benchmark-principal en su cluster:

| Work | cluster | tier | conf. | ¿es foundational/único/benchmark? | veredicto |
|---|---|---|---|---|---|
| work-00684 HyKGE | rag b5ba71… | silver_only | 0.82 | No — es una de 5 variantes de KG-RAG médico en el cluster, con 2 P0 ya cubriendo la familia (Medical Graph RAG, Clinical KG Construction) | No es false negative — redundancia real dentro de una familia bien poblada |
| work-00478 Staged Speculative Decoding | context 787c72… | silver_only | 0.85 | Parcial — es de los primeros trabajos de speculative decoding en el cluster, pero SpecInfer y Sequoia (ambos P0) ya cubren tree-based speculative inference con mejor evidencia empírica | Riesgo bajo — el linaje histórico de "staged" queda parcialmente sin representación P0/P1 dedicada, pero el tema general (speculative decoding) está bien cubierto |
| work-00594 SPEED | context 787c72… | silver_only | 0.82 | No — variante de pipelined speculation, PipeInfer (P1) ya cubre esa familia | No es false negative |
| work-02394, work-03016, work-03227 (rag da4211…) | rag da4211… | silver_only | 0.62-0.65 | No — todas de confidence baja (<0.65), el propio curator señala incertidumbre vía confidence bajo, no alto | No aplica al criterio "alta confidence" del gate |
| work-02680 StreamKV | memory b1900d… | silver_only | 0.68 | No — kv-cache-retrieval ya tiene work-01980 (P1) representando esa familia | No es false negative |
| work-03406 CurveStream | memory b1900d… | silver_only | 0.65 | No — hierarchical-memory ya tiene work-02534 (P1) | No es false negative |

**CRITICAL_FALSE_NEGATIVE encontrados: 0.**

Ningún Work fue degradado a `silver_only` con confidence alta (≥0.80) siendo simultáneamente foundational/único-en-su-familia/benchmark-principal sin otro Work que preservara esa contribución. Los dos casos con confidence ≥0.80 (`work-00684` 0.82, `work-00478` 0.85, `work-00594` 0.82) están todos dentro de clusters donde la contribución real ya está preservada por al menos un P0/P1 hermano de la misma familia — el patrón de "minimum sufficient set" pedido en el rubric parece estar funcionando como se diseñó, al menos en esta muestra de 9 clusters.

**Nota de honestidad del gate**: esta auditoría cubre solo los 9 clusters con dispatch exitoso — los 4 clusters que fallaron (incluyendo `symbolic aac0f99841969e76`, que devolvió una respuesta vacía) y los 2 nunca despachados no pudieron auditarse por false negatives porque no produjeron ningún tier. El false-negative audit de esta ronda es necesariamente parcial.

---

## 8. REDUNDANCY QUALITY

Redundancy groups con >1 Work, clasificados manualmente:

| redundancy_group | cluster | Works | clasificación real |
|---|---|---|---|
| kg-rag-framework | rag b5ba71… | 3 Works (00684 silver, 01370 P0, 01878 P1) | **same-method-family**, no duplicado — cada uno aplica el patrón KG-RAG a un sub-dominio médico distinto (framework general vs safe medical LLM vs healthcare copilot) |
| graph-rag | rag da4211… | 4 Works (01927, 01964, 02403, 02573, todos P1) | **same-method-family**, variantes metodológicas distintas dentro de graph-based multi-hop QA (lost-in-retrieval mitigation, iterative retriever, cross-granularity hypergraph, sub-question driven) — no colapsables a uno solo sin perder cobertura técnica real |
| hypergraph-rag | rag da4211… | 2 Works (02599, 03216, ambos P1) | **complementary** — uno enfoca dynamic planning/reasoning, el otro n-ary facts sobre hypergraphs; técnicas relacionadas pero no intercambiables |
| plan-driven-rag | rag da4211… | 2 Works (02130 P1, 03477 P0) | **incremental** — PAR2-RAG (P0, más reciente) extiende explícitamente el enfoque plan-driven con retrieval activo planeado, coherente con foundational-antiguo+mejora-moderna |
| tree-verification | context 787c72… | 2 Works (00384 P0, 00817 P0) | **complementary**, no redundante — ambos P0: SpecInfer establece tree-based speculative inference, Sequoia lo extiende a robustez/escalabilidad de hardware; el curator correctamente NO degradó ninguno a silver pese a la cercanía temática |
| streaming-llm-training | memory b1900d… | 3 Works (01193 P0, 02586 P1, 02671 P1) | **same-method-family**, cobertura de distintos puntos del espectro online/streaming (entrenamiento online, tiempo real, asistente en vivo) |
| kv-cache-compression | memory b1900d… | 2 Works (02312 P1, 02628 P1) | **complementary** — InfiniPot-V (memory-constrained) vs StreamingTOM (token compression), mecanismos técnicamente distintos aunque el objetivo (comprimir KV cache) es el mismo |
| evaluation-baselines | memory b1900d… | 2 Works (03424 P0, 03496 P0) | **survey/overview**-adjacente — uno es protocolo de evaluación, el otro es baseline simple; correctamente tratados como complementarios, no redundantes, ambos P0 |
| symbolic-belief-consistency | symbolic 40c4d5… | 2 Works (00007 P0, 00399 P1) | **complementary** — memoria de creencias (BeliefBank) vs racionalidad/consistencia lógica (Rationality), cercanos temáticamente, técnicamente distintos — mismo patrón que el caso de referencia EPIC+MagicPIG citado en el pedido |

**Ningún caso observado donde "semánticamente similar" se colapsó incorrectamente a "redundante".** El patrón dominante del curator en esta muestra es: agrupar por `redundancy_group` para señalar relación temática, pero mantener tiers independientes por Work según su contribución específica — exactamente el comportamiento pedido en el rubric ("Related is not the same as redundant").

---

## 9. RETENTION QUALITY

No se evaluó por cuota — se interpreta cualitativamente por cluster.

| cluster | retained_work_count | retained_ratio | coverage retenida (cualitativo) |
|---|---|---|---|
| rag ee0698… | 2/2 | 100% | Cobertura completa — problema + mitigación, ambos preservados |
| rag b5ba71… | 5/6 | 83% | Framework general + 2 aplicaciones especializadas + dataset de benchmark retenidos; 1 variante redundante en silver |
| rag da4211… | 15/18 | 83% | Todas las familias metodológicas (iterative retrieval, graph-rag, hypergraph-rag, plan-driven, hierarchical) tienen al menos 1 P0/P1 representante; los 3 silver_only son variantes de baja confidence (0.62-0.65) dentro de familias ya bien cubiertas |
| memory a16533… | 6/7 tiered (1 Work sin tier, ver hallazgo 1.3) | 86% de lo tiered | Metodológicamente diversa (compresión, ruteo, consolidación) sin redundancia obvia — nada bajó a silver_only en absoluto, lo que sugiere que el cluster ya era de por sí poco redundante (coherente con clustering semántico correcto: works agrupados por similitud pero cada uno con contribución distinta) |
| memory b1900d… | 14/16 | 87.5% | Cobertura fuerte: foundational streaming (4 P0), evaluación/baseline (2 P0), y 8 P1 cubriendo sub-variantes técnicas (kv-cache, memoria jerárquica, tokens). Solo 2 silver_only, ambos con redundancy_group compartido con un P1 hermano |
| context ba5e63… | 1/1 (tras colapso) | 100% | N/A — era un duplicado exacto, la cobertura real es 1 Work único |
| context 787c72… | 6/8 (+1 review) | 75% (87.5% si se cuenta review_required como "pendiente de decisión", no descartado) | Cobertura técnica fuerte en speculative decoding: tree-based, pipelined, throughput, cache-aware — todas las sub-familias tienen representación P0/P1 |
| symbolic 40c4d5… | 2/2 | 100% | Cobertura completa |
| agents ff2a66… | 2/2 | 100% | Cobertura completa |

**Lectura cualitativa general**: ningún cluster fue vaciado agresivamente. El retained_ratio osciló entre 75-100% en los clusters no triviales, con los descartes (`silver_only`) consistentemente justificados por redundancia real dentro de una familia ya cubierta por un P0/P1 — no se observó sobre-eliminación. La cobertura de benchmarks/evaluación, linaje histórico/foundational, y metodologías distintas se preservó en todos los casos revisados.

---

## 10. DIVERSIDAD

Distribución sobre los 62 Works efectivamente tiered:

**Por año** (de los datos de metadata disponibles):
- 2021: 1 (BeliefBank)
- 2023: 4 (speculative decoding tempranos)
- 2024: ~16
- 2025: ~28
- 2026: ~13

Sesgo hacia publicaciones recientes (2025-2026 ≈ 66% del total), pero esto refleja la composición real del corpus curado (el harvester priorizó literatura reciente por diseño, no es un sesgo introducido por el curator).

**Por topic/tema del cluster seleccionado**: 3 rag, 2 memory (de los cuales uno con 16 Works, dominando el conteo total de Works), 2 context, 2 symbolic-relacionados (uno real, uno vacío), 1 agents real. efficiency no tiene representación en los resultados exitosos (ambos clusters de efficiency fallaron).

**Por authority_tier**: prácticamente todos Tier A (arXiv/venues reconocidos: EMNLP, ACL, NeurIPS, ICLR, CVPR, AAAI, etc.); solo 2 Works de Tier B en toda la muestra (`work-00684` HyKGE, `work-04064` Retain or Consolidate) — no hay indicación de que Tier B haya sido tratado sistemáticamente peor (HyKGE es Tier B y quedó silver_only por redundancia real, no por su tier; work-04064 es Tier B y quedó P1).

**Por tipo de paper**: predominan papers metodológicos originales (no surveys) — el único survey/protocolo explícito es `StreamingEval` (P0). No se observa sesgo hacia papers largos, hacia surveys, ni hacia lenguaje de marketing fuerte (los `unique_contribution` devueltos son técnicos y específicos, no promocionales).

**No se identificó sesgo problemático** en la muestra disponible, aunque n=9 clusters (62 Works) es una base pequeña para conclusiones fuertes de diversidad — esto debería revisarse de nuevo a mayor escala antes de Silver completo.

---

## 11. INPUT QUALITY

Confirmado sobre los 63 Works enviados a los 9 dispatches exitosos:

- **62 de 63 Works (98.4%) operaron sobre abstract real** (`abstract_status=present`, vía Semantic Scholar).
- **1 Work title-only afecta este canario**: `work-00195` (GraphReader), el sobreviviente canónico del colapso de duplicado en `context scluster-ba5e63822655c3d4`. Su abstract_status es `missing` — pero su alias colapsado `work-01212` (la MISMA paper, entrada duplicada del harvester) sí tenía `abstract_status=present`. **Esto es un hallazgo real, no solo una nota**: `CollapseDuplicateWorksInCluster` elige el work_id lexicográficamente menor como canónico, sin considerar cuál copia tiene mejor metadata — en este caso concreto, escogió la copia SIN abstract y descartó la que SÍ lo tenía. El curator, por tanto, tomó su decisión de P0 sobre GraphReader basándose solo en título, no en abstract, cuando una alternativa con abstract estaba disponible y fue descartada por la regla determinística de identidad.
- El resto del input (`canonical_title`, `year`, `venue`, `authority_tier`, `topics`, `canonical_identifiers`) se confirma poblado y no contaminado por títulos crudos del harvester — todos los `canonical_title` en las tablas de la sección 5 provienen de Semantic Scholar (`title_source=semantic_scholar`), consistente con la decisión de adoptar el título de S2 en match exacto de paperId tomada en la fase de reconciliación.
- No se observó ninguna otra señal de contaminación (HTML residual, títulos truncados, campos vacíos no declarados) en la muestra de 63 Works inspeccionada.

**Recomendación derivada** (no implementada, solo señalada): `CollapseDuplicateWorksInCluster` debería preferir como canónico el work_id con mejor completitud de metadata (abstract presente > abstract ausente) en caso de empate por título normalizado, no solo el lexicográficamente menor.

---

## 12. REPRODUCIBILIDAD

Confirmado, constante y congelado en los 9 dispatches exitosos:

- `rubric_version`: `v1` (idéntico en los 9)
- `cluster_algorithm_version`: `gemini-embedding-2-average-link-v1-threshold-0.88` (idéntico en los 9)
- `model_route` / provider / model_id: `deepseek/deepseek-v4-flash` (idéntico en los 9)
- `input_hash`: único y distinto por cluster real (listado en la tabla de la sección 1.1) — determinístico sobre `sha256(json.dumps(cluster_payload, sort_keys=True))`
- `adapter_version`: no versionado explícitamente como campo separado hoy — el adapter DeepSeek (`internal/modelruntime/adapter/deepseek`) no expone un número de versión propio en el registro de invocación; el `request_hash`/`egress_policy_hash` de la tabla `model_invocations` sí capturan una huella de la configuración efectiva usada, pero no hay un campo humano-legible "adapter_version" reproducible.
- `context snapshot id`: capturado por invocación (`context_snapshot_id` en `model_invocations`), cada dispatch tiene el suyo, ligado al `task_ref` correspondiente.

**Se puede explicar hoy** "por qué este Work recibió P1 bajo qué input/model/rubric" combinando: `input_hash` (qué se le mandó exactamente) + `rubric_version` + `model_route` + `context_snapshot_id` (referencia al render exacto), sin necesidad de chain-of-thought — el `unique_contribution` corto ya sirve como justificación auditable.

**Gap de reproducibilidad real**: falta un campo explícito de versión de adapter/build de código (por ejemplo, el SHA del commit del adapter DeepSeek en el momento del dispatch) — hoy solo se puede reconstruir indirectamente revisando `git log` contra la fecha del `created_at` de la invocación, no está capturado como dato de primera clase en el propio registro de invocación.

---

## 13. VEREDICTO

**ITERATE.**

Razonamiento contra los criterios propuestos:

- **No cumple PASS**: no fueron "15/15 terminales o failures explicados sin corrupción" — 2 de 15 clusters seleccionados **nunca fueron despachados** por un bug real de infraestructura del driver de canary (cascada de reclamo de tareas, sección 2.1), y 3 resultados "exitosos" quedaron con **etiqueta de cluster incorrecta** (contenido real distinto del anunciado por el índice). El accounting tiene un gap real y reproducible (2 reservas nunca liberadas, sección 2.3). Y hubo un incumplimiento de contrato del curator (1 Work enviado, nunca tiered, sección 1.3) que rompe la cuadratura exacta pedida.
- **No cae en FAIL**: la infraestructura gobernada de fondo (task/claim/context/assignment/egress/dispatch/accounting/audit) funcionó correctamente en su propia lógica — el bug está en el **driver de orquestación del canario** (código de esta sesión, no en `internal/tasks`/`internal/modelruntime`/CostGate de producción), es diagnosticable y acotado. El curator, cuando SÍ se ejecutó limpiamente, mostró discriminación real entre redundancia y complementariedad (sección 8), 0 critical false negatives en la muestra auditada (sección 7), y decisiones reproducibles (sección 12). El timeout de 30s que dominó el intento anterior (r7) está confirmado resuelto — 0 ocurrencias de `response_read_failed` en r8.
- **Motivo central de ITERATE en vez de PASS_WITH_CHANGES**: los problemas encontrados no son ajustes menores de prompt/rubric — son **3 bugs de infraestructura de orquestación distintos** (cascada de claim, accounting no liberado en fallos de normalización, 1 Work perdido sin tier) que deben corregirse antes de que un run de 15 clusters sea confiable como medición. Ninguno de los tres invalida el diseño del curator en sí, pero sí invalidan la limpieza del propio experimento — no se puede afirmar con confianza "15/15 limpio" cuando 5 de los 15 resultados (2 nunca-despachados + 3 mal-etiquetados) requirieron reconstrucción forense para saber qué pasó realmente.

**Antes de repetir el canario o avanzar a Silver completo, se requiere**:
1. Corregir el driver para finalizar explícitamente (`task result --outcome failed` + `task finalize`) toda tarea que agote reintentos, evitando que quede reclamable por el siguiente índice.
2. Investigar y corregir por qué las reservas de invocaciones con `response_normalization_failed` no se liberan (gap de `provider_wallet_events`).
3. Investigar el caso del Work faltante en el output del curator (`work-03889`) — ¿es un patrón sistemático o un incidente aislado? Con n=1 no se puede concluir, pero merece verse en el próximo run.
4. Investigar `response_truncated_empty`/`response_normalization_failed` como categoría — 6 de 15 intentos base terminaron así; no se investigó la causa raíz de por qué DeepSeek devuelve contenido vacío/inválido en ~40% de los dispatches de este canario, más allá de confirmar que NO es el bug de timeout de 30s ya resuelto.

Con esas correcciones, un re-run limpio de 15 clusters (posiblemente con `RUN_TAG=r9`) sería el siguiente paso natural antes de decidir sobre Silver completo — no bulk, un nuevo canario controlado.

---

## 14. NO SE HIZO (confirmado)

No se corrió Silver completo sobre los 2,009 clusters. No se publicó Knowledge. No se extrajo Knowledge Graph. No se corrió Docling en bulk. No se ingirió corpus multimodal en bulk. No se implementó ninguna corrección de los bugs descritos arriba — quedan documentados como hallazgos, pendientes de decisión y priorización explícita.
