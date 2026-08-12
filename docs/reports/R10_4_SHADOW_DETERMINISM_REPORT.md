# R10_4_SHADOW_DETERMINISM_REPORT.md

Fase de shadow determinism sobre los mismos 15 `context_snapshot` reales de R10 (`task_ids` 130-144, `snapshot_ids` 190-210). **100% read-only, cero llamadas a provider** — vía `orgctl contextcompiler provider-render-shadow <snapshot_id>`, nuevo subcomando CLI que llama directamente a `contextengine.BuildProviderRender` sobre la proyección real ya persistida (misma `contextcompiler.CompileForTaskClass`, sin cambios).

## Resultado agregado

| snapshot_id | fell_back | stable_prefix_hash (primeros 12) | stable_prefix_bytes | dynamic_suffix_bytes | provider_visible_bytes |
|---|---|---|---:|---:|---:|
| 190 | false | `0718a9a209e8` | 26,975 | 6,810 | 33,785 |
| 192 | false | `0718a9a209e8` | 26,975 | 15,639 | 42,614 |
| 194 | false | `0718a9a209e8` | 26,975 | 37,426 | 64,401 |
| 195 | false | `0718a9a209e8` | 26,975 | 7,692 | 34,667 |
| 197 | false | `0718a9a209e8` | 26,975 | 17,919 | 44,894 |
| 199 | false | `0718a9a209e8` | 26,975 | 37,160 | 64,135 |
| 201 | false | `0718a9a209e8` | 26,975 | 4,993 | 31,968 |
| 202 | false | `0718a9a209e8` | 26,975 | 18,015 | 44,990 |
| 203 | false | `0718a9a209e8` | 26,975 | 9,018 | 35,993 |
| 204 | false | `0718a9a209e8` | 26,975 | 17,836 | 44,811 |
| 205 | false | `0718a9a209e8` | 26,975 | 7,152 | 34,127 |
| 206 | false | `0718a9a209e8` | 26,975 | 11,913 | 38,888 |
| 208 | false | `0718a9a209e8` | 26,975 | 7,714 | 34,689 |
| 209 | false | `0718a9a209e8` | 26,975 | 18,200 | 45,175 |
| 210 | false | `0718a9a209e8` | 26,975 | 6,991 | 33,966 |

```
stable_prefix_hash cardinality:    1   (idéntico en los 15 -- el hash completo de 64 hex chars coincide exacto en los 15, no solo el prefijo mostrado)
dynamic_suffix_hash cardinality:   15  (todos distintos -- cada cluster tiene su propio payload de Works)
provider_render_hash cardinality:  15  (todos distintos, consistente con dynamic_suffix variando)
fallback_to_legacy count:          0   (los 15 usaron ProviderRender v1, ninguno cayó a legacy)
```

**Gate de la sección 20 del pedido: PASADO limpio.** `stable_prefix_hash` cardinalidad = 1 exactamente como se esperaba (mismo actor, mismo profile, misma rúbrica, mismas policy versions en los 15) — no se disparó el criterio de STOP, no fue necesario investigar diferencias ni clasificar ningún caso como `BUG_DYNAMIC_CONTAMINATION`/`AUTHORITY_DIFFERENCE`/`SERIALIZATION_NONDETERMINISM`.

## Authority / Content-Equivalence Gate

Comparación directa, byte a byte, contra el render legacy (R10) para el mismo snapshot representativo (204, `task:139`, cluster de 8 Works):

| | legacy (R10, `shadow-compile`) | ProviderRender v1 |
|---|---:|---:|
| stable prefix bytes | 26,841 (suma cruda de los 14 segmentos estables ya proyectados) | 26,975 |
| dynamic suffix bytes | 17,836 | 17,836 (**idéntico exacto**) |

Diferencia de 134 bytes en el prefijo estable: **exactamente** el header nuevo (`organization_id`/`actor_role_id`/`purpose`, 3 líneas de texto explícito, nunca presente como contenido de segmento en el legacy) más los separadores deterministas entre segmentos (`\n\n`, 2 bytes × 13 uniones) — no hay pérdida de contenido, solo la adición documentada del header (sección 4 del pedido, campos ya clasificados "contextualmente relevantes y estables" en el audit de Fase 1).

**Verificación de autoridad**: los 15 snapshots preservan las mismas 7 `AuthorityTier` en el mismo orden relativo (`immutable_safety`, `owner_decisions`, `canonical_registry_and_policies`, `organization_agent`, `department_agent`, `role_profile` → `StablePrefix`; `task_context` → `DynamicSuffix`) — verificado programáticamente por los tests `TestBuildProviderRender_SafetyContentPreserved`, `...OwnerDecisionsPreserved`, `...ApplicablePoliciesPreserved`, `...RoleAuthorityPreserved`, `...TaskContextPreservedInSuffix` (ver `go test ./internal/contextengine/...`). **0 tiers requeridos excluidos, 0 pérdida de contenido de safety/owner/policy/role.**

## Conclusión de esta fase

Gate PASADO en su totalidad: cardinalidad esperada exacta en los tres hashes, 0 fallback, contenido y autoridad preservados con evidencia byte-exacta. Se procede al canario real de 5 clusters DeepSeek (siguiente reporte).
