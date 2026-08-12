# R10_4_1_FINAL_VERDICT.md

Veredicto formal de R10.4.1. Basado en `R10_4_1_DEEPSEEK_SENTINEL_RECHECK.md` y `R10_4_1_DEEPSEEK_FULL_SILVER_PROJECTION.md`. R10.4 permanece congelado sin reinterpretar (`PROVIDER_RENDER_V1_STATUS=ADOPT`, `DEEPSEEK_PROMPT_CACHE_STATUS=WORKING`).

## DEEPSEEK_SENTINEL_RECOVERY

**`4/4`**

Los 4 clusters que DeepSeek falló terminalmente en R10 (`response_truncated_empty` puro, 0/4) se recuperaron en su totalidad bajo la configuración candidata a producción: `scluster-a16533182030ccd4` (recuperado en reintento), `scluster-aac0f99841969e76`, `scluster-40c4d59b6cfd6490`, `scluster-9f9da5855df592ce` (los dos últimos, ambos de 2 Works, específicamente gracias al floor corregido de 4500 — `OUTPUT_BUDGET_RECOVERED_CROSS_PROVIDER = true`, confirmado también en MiMo en R10.3).

## DEEPSEEK_PRODUCTION_CONFIG_RELIABILITY

**`STRONG`**

Justificación: 4/4 terminal-valid (100% de la muestra de sentinels, el peor subconjunto conocido de DeepSeek bajo cualquier configuración anterior), contrato de salida exacto en los 4, 0 regresión de lifecycle/accounting, 0 falso negativo crítico detectado en la comparación offline con MiMo (3 transiciones de alto riesgo revisadas individualmente, las 3 con confianza moderada-baja y razones de redundancia concretas, ninguna con el patrón de "confianza alta + sin flag de incertidumbre + Work foundational/benchmark" que definió el único critical false negative confirmado de MiMo en R10.2). No se clasifica `IMPROVED` (que implicaría mejora parcial con dudas) porque la recuperación fue completa (4/4, no 2-3/4); no se clasifica `INCONCLUSIVE` porque el patrón es limpio y consistente, no ambiguo — con la salvedad honesta de que la muestra total de producción-config DeepSeek sigue siendo pequeña (9 requests reales acumulados entre R10.4 y R10.4.1).

## PROVIDER_STRATEGY_RECOMMENDATION

**`DEEPSEEK_PRIMARY_CANDIDATE`**

Criterios de la sección 26 cumplidos: ≥3/4 sentinels terminal-valid (se cumple 4/4, el caso más favorable explícitamente mencionado), exact Work completeness (4/4), sin regresión crítica de calidad, economía de production-config claramente sostenible (proyección Full Silver de un solo dígito a bajo-doble-dígito USD bajo cualquier escenario de cache, ver reporte de proyección), sin regresión de lifecycle/accounting.

**Esto NO significa cambiar el ruteo automáticamente** (instrucción explícita, sección 29) — `research.worker` sigue apuntando a `deepseek-v4-flash` sin cambios (nunca cambió durante toda la fase R10.x); esta es una recomendación para decisión del owner, no una acción tomada.

**Contexto de la estrategia alternativa (R10.3)**: `MIMO_PRIMARY_DEEPSEEK_ADJUDICATOR` seguía siendo la recomendación más fuerte al cierre de R10.3, basada en la ventaja de reliability/cobertura de MiMo sobre DeepSeek bajo su configuración ANTIGUA (legacy render, floor 3000). R10.4.1 cambia el terreno de comparación: bajo production-config actual, DeepSeek ya no muestra la brecha de reliability que motivó preferir a MiMo como primary (4/4 en el peor subconjunto conocido de DeepSeek). Esto no invalida el hallazgo de R10.3 (calidad de MiMo, adjudicación funcionando, capacidad de créditos) — lo complementa con una nueva variable: DeepSeek bajo su propia configuración corregida también resuelve gran parte del problema original de reliability, con una economía muchísimo más simple (PAYG real, sin los problemas de cuota de 4.1B créditos que sí bloquean a MiMo para Full Silver).

## DEEPSEEK_FULL_SILVER_TECHNICAL_READY

**`CONDITIONAL`**

