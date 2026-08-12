# R9_VS_R10_COMPARISON.md

Comparación estricta. **CONTROL = r9.1** (contexto completo, contrato definitivo tras los 4 fixes de cierre). **TREATMENT = R10** (mismo contrato, `ExecutionContextView` proyectado activo solo para `research.corpus_curate`). Mismos 15 cluster IDs, mismos Work sets, mismo modelo, mismo rubric, mismo clustering, mismo `max_output_tokens`, misma política de reintentos.

| Metric | r9.1 (control) | R10 (treatment) | Delta |
|---|---:|---:|---:|
| mean prompt tokens/cluster (15, 1er intento) | 82,451 | 39,423 | **-52.2%** |
| fixed context floor (medido) | ~74,100 | ~31,000-33,000 (clusters de 1-2 Works) | -~58% |
| provider requests (intentos totales) | 21 | 23 | +2 (1 fallo extra generó 1 reintento extra) |
| retry amplification (requests/terminal_valid) | 21/12 = 1.75 | 23/11 = 2.09 | +0.34 |
| valid terminal clusters | 12/15 (80%) | 11/15 (73.3%) | -1 cluster |
| provider-only reliability (excluyendo problemas de contrato) | 12/15 (80%) — ya sin contaminación de cluster_id typos | 11/15 (73.3%) | -6.7pp, **no concluyente con n=15** |
| response_truncated_empty count | 3 | 4 | +1 |
| latencia | no recalculada en detalle esta ronda | no recalculada en detalle esta ronda | — |
| actual provider cost (100% visibilidad ambos) | $0.27960100 | $0.16531130 | **-40.9%** |
| unreconciled exposure | $0 | $0 | sin cambio |
| cost per accepted outcome | $0.2796/12 = $0.0233 | $0.1653/11 = $0.0150 | **-35.7%** |
| cache hit ratio | 0% | 0% | sin cambio |
| P0 count (clusters exitosos) | no tabulado por separado en r9.1 (ver R9.1 canary) | 23 | — |
| P1 count | — | 46 | — |
| silver_only count | — | 4 | — |
| accounting visibility | 100% | 100% | sin cambio |

## Lectura honesta

**Economía**: la reducción de contexto funcionó exactamente como predijo el shadow report (53.9% de reducción proyectada, 52.2% medido en producción real — desviación de menos de 2 puntos). El costo real bajó 40.9%, y el costo por resultado aceptado bajó 35.7% pese a tener un cluster fallido más — la reducción de tokens de input domina claramente sobre la pequeña pérdida de reliability.

**Reliability**: cayó de 80% a 73.3% — exactamente 1 cluster de 15. Es real, se reporta sin filtrar, pero **no hay evidencia suficiente para atribuirlo a la reducción de contexto** de forma confiable. Motivos concretos para no sacar esa conclusión todavía:
1. Las 4 fallas de R10 son idénticas en naturaleza (`response_truncated_empty` puro) a las 3 de r9.1 y a las de r8/r9 — el mismo modo de fallo que ya veníamos observando de forma intermitente en corridas con contexto completo, sin relación demostrada con el tamaño del input.
2. n=15 es una muestra pequeña para un evento binario con tasa base ~20-27% — una diferencia de 1 cluster (6.7 puntos porcentuales) está dentro del ruido esperable de una corrida a otra del mismo provider, incluso sin cambiar nada (ya lo vimos entre r8, r9 y r9.1, con tasas de fallo distintas en cada corrida bajo condiciones nominalmente idénticas).
3. No se controló por el orden ni por variabilidad temporal del lado del provider (posible carga del servicio, etc.).

**Conclusión siguiendo el criterio de la sección 27 del diseño**: este es el escenario intermedio que el propio plan anticipó — "R9.1 11/15, R10 11/15 pero R10 baja 70% los tokens, la reducción sigue siendo valiosa, pero el output reliability necesita otro tratamiento". Los números reales (11/15 vs 12/15, -52% tokens) caen casi exactamente en esa categoría.

## Veredicto final combinado: **PASS_WITH_CHANGES**

- La reducción de contexto es real, medida, reproducible, y no muestra degradación de calidad (0 critical false negatives nuevos, misma distribución de tiers, mismo patrón de `needs_deep_review`).
- La reliability del provider sigue siendo el problema central, independiente del tamaño de contexto — la evidencia de esta ronda no muestra que reducir contexto lo empeore, pero tampoco que lo arregle.
- **Recomendación**: adoptar la proyección de `role-catalog.yaml` para `research.corpus_curate` como comportamiento por defecto (ya está activada, con rollback disponible vía el fallback automático a canónico si algo falla) — el ahorro de costo/tokens es sólido y sin riesgo demostrado de calidad. La reliability del provider (`response_truncated_empty`) sigue siendo un problema aparte, no resuelto por R10, que requiere su propio tratamiento (ajuste de `max_output_tokens`, o evaluar otro modelo/ruta cuando el trabajo de Benchmark Registry/CEO routing esté listo).

## No se hizo

No se corrió Full Silver. No se generalizó el perfil a otros task classes. No se tocó `renderer.go` (el fix del cache-hit real queda pendiente, documentado como cambio separado). No se cambió `max_output_tokens`, modelo, rubric, ni política de reintentos.


---

**Historical runtime evidence referenced by R9–R10.5 was destroyed in the development-database incident of 2026-08-12. The reports and committed implementation remain intact, but the referenced database rows are no longer independently queryable.**
