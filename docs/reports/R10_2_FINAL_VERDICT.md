# R10_2_FINAL_VERDICT.md

Veredicto formal de R10.2 (MiMo-V2.5 provider challenger canary), emitido tras la auditoría offline de calidad completa (`DEEPSEEK_VS_MIMO_QUALITY_AUDIT.md`) y las correcciones metodológicas al reporte de comparación (`DEEPSEEK_VS_MIMO_CURATION_CANARY.md`). Cero llamadas nuevas al provider en esta fase — auditoría 100% sobre datos ya persistidos.

## Evidencia consolidada

| Eje | DeepSeek R10 | MiMo | Ventaja |
|---|---:|---:|---|
| Reliability (15 clusters) | 11/15 (73.3%) | 14/15 (93.3%) | **MiMo** |
| Requests/accepted outcome | 2.09 | 1.36 | **MiMo** |
| Bytes de respuesta/accepted outcome | 54,255 | 21,645 | **MiMo** |
| Latencia p50/p95 por request | 50.0s / 90.2s | 42.5s / 72.3s | **MiMo** (n pequeño) |
| Sentinels de DeepSeek recuperados | — | 3/4 | **MiMo** |
| Critical false negatives (set pareado, 73 Works) | 0 | 1 | **DeepSeek** |
| Cobertura recuperada (15 Works adicionales) | — | 0 critical false negatives, contrato exacto | **MiMo** (sin contrapartida DeepSeek) |
| Costo real (cash, PAYG) | $0.16531130 (100% visible) | subscription-covered (no comparable en $) | no aplica comparación directa |
| Governance/accounting | sin regresión | sin regresión (subscription provenance correcta, sin `$0` engañoso) | empate |

## Aplicación de los criterios de MIMO_WINS (sección 12 del pedido original)

Requisitos mínimos exigidos:
- ✅ 14/15 reliability observado.
- ❌ **no critical false-negative regression** — se encontró 1 (`scluster-ec8115783df9722f`/`work-03893`, benchmark paper degradado de P0/0.97 a `silver_only`/0.80 sin flag de incertidumbre).
- ✅ exact Work completeness (100% en los 14 clusters válidos, 0 faltantes/duplicados/tier inválido).
- ⚠️ quality comparable or better on paired set — comparable (63.0% acuerdo exacto de tier, mayoría de desacuerdos adyacentes P0↔P1), pero no estrictamente "mejor" dado el critical false negative.
- ✅ recovered clusters quality acceptable (3/3 clusters, 15/15 Works, 0 critical false negatives, contrato exacto).
- ✅ no governance/accounting regression.

**No se cumple el criterio obligatorio de "no critical false-negative regression".** Por diseño explícito del pedido, esto excluye a `MIMO_WINS` como veredicto, sin importar cuán favorables sean los demás ejes.

## Veredicto: **COMPLEMENTARY**

MiMo-V2.5 y DeepSeek V4 Flash no son intercambiables sin matices — cada uno tiene una ventaja real, medida, no fabricada:

- **MiMo gana en reliability, cobertura, eficiencia de recursos (requests y bytes por resultado aceptado), y latencia por-request observada.** Recupera 3 de los 4 clusters que DeepSeek pierde sistemáticamente, con cobertura de calidad limpia (0 critical false negatives en esos 15 Works adicionales).
- **DeepSeek gana en el único eje de seguridad de calidad estrictamente medido**: 0 critical false negatives en su propio run vs 1 confirmado en MiMo sobre el mismo set pareado de 73 Works — un paper explícitamente calificado como "benchmark" fue degradado con confianza y sin señal de incertidumbre.
- Ninguno domina al otro en todos los ejes simultáneamente — la combinación de ambos (p.ej. MiMo como ruta primaria con revisión humana reforzada en el rango `silver_only`/`review_required`, o DeepSeek como validación cruzada en casos de alto riesgo) sería más segura que reemplazar uno por el otro ciegamente.

