# R10_4_1_DEEPSEEK_SENTINEL_RECHECK.md

R10.4.1 — DeepSeek Production-Config Sentinel Recheck. Pregunta experimental: ¿DeepSeek V4 Flash sigue perdiendo los 4 sentinels de R10 cuando se elimina el desperdicio de rendering (`ProviderRender v1`) y se corrige el output budget (floor 4500)? Control histórico: R10 (0/4 terminal-valid). Tratamiento: production-config actual (ProviderRender v1 + cache + floor corregido).

## Preflight

```
HEAD:              7cd60785683cb197b3941974d1727311447af4fa (sin cambios)
Migration tip:      000040 (sin cambios)
go test ./...:      verde, incluye los 4 tests nuevos de CorpusCurateOutputTokenBudget
ProviderRender:      research-corpus-curate-render/v1 (sin cambios desde R10.4)
Output budget:       CorpusCurateOutputTokenBudget implementado en internal/contextcompiler (nuevo, R10.4.1)
Retry policy:        máx 2 attempts/cluster (sin cambios)
```

## Nota obligatoria — delta shadow-vs-live prefix, causa cerrada

R10.4 documentó una diferencia entre el hash shadow histórico (`0718a9a2...`, 26,975 bytes) y el hash live del canario (`7f3933...`, 27,123 bytes) sin cerrar la causa exacta. **Causa confirmada en esta fase, con evidencia directa de `context_segments`**: el snapshot histórico (190, creado durante R10, antes de la integración de MiMo) tiene `docs/canonical/model-routing.yaml` en 2,224 bytes; el snapshot live (287, creado en R10.4, después de que esta misma sesión agregara la política `research.worker.mimo_canary` a ese archivo en la fase R10.2) lo tiene en 2,372 bytes — **una diferencia de exactamente 148 bytes, que coincide byte a byte con el delta total observado (27,123-26,975=148)**. `role-catalog.yaml` creció de 50,034 a 51,383 bytes en el archivo crudo (por la misma razón: el nuevo rol canario), pero su contenido **proyectado** (la entrada propia de `investigacion/research_worker_hourly`) es **idénticamente 1,179 bytes en ambos** — confirmado comparando `shadow-compile` sobre los snapshots 190 y 287 directamente.

**Clasificación: `EXPECTED_STABLE_VERSION_DIFFERENCE`.** No es nondeterminismo — es la evolución legítima y ya documentada del canonical bundle entre R10 y R10.2 (la integración de MiMo). El invariante correcto (mismo actor + mismo profile + mismo rubric + **mismas versiones de policy aplicables** + mismo estado de autoridad canónica → mismo stable prefix) se cumple: el shadow precheck de esta fase (sección siguiente), corrido bajo el estado canónico ACTUAL, ya mostró cardinalidad=1 consistente con el hash live de R10.4 — confirmando que no hay drift adicional desde entonces.

## Shadow precheck de los 4 sentinels — offline, cero llamadas a provider

| snapshot_id | cluster | fell_back | stable_prefix_hash |
|---|---|---|---|
| 292 | scluster-a16533182030ccd4 | false | `7f39338b94dddbb5a...` |
| 293 | scluster-aac0f99841969e76 | false | `7f39338b94dddbb5a...` |
| 294 | scluster-40c4d59b6cfd6490 | false | `7f39338b94dddbb5a...` |
| 295 | scluster-9f9da5855df592ce | false | `7f39338b94dddbb5a...` |

`stable_prefix_hash` cardinalidad = 1 (idéntico en los 4, y además idéntico al hash live del canario R10.4 -- confirma 0 drift desde entonces). `dynamic_suffix_hash`/`provider_render_hash` cardinalidad = 4 (todos distintos, esperado). `fallback_to_legacy` count = 0. **Gate pasado, se procede a dispatch.**

## Authority/correctness gate

Verificado sin repetir la auditoría completa de R10.4: los 7 `AuthorityTier` requeridos presentes en los 4 (`immutable_safety`, `owner_decisions`, `canonical_registry_and_policies`, `organization_agent`, `department_agent`, `role_profile`, `task_context`), proyección de `role-catalog.yaml` vigente (mismo `projected_bytes=1179`), rubric/output contract sin cambios (mismo schema, mismo validador `ValidateCurationOutputContract`). Ningún cambio desde R10.4 alteró estos invariantes.

## Ejecución real — orden fijado, ventana continua (2026-08-12T02:39:43Z inicio)

Orden: `scluster-a16533182030ccd4` (8W) → `scluster-aac0f99841969e76` (5W) → `scluster-40c4d59b6cfd6490` (2W) → `scluster-9f9da5855df592ce` (2W). 5 requests DeepSeek reales usados de 8 permitidos.

