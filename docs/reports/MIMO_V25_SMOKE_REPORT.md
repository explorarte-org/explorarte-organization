# MIMO_V25_SMOKE_REPORT.md

Fase B de R10.2: 4 smokes controlados contra MiMo-V2.5 real (`mimo-v2.5`, Direct API), usando los mismos Work sets canónicos de R10 (nunca modificados). 4to intento (`RUN_TAG=mimosmoke4`) — los 3 intentos previos fallaron 100% pre-provider (identity check → egress_denied → credential_unavailable), todos corregidos y documentados aparte; 0/34 del budget se gastó en esos 3 intentos.

## Resultado por cluster

| # | cluster_id | size | attempts | resultado | input_tok | output_tok | tiers |
|---|---|---:|---:|---|---:|---:|---|
| SMOKE1 | scluster-ee06984a5f0f49d5 | 2 | 1 | **OK** (terminal-valid) | 36,143 | 2,517 | P0:2 |
| SMOKE2 | scluster-787c72467109c079 | 8 | 2 | **FAILED** (FAILED_AFTER_RETRIES) | — | — | — |
| SMOKE3 | scluster-da421180ed2349f2 | 18 | 1 | **OK** (terminal-valid) | 64,710 | 7,988 | P0:2 P1:13 silver:3 |
| SMOKE4 | scluster-a16533182030ccd4 | 8 | 2 (1 fail + 1 ok) | **OK** (terminal-valid) | 46,600 | 2,941 | P0:4 P1:3 review_required:1 |

**3/4 terminal-valid (75%). El cluster de 18 Works (SMOKE3) fue procesado limpio en el primer intento.**

Requests reales consumidos en este intento: SMOKE1=1, SMOKE2=2, SMOKE3=1, SMOKE4=2 → **6 requests totales**. Presupuesto restante tras este smoke: 34-6 = 28 para los 15 clusters (máx 2 intentos c/u = hasta 30 — se monitoreará de cerca para no exceder el cap duro).

## SMOKE2 — falla de contrato (no de conectividad)

```
expected_cluster_id: "scluster-787c72467109c079"
actual_cluster_id:   "cluster-787c72467109c079"   <- MiMo omite el prefijo "s"
cluster_id_mismatch: true
missing_work_ids: ["work-02601"]
```

MiMo devolvió el `cluster_id` sin el prefijo `s` (patrón: `scluster-X` → `cluster-X`) Y omitió un Work del cluster. El validador de contrato exacto (idéntico al usado con DeepSeek, sección 9/idéntico fix `cluster_id` no re-pedido como responsabilidad generativa aplicado igual) lo rechazó correctamente en el intento 1; el reintento (intento 2, presupuesto max 2) repitió el mismo patrón de mismatch+missing Work y terminó en `FAILED_AFTER_RETRIES` — comportamiento correcto del sistema, sin tolerancia especial para MiMo.

## SMOKE4 — recuperación del sentinel de DeepSeek

`scluster-a16533182030ccd4` fue exactamente el cluster que DeepSeek falló terminalmente en R10 con `response_truncated_empty` puro (ningún contenido, ninguna violación de contrato — simplemente vacío). MiMo, en cambio:
- Intento 1: **respondió con contenido** pero con el mismo patrón de `cluster_id_mismatch` (`cluster-X` sin `s`) + 1 Work faltante (`work-03174`) → `curation_output_contract_invalid`, rechazado correctamente.
- Intento 2: corrigió ambos problemas → contrato exacto cumplido, 8/8 Works, terminal-valid.

Es una recuperación real y genuina de un modo de fallo distinto al de DeepSeek (MiMo nunca truncó vacío en estos 4 smokes; su modo de fallo observado es un `cluster_id`-prefix drop + Work faltante, no truncamiento).

## Gate de la sección 34 del pedido

Criterio: **≥3/4 terminal-valid** Y **el cluster de 18 Works debe ser técnicamente procesable**.

- 3/4 terminal-valid: ✅ cumplido.
- Cluster de 18 Works (SMOKE3) procesado limpio, contrato exacto, sin reintentos: ✅ cumplido.

**GATE PASADO. Se procede a Fase C (15 clusters, mismos que R10/r9.1).**

## Nota de capacidad

El patrón de fallo observado (`cluster_id` sin prefijo `s`) es consistente con la sección K del audit — `structured_output_json_object` se registró como "verificado con prompt explícito anti-markdown", no como "cumple estructura exacta sin fallas" — el validador de contrato exacto sigue siendo necesario e irremplazable, igual que con DeepSeek. No se relaja el contrato ni se agrega tolerancia para este patrón.