No se declara `YES` sin matices porque:
- La muestra de producción-config DeepSeek sigue siendo pequeña (9 requests reales totales: 5 del canario R10.4 + 4 sentinels de R10.4.1) — suficiente para invalidar la hipótesis "DeepSeek sigue fallando los sentinels", insuficiente para certificar reliability a escala de 2,009 clusters sin una corrida de validación más amplia.
- El bucket de clusters grandes (19+ Works, 15 clusters, hasta 77 Works) nunca se probó bajo production-config — la proyección económica para ese bucket es una extrapolación, declarada explícitamente como tal.
- La tasa $/token usada en la proyección económica es empírica (9 requests), no la tabla de precios oficial verificada de DeepSeek.

No se declara `NO` porque no hay ningún hallazgo que bloquee la viabilidad técnica: contrato exacto sostenido, 0 regresión de accounting/lifecycle, 0 regresión crítica de calidad, output-budget issue cerrado (floor 4500 confirmado funcionando, cross-provider), economía proyectada trivialmente viable en dólares.

**Condición para pasar a `YES`**: repetir una validación de mayor escala bajo production-config (p.ej. el mismo canario completo de 15 clusters de R10/R10.2, ahora con `ProviderRender v1` + floor corregido) antes de autorizar Full Silver — no ejecutado en esta fase (fuera de alcance, `NO FULL SILVER` explícito).

## Economía — resumen

```
LOW:  ≈ $8.81  (cache óptimo, retry 1.2x)
BASE: ≈ $15.00 (cache conservador ~41%, retry 1.2x)
HIGH: ≈ $21.90 (0% cache, retry 1.2x)
```

Contraste directo con MiMo (R10.3): Full Silver con MiMo excede el plan completo de 4.1B créditos en todos los escenarios calculados (LOW ~164% de la cuota). Full Silver con DeepSeek es económicamente trivial en cualquier escenario de cache. La variable que domina la decisión ya no es el costo — es la validación de reliability a escala y la decisión estratégica de si preferir un solo provider (DeepSeek) o una estrategia complementaria (MiMo+adjudicador).

## NEXT_RECOMMENDED_EXPERIMENT

Dado que DeepSeek SÍ resultó suficiente en esta recheck (4/4, no se activa la condición de la sección 36 del pedido para requerir un challenger nuevo), no se declara una necesidad urgente de R10.5. Se deja la nota igualmente, por completitud, para cuando el owner quiera diversificar compute o explorar costo aún menor:

```
NEXT_RECOMMENDED_EXPERIMENT (opcional, no urgente dado el resultado de R10.4.1):
R10.5 — Kaggle Quota GPU Challenger
Gemma 4 E4B primero
Gemma 4 E2B segundo si se justifica
```

No implementado. No evaluado. Puramente una nota para el roadmap futuro.

## No se hizo

No Full Silver. No Kaggle. No llamadas a MiMo. No MiniMax. No R11. No Skills/Tools. No Memory OS. No nuevo rubric/clustering. No cambios a ContextProfile. No compresión adicional de contexto. No rediseño de ProviderRender. No reintentos >2. No relajación de validator. No cambio de ruteo productivo.

## Hard caps — verificación final

```
DeepSeek: 5 requests reales usados de 8 permitidos
MiMo:     0 requests (por diseño)
```

`go test ./...` verde (incluye los 4 tests nuevos de `CorpusCurateOutputTokenBudget`). Imágenes reconstruidas y desplegadas antes de las llamadas reales. 0 STOP conditions disparadas.

## STOP

R10.4.1 cierra aquí. `DEEPSEEK_SENTINEL_RECOVERY=4/4`, `DEEPSEEK_PRODUCTION_CONFIG_RELIABILITY=STRONG`, `PROVIDER_STRATEGY_RECOMMENDATION=DEEPSEEK_PRIMARY_CANDIDATE`, `DEEPSEEK_FULL_SILVER_TECHNICAL_READY=CONDITIONAL`. Ningún routing cambiado. Pendiente de decisión del owner sobre si validar a mayor escala antes de Full Silver, y sobre cómo reconciliar esta recomendación con la de R10.3 (`MIMO_PRIMARY_DEEPSEEK_ADJUDICATOR`) — ambas evidencias son reales y no se contradicen, describen configuraciones distintas de DeepSeek (legacy vs production-config).


---

**Historical runtime evidence referenced by R9–R10.5 was destroyed in the development-database incident of 2026-08-12. The reports and committed implementation remain intact, but the referenced database rows are no longer independently queryable.**