| cluster | Works | budget | attempts | status | input | output | cache hit | cache miss | latencia | costo real | quality |
|---|---:|---:|---:|---|---:|---:|---:|---:|---:|---:|---|
| a16533182030ccd4 (attempt 1) | 8 | 10,200 | 1 | ❌ `response_truncated_empty` (`finish_reason=length`, output=10,200=budget exacto) | 11,802 | 10,200 | 7,552 | 4,250 | 83.5s | $0.00946624 | — |
| a16533182030ccd4 (attempt 2) | 8 | 10,200 | 2 | ✅ succeeded | 11,802 | 7,783 | 11,776 | 26 | 60.5s | $0.00878948 | P0:1 P1:5 silver:2 |
| aac0f99841969e76 | 5 | 6,600 | 1 | ✅ succeeded | 10,658 | 5,260 | 7,552 | 3,106 | 45.5s | $0.00663446 | P0:1 P1:3 silver:1 |
| 40c4d59b6cfd6490 | 2 | **4,500** | 1 | ✅ succeeded | 9,621 | 4,219 | 7,552 | 2,069 | 35.9s | $0.00538762 | P0:2 |
| **9f9da5855df592ce** | 2 | **4,500** | 1 | ✅ succeeded | 9,494 | 2,854 | 7,552 | 1,942 | 27.2s | $0.00498036 | P1:2 |

**4/4 terminal-valid, 4/4 finalizados automáticamente, 0 limpieza manual.** 5 requests reales usados de 8 permitidos.

## Comparación histórica R10

```
R10 sentinel set:    0/4 terminal-valid (los 4 con response_truncated_empty puro)
R10.4.1:              4/4 terminal-valid
```

**No se afirma "ProviderRender causó toda la mejora"** — la corrección del output floor (3000→4500) es un factor separado y necesario para 2 de los 4 clusters (`40c4d5`, `9f9da5`, ambos de 2 Works, ambos habrían usado 3000 bajo la fórmula histórica). Se usa el lenguaje correcto: **"DeepSeek production-config recovery rate: 4/4"**, sin descomponer causalidad aislada entre `ProviderRender`/cache/output-floor (instrucción explícita, sección 5/15).

## Output-budget split (sección 16)

**A. Sentinels SIN cambio de budget** (`a16533182030ccd4` 10,200 sin cambios, `aac0f99841969e76` 6,600 sin cambios): evalúan principalmente `ProviderRender v1` + economía de contexto + cache, sin el floor corregido. **Resultado: 2/2 recuperados** (uno con 1 reintento).

**B. Sentinels CON floor corregido** (`40c4d59b6cfd6490`, `9f9da5855df592ce`, ambos 2W, 3000→4500): evalúan `ProviderRender v1` + cache + el floor corregido específicamente. **Resultado: 2/2 recuperados, ambos al primer intento, sin reintentos.**

## Caso especial — `scluster-9f9da5855df592ce`

```
DeepSeek R10 (histórico):   finish_reason=length, ~3000 output tokens, terminal failure
MiMo R10.2 (histórico):     mismo patrón mecánico de output-budget boundary observado
R10.3:                      MiMo recuperado con 4500 (primer valor probado)
R10.4.1 DeepSeek:           RECUPERADO con 4500, primer intento, sin reintentos
```

**`OUTPUT_BUDGET_RECOVERED_CROSS_PROVIDER = true`.** Ambos providers (MiMo en R10.3, DeepSeek en R10.4.1) recuperan exactamente este cluster con el mismo floor corregido (4500) — evidencia fuerte y convergente de que el fallo histórico era policy/budget-induced (un piso de presupuesto insuficiente para modelos con razonamiento habilitado), no una incapacidad semántica de ningún modelo sobre el contenido específico de este cluster.

## Primary reliability metrics

```
first_attempt_valid:               3/4 (aac0f9, 40c4d5, 9f9da5)
retry_recovered:                   1/4 (a1653182030ccd4)
terminal_valid:                    4/4
terminal_failure:                  0/4
requests / accepted outcome:       5/4 = 1.25
response_truncated_empty count:    1 (intento 1 de a1653182030ccd4)
finish_reason=length count:        1
output-budget-exhaustion count:    1
semantic-contract failure count:   0
normalization failure count:       0
```

