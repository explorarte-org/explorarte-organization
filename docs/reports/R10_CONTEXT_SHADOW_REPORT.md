# R10_CONTEXT_SHADOW_REPORT.md

Fase 4 de R10: dry-run del Context Compiler (`internal/contextcompiler`, perfil `research.corpus_curate/v1`) sobre los 15 `context_snapshot` reales de r9.1 (task_ids 130-144). **Ninguna llamada al provider — modo shadow puro**, vía `orgctl contextcompiler shadow-compile <snapshot_id>` (nuevo CLI, solo lectura, reusa `runtime.Service.Get` existente).

## Resultado agregado

**15/15 snapshots compilaron con perfil aplicado, 0 fallback a canónico** (`fell_back_to_canonical=false` en los 15 — el perfil siempre matcheó y los 7 tiers requeridos siempre estuvieron presentes).

| task_id | snapshot_id | bytes originales | bytes proyectados | reducción |
|---|---|---:|---:|---:|
| 130 | 190 | 82,506 | 33,651 | 59.2% |
| 131 | 192 | 91,335 | 42,480 | 53.5% |
| 132 | 194 | 113,122 | 64,267 | 43.2% |
| 133 | 195 | 83,388 | 34,533 | 58.6% |
| 134 | 197 | 93,615 | 44,760 | 52.2% |
| 135 | 199 | 112,856 | 64,001 | 43.3% |
| 136 | 201 | 80,689 | 31,834 | 60.5% |
| 137 | 202 | 93,711 | 44,856 | 52.1% |
| 138 | 203 | 84,714 | 35,859 | 57.7% |
| 139 | 204 | 93,532 | 44,677 | 52.2% |
| 140 | 205 | 82,848 | 33,993 | 59.0% |
| 141 | 206 | 87,609 | 38,754 | 55.8% |
| 142 | 208 | 83,410 | 34,555 | 58.6% |
| 143 | 209 | 93,896 | 45,041 | 52.0% |
| 144 | 210 | 82,687 | 33,832 | 59.1% |
| **Total** | | **1,359,918** | **627,093** | **53.9%** |

La reducción cae con el tamaño del cluster (43.2-43.3% en los 2 clusters de 18/16 Works, donde el payload dinámico es proporcionalmente mayor) y sube en clusters chicos (~59-60% en los de 1-2 Works) — consistente con que la única proyección es sobre contenido FIJO (`role-catalog.yaml`), nunca sobre el payload dinámico del cluster.

## Verificación segmento-por-segmento (task 130, representativo de los 15)

```
immutable_safety                 972 ->    972   (sin cambio)
owner_decisions                1,761 ->  1,761   (sin cambio)
canonical_registry_and_policies (7 archivos)      (sin cambio, cada uno)
canonical_registry_and_policies/role-catalog.yaml 50,034 -> 1,179  *proyectado (role_catalog_self_entry)
organization_agent             1,349 ->  1,349   (sin cambio)
department_agent                 808 ->    808   (sin cambio)
role_profile                   1,725 ->  1,725   (sin cambio)
task_context                   6,810 ->  6,810   (sin cambio, es el payload dinámico)
```

**Confirmado en los 15**: la ÚNICA proyección que ocurre en toda la corrida es `role-catalog.yaml → role_catalog_self_entry`. Ningún otro segmento se tocó, ni de contenido ni de orden. `task_context` (lo único que debe variar entre invocaciones) permanece exactamente igual en cada caso — el compilador nunca proyecta el payload dinámico.

## Verificación de autoridad

- **0 tiers requeridos excluidos** en los 15 (`immutable_safety`, `owner_decisions`, `canonical_registry_and_policies`, `organization_agent`, `department_agent`, `role_profile`, `task_context` — todos presentes e incluidos en cada compilación).
- **0 exclusiones dudosas** — no se excluyó ningún segmento completo, solo se proyectó contenido dentro de uno (role-catalog.yaml), preservando la entrada propia del rol íntegra.
- **`AuthorityOrderHash` no se verificó aquí que sea idéntico a r9.1** porque el test de determinismo de esa propiedad ya está cubierto en `internal/contextcompiler`'s test suite (`TestCompile_AuthorityOrderHashStableAcrossDifferentTaskPayloads`, verifica que el hash de orden es igual entre dos clusters distintos del mismo actor) — no hace falta repetirlo contra datos reales porque el compilador nunca reordena segmentos, solo proyecta contenido dentro de uno ya existente.

## Gate de la sección 53 del diseño

Ningún caso disparó el criterio de STOP ("exclusión dudosa de safety/owner_decisions/canonical policy/role authority") — no hubo ninguna exclusión de segmento completo en los 15, solo la proyección de contenido diseñada y aprobada. **Gate pasado.**

## Siguiente paso

Fase 5 (activar el perfil solo para `research.corpus_curate`) y Fase 6 (ejecutar los 15 clusters reales con el contexto proyectado, comparar contra r9.1) requieren gastar dinero real en DeepSeek — **no ejecutado todavía, esperando autorización explícita** antes de hacer el primer dispatch real con contexto reducido.
