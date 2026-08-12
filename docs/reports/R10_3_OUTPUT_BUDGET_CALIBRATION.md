# R10_3_OUTPUT_BUDGET_CALIBRATION.md

Parte A de R10.3. Variable única cambiada: `max_output_tokens`. Todo lo demás congelado exacto (cluster, Work set, ContextProfile, rubric, model, response format, prompt, validator, task lifecycle).

## Cluster objetivo

`scluster-9f9da5855df592ce` (2 Works: `work-00913`, `work-01458`) — el fallo común de R10/R10.2, ambos providers agotaron `max_output_tokens≈3000` con `finish_reason=length` sin contenido válido completo.

## Experimento A1 — MiMo-V2.5

| valor probado | resultado | terminal_valid | input_tokens | output_tokens | invocation_id |
|---:|---|---|---:|---:|---:|
| 4500 | ✅ éxito en el primer intento | true | 36,344 | 2,538 | 261 |

**STOP en el primer valor exitoso** (por diseño del experimento) — no se probaron 6000 ni 8000. `output_tokens=2538` está cómodamente bajo el presupuesto de 4500, sin truncamiento. 1 request MiMo real usado (de los ≤3 permitidos).

`minimum_observed_valid_budget_mimo = 4500`

## Experimento A2 — DeepSeek V4 Flash (mismo budget de control)

| valor usado | resultado | invocation_id |
|---:|---|---:|
| 4500 | ❌ `response_truncated_empty` (mismo modo de fallo que en R10) | 262 |

Por instrucción explícita, **no se reintentó** ni se optimizó DeepSeek con valores mayores — el objetivo de A2 era únicamente verificar si el mismo piso que recuperó a MiMo también recupera a DeepSeek. No lo hizo. 1 request DeepSeek real usado (del máximo de 1 permitido para esta comparación).

`deepseek_same_budget_status = FAIL`

## Resultado

`OUTPUT_BUDGET_CALIBRATION` no es un fallo total (MiMo se recupera), pero tampoco un éxito universal (DeepSeek sigue fallando al mismo piso). El resultado es **provider-specific**: MiMo necesita al menos 4500 (posiblemente menos, no probado por diseño — el experimento detiene en el primer éxito, no busca el mínimo exacto), DeepSeek necesita más de 4500 (no determinado, fuera de alcance de esta fase).

## Propuesta de nuevo floor — NO aplicada todavía

**No se cambia la fórmula global `max(3000, min(16000, 600+1200*n_works))` en esta fase.** Nota importante descubierta durante el preflight: esta fórmula vive únicamente en el driver Python local (`canary15_driver_r10.py`), no en ningún código Go de producción — es una convención del arnés de canario, no un componente del sistema desplegado. Cualquier cambio de floor es, por ahora, una recomendación de configuración de futuros drivers/tareas, no una migración de código.

Propuesta (evidencia, no política): **elevar el piso mínimo de 3000 a 4500 para clusters pequeños (1-2 Works) cuando el provider es MiMo**, dado el patrón confirmado en el código real (`internal/modelruntime/adapter/mimo/adapter.go`: `alwaysEnabledThinking = thinkingConfig{Type: "enabled"}`, deliberadamente nunca deshabilitado por decisión explícita del owner — sección E de `MIMO_V25_INTEGRATION_AUDIT.md`) — el modo `thinking` consume `completion_tokens` reales antes de la respuesta final, y un piso de 3000 sin margen para razonamiento es la causa estructural más simple del truncamiento observado. **No se recomienda el mismo cambio de piso para DeepSeek** sin evidencia adicional (A2 no encontró el mínimo real de DeepSeek, solo confirmó que 4500 es insuficiente).

**Margen de seguridad, no el mínimo observado**: no se recomienda fijar el piso exactamente en 4500 (el mínimo que funcionó una vez) — un piso de producción debería incluir margen sobre el mínimo observado para tolerar variabilidad de razonamiento entre invocaciones. Se deja como recomendación cualitativa para que el owner decida el margen exacto (p.ej. 6000 como piso operativo con reserva, no 4500 exacto), no como valor hardcodeado en esta fase.

## Contabilidad

2 requests reales usados en Parte A (1 MiMo + 1 DeepSeek), ambos dentro de los caps declarados (≤3 MiMo para calibración de budget, ≤1 DeepSeek para el control). Accounting: MiMo `$0 subscription-covered` (real, no fabricado, invocation 261), DeepSeek `$0.00962178` (invocation 262, fallo, cobrado igual — el fallo del provider no exime el costo real de un request PAYG ya realizado).


---

**Historical runtime evidence referenced by R9–R10.5 was destroyed in the development-database incident of 2026-08-12. The reports and committed implementation remain intact, but the referenced database rows are no longer independently queryable.**