Nota sobre el reintento de `a1653182030ccd4`: intento 1 agotó exactamente el presupuesto (10,200/10,200, `response_truncated_empty`); intento 2, con el MISMO presupuesto de 10,200, tuvo éxito holgado (7,783 tokens usados). Esto es consistente con la variabilidad de razonamiento entre invocaciones bajo `thinking` habilitado (más razonamiento consumido en el intento 1) — no un problema del budget en sí para este cluster (su budget de 10,200 ya era suficiente por la fórmula histórica, sin cambios).

## Cache — StablePrefix reutilizable bajo sentinel workloads

Cache hits reales en las 5 invocaciones: 7,552 / 11,776 / 7,552 / 7,552 / 7,552 — el patrón estable de 7,552 tokens (el `StablePrefix` completo) se repite en 4 de 5 requests, confirmando que el prefijo sigue siendo reutilizable bajo esta carga de trabajo distinta (sentinels, no los clusters "normales" del canario). El intento 2 de `a1653182030ccd4` (11,776 hit) es un dato interesante: al ser un reintento del MISMO cluster con el MISMO payload dinámico, es plausible que el provider cacheara el prompt completo del intento 1 (no solo el prefijo estable), no solo la porción fija — observación registrada, no confirmada oficialmente por el provider. **Cache medido, nunca usado como reliability gate** (instrucción explícita) — ningún cache miss causó retry, ningún cache hit implicó calidad especial asumida.

## Quality audit — comparación offline con MiMo (referencia secundaria, NO ground truth)

Para los 3 clusters que MiMo también recuperó en R10.2 (`a1653182030ccd4`, `aac0f99841969e76`, `40c4d59b6cfd6490`), comparación Work por Work:

| cluster | work_id | DeepSeek tier | MiMo tier | nota |
|---|---|---|---|---|
| a1653182030ccd4 | work-02940 | P1 | P1 | acuerdo |
| a1653182030ccd4 | work-03161 | P1 | P0 | adyacente |
| a1653182030ccd4 | work-03174 | P1 | P1 | acuerdo |
| a1653182030ccd4 | work-03366 | silver_only | P1 | high-risk (ver abajo) |
| a1653182030ccd4 | work-03709 | P1 | P1 | acuerdo |
| a1653182030ccd4 | work-03799 | P0 | P0 | acuerdo |
| a1653182030ccd4 | work-03889 | silver_only | P1 | high-risk (ver abajo) |
| a1653182030ccd4 | work-04064 | P1 | P1 | acuerdo |
| aac0f99841969e76 | work-00108 | P1 | P0 | adyacente |
| aac0f99841969e76 | work-00992 | P1 | P1 | acuerdo |
| aac0f99841969e76 | work-01893 | silver_only | P1 | high-risk (ver abajo) |
| aac0f99841969e76 | work-01940 | P1 | P1 | acuerdo |
| aac0f99841969e76 | work-02184 | P0 | P1 | adyacente |
| 40c4d59b6cfd6490 | work-00007 | P0 | P0 | acuerdo |
| 40c4d59b6cfd6490 | work-00399 | P0 | P1 | adyacente |

**3 transiciones de alto riesgo** (MiMo P1 → DeepSeek silver_only): `work-03366`, `work-03889`, `work-01893`. Revisadas individualmente contra su propio `unique_contribution`:
- `work-03366`: DeepSeek confianza 0.62-0.78 (moderada, no máxima), razón explícita: "overlaps with stronger latent-memory works in this cluster" — juicio de redundancia legítimo, no descarte por incomprensión.
- `work-03889`: mismo patrón — "largely subsumed by the retention-vs-consolidation decision framework in work-04064" — juicio de redundancia dentro del mismo cluster, referencia concreta a otro Work.
- `work-01893`: confianza 0.62 (más baja de las tres), MiMo mismo lo había flageado `needs_deep_review=true` en R10.2 (incertidumbre reconocida por ambos providers de forma independiente).

**Ninguna cumple el patrón de critical false negative** establecido en `DEEPSEEK_VS_MIMO_QUALITY_AUDIT.md` (confianza alta + sin flag de incertidumbre + paper explícitamente "benchmark/foundational"): las tres tienen confianza moderada-baja y razones de redundancia concretas y verificables. **No se reporta ninguna regresión crítica de calidad en esta fase.** No se repite el quality audit completo (no requerido).

## Contabilidad

5 requests DeepSeek reales, costo total real: $0.03525816. 4/4 tareas finalizadas automáticamente, 0 limpieza manual, 100% visibilidad de accounting. 0 requests MiMo (por diseño).


---

**Historical runtime evidence referenced by R9–R10.5 was destroyed in the development-database incident of 2026-08-12. The reports and committed implementation remain intact, but the referenced database rows are no longer independently queryable.**