No se declara `INCONCLUSIVE` porque sí hay evidencia suficiente y consistente para caracterizar ambas ventajas (no es un empate por falta de datos); no se declara `MIMO_WINS` porque el criterio obligatorio de false-negative no se cumple; no se declara `DEEPSEEK_WINS` porque la ventaja de reliability/cobertura/eficiencia de MiMo es real, medida y sustancial, no un ruido menor.

**Nota de incertidumbre explícita** (por instrucción directa del pedido): no se exige significancia estadística con n=15 para reconocer la ventaja operacional observada de MiMo — es real y reproducible en esta corrida — pero la magnitud exacta de esa ventaja de reliability (73.3%→93.3%, +20pp) tiene incertidumbre real con una muestra de 15 clusters; una segunda corrida independiente podría mostrar una brecha menor o mayor. La ventaja de calidad de DeepSeek (0 vs 1 critical false negative) también proviene de una muestra pequeña (73 Works pareados) y un solo caso — no se generaliza como "MiMo tiene una tasa de critical false negative de X%" sin más evidencia.

## Routing Recommendation (NO implementado)

Recomendación a evaluar por el owner, condicionada a resolver el hallazgo de calidad antes de cualquier cambio real:

```
research.corpus_curate:
  primary: deepseek-v4-flash        # sin cambios, mantiene el comportamiento actual
  challenger_shadow: mimo-v2.5      # seguir corriendo en shadow/canario, nunca como ruta activa
```

Alternativa a considerar SOLO si el owner decide priorizar reliability/cobertura sobre el gap de calidad detectado, y solo después de mitigar el riesgo del `silver_only`/`review_required` (p.ej. forzando revisión humana obligatoria en el 100% de esos tiers para MiMo, no solo cuando `needs_deep_review=true`, dado que el único critical false negative encontrado NO tenía ese flag activado):

```
research.corpus_curate:
  primary: mimo-v2.5
  fallback: deepseek-v4-flash
  mandatory_human_review: [silver_only, review_required]  # para todo output de mimo-v2.5, sin excepción
```

**No se cambia el ruteo de ningún rol en esta fase.** Ambas opciones quedan como recomendación para decisión del owner, no como acción tomada.

## Full Silver Decision

```
FULL_SILVER_READY = NO
```

**Razón**: (1) el critical false negative confirmado en el set pareado indica que el rubric/modelo challenger todavía puede descartar contenido de tipo "benchmark" con confianza y sin señal de alerta — correr Full Silver con esa exposición sin mitigación sería propagar ese riesgo a escala; (2) la semántica de `credits` del Token Plan de MiMo permanece `unknown` (no se puede proyectar el costo/cuota de una corrida a escala completa con la información disponible); (3) el pedido original R10.2 excluye explícitamente Full Silver de esta fase bajo cualquier resultado — este veredicto no cambia esa restricción, solo la ratifica con evidencia.

## TOKEN PLAN RESOURCE ACCOUNTING

```
A. Provider usage (medido):       25 requests reales, 1,118,404 input + 105,790 output tokens (intra-provider only, ver nota tokenizer)
B. Subscription resource usage:   106,134,737 credits (dashboard), credit_semantics=unknown, measurement_scope=unknown
C. Cash accounting:                subscription-covered, sin cargo marginal PAYG atribuible; DeepSeek R10 actual PAYG = $0.16531130
```

`credits/request` y `credits/accepted_execution` = **N/A** — el alcance de medición del contador de créditos no coincide de forma verificable con los 25 requests reales de esta fase (ver `MIMO_V25_INTEGRATION_AUDIT.md` sección M para el detalle completo). Este dato no altera el veredicto de calidad/reliability de esta sección — es evaluación económica/de capacidad, tratada por separado según instrucción explícita.

## STOP

No se realizaron llamadas nuevas al provider en esta fase de auditoría (100% offline). No se ejecuta Full Silver. No se prueba MiniMax. No se inicia R11. No se cambia el ruteo automático de ningún rol — DeepSeek permanece intacto y como ruta primaria; MiMo permanece como challenger, disponible para futuras corridas de canario bajo la misma disciplina (offline-first, caps explícitos, nunca ventaja artificial), pendiente de que el owner decida sobre las recomendaciones de esta sección.
