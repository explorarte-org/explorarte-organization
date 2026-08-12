# R10_DESIGN_AUDIT.md

Auditoría + diseño de R10 (Context & Inference Economy V1). **Solo diseño — nada implementado.** Espera aprobación antes de tocar código.

**Nota sobre la precondición de la sección 0**: r9 terminó con veredicto **ITERATE**, no PASS/PASS_WITH_CHANGES — el bloqueador fue confiabilidad del provider (46.7% terminal-valid), no ninguno de los invariantes que la precondición lista explícitamente (identidad exacta, 0 huérfanas, completitud de output, accounting correcto, 0 costo invisible, cache telemetry funcional, build/adapter identity persistida, `go test ./...` verde) — esos SÍ están limpios, verificados en producción real. Procedo con el diseño porque me lo autorizaste explícitamente después de ver este detalle, no porque decida ignorar la precondición.

## A. R9 baseline exacto

- 15/15 task identity correcta (0 mismatch, 0 huérfanas al cierre — 1 bug de vocabulario `AttemptOutcome` encontrado y corregido en vivo).
- 24 invocaciones reales al provider (12 éxito de dispatch, 12 fallo), **0 fallos pre-provider**.
- 7/15 clusters con resultado terminal válido (dispatch + contrato semántico).
- Total prompt tokens (24 intentos): **1,946,017**. Total output tokens: **127,079**.
- Cache hit tokens: **0** en el 100% de las invocaciones donde el dato está disponible (12 del lado de fallo; el lado de éxito no captura cache tokens aún, gap documentado en `P0_FIX_EVIDENCE.md`).
- Cache miss tokens: igual al total de prompt tokens en esas 12 (consistente con 0% hit).
- Costo real del provider: **$0.30802450**, 100% `actual_provider_reported` — 0% invisible.
- Unreconciled exposure: **$0** — no quedó ninguna invocación en estado `estimated_pending_reconciliation` en r9 (todas las 24 resolvieron a `actual` porque el adapter siempre logró decodificar el envelope externo antes de fallar, incluso en `response_truncated_empty`).
- Latencia: mediana ~41.4s, p95 ~96.5s (sobre 24 intentos).
- Piso fijo de contexto: **~74,100 tokens** (consistente con el ~73,000 de r8).

## B. Desglose real del piso fijo — medido, no estimado

Se consultó `context_segments` (tabla real del Context Engine, no inferencia) para el snapshot 166 (invocación 149, cluster rag `scluster-ee06984a5f0f49d5`, 2 Works). **15 segmentos, los 15 incluidos, 0 omitidos:**

| render_ordinal | authority_tier | source_reference | bytes | % del total |
|---|---|---|---:|---:|
| 1 | immutable_safety | `docs/canonical/cell-boundaries.yaml` | 972 | 1.2% |
| 2 | owner_decisions | `docs/canonical/decisions-required.yaml` | 1,761 | 2.1% |
| 3 | canonical_registry_and_policies | `docs/canonical/architecture-characteristics.yaml` | 2,084 | 2.5% |
| 4 | canonical_registry_and_policies | `docs/canonical/capability-matrix.yaml` | 6,431 | 7.8% |
| 5 | canonical_registry_and_policies | `docs/canonical/instruction-precedence.yaml` | 1,656 | 2.0% |
| 6 | canonical_registry_and_policies | `docs/canonical/leader-worker-map.yaml` | 1,756 | 2.1% |
| 7 | canonical_registry_and_policies | `docs/canonical/memory-policy.yaml` | 1,050 | 1.3% |
| 8 | canonical_registry_and_policies | `docs/canonical/model-routing.yaml` | 2,224 | 2.7% |
| 9 | canonical_registry_and_policies | `docs/canonical/organization.yaml` | 1,742 | 2.1% |
| 10 | canonical_registry_and_policies | `docs/canonical/reasoning-assurance.yaml` | 2,104 | 2.6% |
| **11** | **canonical_registry_and_policies** | **`docs/canonical/role-catalog.yaml`** | **50,034** | **60.6%** |
| 12 | organization_agent | `AGENT.md` | 1,349 | 1.6% |
| 13 | department_agent | `investigacion/AGENT.md` | 808 | 1.0% |
| 14 | role_profile | `investigacion/research_worker_hourly/PERFIL.md` | 1,725 | 2.1% |
| 15 | task_context | `task:115` (rubric + cluster payload, 2 Works) | 6,810 | 8.3% |
| | **Total** | | **82,506** | 100% |

**No hay segmentos `approved_memory`, `matched_approved_skills`, `project_context` ni `rag_evidence` en este snapshot** — para `research.corpus_curate` esos tiers vienen vacíos hoy (útil: significa que no hay memoria/RAG compitiendo por espacio en este task class todavía).

**Nota de honestidad numérica**: la suma de bytes de segmentos (82,506) + el schema-as-text que el adapter DeepSeek añade (`curation_schema.json`, 1,145 bytes) no explica aritméticamente el total de 75,558 tokens facturados por el provider a una densidad de texto normal (~4 bytes/token esperado, aquí sale ~1.1 bytes/token). La causa más probable es overhead de escaping/estructura JSON al serializar el contenido dentro del cuerpo de la request HTTP (comillas escapadas, envoltorio de mensajes/roles) — **no se verificó exactamente en esta ronda**, queda como pregunta abierta a resolver antes de fijar metas de reducción precisas en tokens (los % de bytes por segmento siguen siendo válidos como proporción relativa, que es lo que importa para decidir qué excluir).

## C. Top context contributors

1. **`docs/canonical/role-catalog.yaml` — 60.6% de TODO el contexto medido.** Es el catálogo completo de **47 roles** de toda la organización (CEO, observer, human, cada departamento) con resumen/rag_topics/reports_to en texto largo por rol. Para una tarea de `investigacion/research_worker_hourly`, el modelo recibe la identidad completa de los otros 46 roles que no tienen ninguna relevancia para curar un cluster de papers.
2. `capability-matrix.yaml` — 7.8%.
3. `task_context` (rubric + payload real del cluster) — 8.3%, este SÍ es dinámico y correcto que varíe.
4. El resto de `canonical_registry_and_policies` (7 archivos más) — entre 1.0% y 2.7% cada uno, ~13% combinado.

**Un solo archivo explica más de la mitad del contexto fijo.** Esta es la palanca de mayor impacto por lejos para R10.

## D. Segmentos que propongo incluir siempre (research.corpus_curate/v1)

- `immutable_safety` completo (972 bytes) — no negociable, autoridad máxima, tamaño trivial.
- `owner_decisions` completo (1,761 bytes) — no hay metadata de scope hoy para filtrar con seguridad (ver F/G), tamaño trivial de todos modos.
- `instruction-precedence.yaml`, `memory-policy.yaml`, `model-routing.yaml`, `organization.yaml`, `reasoning-assurance.yaml`, `architecture-characteristics.yaml`, `leader-worker-map.yaml` — los 7 archivos de policy restantes de `canonical_registry_and_policies`: cada uno es pequeño (1.0-2.7%) y no hay señal clara de que ninguno sea inaplicable a `research.corpus_curate` — se mantienen por precaución (regla de la sección G).
- `organization_agent` (AGENT.md, 1,349 bytes) y `department_agent` (investigacion/AGENT.md, 808 bytes) — identidad organizacional/departamental del propio rol, pequeños, claramente aplicables.
- `role_profile` (PERFIL.md del propio rol, 1,725 bytes) — obviamente requerido.
- `task_context` (payload dinámico del cluster) — la parte que SÍ debe variar por invocación.

## E. Segmento que propongo excluir/proyectar para corpus_curate

**`role-catalog.yaml` — proyectar a solo la entrada del rol solicitante** (`investigacion/research_worker_hourly`), en vez de las 47. Esto NO es exclusión de autoridad — el rol sigue recibiendo su propio contrato completo — es exclusión de identidades AJENAS que no tienen ninguna relación con la tarea. Impacto estimado: de 50,034 bytes a probablemente ~1,000-1,500 bytes (una sola entrada del YAML), una reducción de ~48,500-49,000 bytes, **~59% del contexto total medido**, sin tocar ninguna autoridad real.

Esto requiere una decisión de diseño importante (ver H/I): hoy un segmento = un archivo completo. Para este caso el Context Compiler necesita granularidad de **sub-segmento** (una entrada dentro de un YAML), no solo include/exclude de archivo completo. Alternativas:
1. El Context Compiler recibe el segmento completo pero aplica una proyección determinística conocida y auditable (parseo YAML + filtro por `id == actor_role_id`, con hash del resultado proyectado, no del original) — mi propuesta.
2. Extender `internal/contextengine` para que `role-catalog.yaml` se materialice como N segmentos (uno por rol) en vez de 1 — más invasivo, cambia el Context Engine mismo, fuera del alcance de "no crear un segundo Context Engine paralelo" pero sí requiere tocar cómo se generan segmentos desde ese archivo específico.

Recomiendo la opción 1 para V1 (el Compiler proyecta, no el Context Engine cambia su segmentación) — es más aislado, reversible, y no reinterpreta la política (solo selecciona un subconjunto de datos estructurados ya presentes, sin generar texto nuevo).

**No propongo excluir ningún otro segmento** — todo lo demás ya es pequeño y/o claramente aplicable.

## F. Cómo determinar aplicabilidad de owner/canonical policy

Auditado: **ninguno de los 8 archivos de `canonical_registry_and_policies` ni `owner_decisions` tiene hoy metadata de `scope`/`applies_to` en su YAML** (verificado contra `docs/canonical/decisions-required.yaml` y los demás — son documentos de política de organización completa, sin campo de aplicabilidad por task_class/domain). **Esto es un gap real, reportado tal como pide la sección 9, no resuelto con clasificación LLM silenciosa.**

Para V1: dado que no existe metadata segura, **se aplica la regla de la sección 9/42 — incluir conservadoramente todo lo que no tenga evidencia estructural de inaplicabilidad**. El único caso donde SÍ hay evidencia estructural real (no heurística, no LLM) es `role-catalog.yaml`: el YAML ya tiene un campo `id` por rol, comparable determinísticamente contra `actor_role_id` de la invocación — no es "el LLM decidió que esto no aplica", es una igualdad de string sobre un campo ya estructurado.

## G. Qué hacer cuando el scope es desconocido

Tal como pide la sección 42: **incluir conservadoramente**, registrar `include_reason=scope_unknown_conservative`. Esto aplica a los 8 archivos de policy + owner_decisions (sección D) — se incluyen enteros porque no hay forma determinística segura de proyectarlos hoy. Esto queda registrado explícitamente como gap de metadata a mejorar después (no una limitación oculta).

## H. `ContextProfile` schema propuesto

**Precisión tras leer el código real** (`internal/contextengine/domain.go`, `renderer.go`): `Segment`/`Snapshot` ya existen como tipos Go y reflejan 1:1 la tabla `context_segments` — hoy la granularidad real es "un segmento = un archivo canónico completo" (confirmado: `role-catalog.yaml` es UN segmento de 50,034 bytes, no 47). Esto fija una decisión de diseño concreta: el Context Compiler **no inventa un segundo modelo de segmentación** — recibe el `contextengine.Snapshot` tal cual lo arma el Context Engine hoy, y aplica su propia proyección **sobre el contenido de segmentos ya incluidos**, sin que `internal/contextengine` cambie cómo segmenta nada. La opción 1 de la sección E queda confirmada como la única compatible con el código real; la opción 2 (que el Context Engine materialice `role-catalog.yaml` como 47 segmentos) queda descartada por invasiva.

```go
package contextcompiler

import "github.com/Mireuz13/explorarte-organization/internal/contextengine"

type ContextProfile struct {
    ID              string // "research.corpus_curate"
    Version         string // "v1"
    TaskClass       string // debe matchear exactamente el purpose/task metadata real

    RequiredClasses     []AuthorityTierClass // nunca pueden excluirse
    ConditionalClasses  []ConditionalClass    // incluidos solo si aplicabilidad determinística se cumple
    ExcludedByDefault   []AuthorityTierClass  // nunca incluidos salvo que RequiredClasses/Conditional los reintroduzcan

    Projections []SegmentProjection // proyecciones determinísticas tipo "role-catalog.yaml -> solo mi entrada"

    TokenBudget ContextBudget
}

type ConditionalClass struct {
    Class            AuthorityTierClass
    ApplicabilityRule string // referencia a una función determinística registrada, nunca texto libre
}

type SegmentProjection struct {
    SourceReference string // "docs/canonical/role-catalog.yaml"
    ProjectionRule   string // referencia a función registrada, ej. "role_catalog_self_entry"
}
```

Para `research.corpus_curate/v1` (basado en la medición real de B, no en la lista genérica que traía el pedido original):

```
required: immutable_safety, owner_decisions, canonical_registry_and_policies (completo, sección G),
          organization_agent, department_agent, role_profile, task_context
conditional: approved_memory (ninguno presente hoy en este task class -- se deja el gancho, no se activa),
             matched_approved_skills (idem), project_context (idem), rag_evidence (idem)
excluded_by_default: (ninguno -- no hay tiers no aplicables en este task class hoy, solo la PROYECCIÓN de role-catalog.yaml, que no es exclusión de tier, es reducción de contenido dentro del tier canonical_registry_and_policies)
projections: role-catalog.yaml -> role_catalog_self_entry(actor_role_id)
```

## I. `ExecutionContextView` schema propuesto

**Precisión tras leer `renderer.go`**: `contextengine.Segment` YA tiene `Included`/`OmissionReason`/`ContentHash`/`ByteCount` — es exactamente el `SegmentDecision` que había propuesto como tipo nuevo. No hace falta inventar un tipo paralelo: `ExecutionContextView` puede ser literalmente una función pura `contextengine.Snapshot → contextengine.Snapshot` (mismo tipo, con `Segments` reemplazado por la proyección: algunos con `Content` recortado/proyectado — ej. `role-catalog.yaml` con `Content` reemplazado por solo la entrada del rol, `ContentHash`/`ByteCount` recalculados sobre ese contenido proyectado — y el resto de campos de auditoría, `Reason`, intactos). Esto es una ventaja real: **`PortableRenderer.Render` no se toca en absoluto** — recibe el `Snapshot` proyectado exactamente como recibiría cualquier otro, sin saber que fue proyectado.

```go
package contextcompiler

import "github.com/Mireuz13/explorarte-organization/internal/contextengine"

// Compile produces a PROJECTED contextengine.Snapshot -- same type, same
// invariants the renderer already expects -- never a new parallel
// shape. The original snapshot (canonical, unprojected) is preserved
// unchanged in the DB; this is purely a read-time transformation.
func Compile(profile ContextProfile, canonical contextengine.Snapshot) (CompilationResult, error)

type CompilationResult struct {
    ContextSnapshotID     int64  // referencia al snapshot canónico completo, nunca se pierde ni se muta
    ContextProfileID      string
    ContextProfileVersion string

    Projected contextengine.Snapshot // Segments ya filtrados/proyectados, listo para PortableRenderer.Render sin cambios

    StablePrefixHash    string
    StablePrefixTokens  int
    DynamicSuffixHash   string
    DynamicSuffixTokens int

    AuthorityOrderHash string // hash del orden de precedencia de los segmentos incluidos -- debe ser igual entre R9 y R10 (mismo AuthorityPriority/RenderOrdinal relativo)

    CompiledContextHash  string
    EstimatedInputTokens int
}
```

Cada `Segment` de `Projected.Segments` ya trae `Included`/`OmissionReason` (reason string) por diseño existente del Context Engine — el Compiler solo necesita poblarlos correctamente para los que decide excluir/proyectar, reusando el contrato de auditoría que ya existe en producción en vez de crear uno nuevo.

No se duplican bodies fuera de esto — el snapshot canónico completo (`context_segments`) sigue siendo la fuente de verdad; `Projected` es una vista derivada en memoria para esta invocación, no persistida como una segunda copia permanente salvo que se decida guardarla para auditoría (a definir en implementación).

## J. Propuesta de prefijo estable

Basado en B: el prefijo estable candidato son los segmentos 1-14 (todo excepto `task_context`) — **75,696 de 82,506 bytes (91.7%) son potencialmente estables** si `role-catalog.yaml` se proyecta a la entrada del rol (que es SIEMPRE la misma para `investigacion/research_worker_hourly` mientras no cambie el registro organizacional). El único segmento genuinamente dinámico es `task_context` (payload del cluster, 8.3% del total medido).

**Causa raíz del 0% cache-hit, ahora confirmada leyendo `renderer.go` (ya no es hipótesis)**: `PortableRenderer.Render` serializa un `portableRender{SchemaVersion, SnapshotID, OrganizationID, OrganizationRevisionID, ActorRoleID, Purpose, ProjectRef, TaskRef, RequestHash, PrecedenceHash, CanonicalBundleHash, RenderedHashBasis, Segments}` — y `Segments` es el ÚLTIMO campo de ese struct. `encoding/json` en Go emite los campos en el orden de declaración del struct, así que **todo el JSON hasta llegar a `Segments` — incluyendo `SnapshotID` y `RequestHash`, que son distintos en cada invocación por diseño — antecede byte a byte a los 14 segmentos "estables"** en el texto final. Aunque el contenido de esos 14 segmentos sea perfectamente idéntico entre dos llamadas, el prefijo real que el provider recibe NUNCA es byte-idéntico, porque cambia antes de llegar a ellos. Esto explica completamente el 0% cache-hit medido — no hace falta inspeccionar nada más para confirmar la causa.

**Implicación de diseño directa**: lograr un prefijo cacheable real requiere que el orden de serialización final coloque los segmentos estables ANTES que cualquier campo por-invocación (`SnapshotID`, `RequestHash`, etc.), o que esos campos variables se muevan fuera del cuerpo que efectivamente se le manda al modelo (ej. como metadata de transporte/HTTP, no como parte del contenido textual). Esto sí requeriría un cambio en `renderer.go` (reordenar campos del struct, o separar "cuerpo para el modelo" de "metadata de auditoría interna") — no es un cambio de `internal/contextengine`'s segmentación, pero SÍ es un cambio real al renderer, más invasivo que la proyección de `role-catalog.yaml`. Lo marco como parte necesaria de R10 si el objetivo incluye cache-hit real, pero es una pieza aparte de la proyección de contenido — dos cambios independientes, no uno solo.

## K. Modelo de token budget

Modo V1: **OBSERVE + enforce solo límites duros de safety** (nunca sacrificar `immutable_safety`/`owner_decisions` por presupuesto).

```go
type ContextBudget struct {
    AuthorityReserve int // required tiers -- nunca se reduce
    TaskReserve      int // task_context -- crece con el tamaño del cluster (medido: ~1,200-1,700 tokens/Work)
    OptionalBudget    int // conditional classes si se activan
    OutputReserve     int // ya existe como max_output_tokens, no se toca en R10
    ProviderContextLimit int // límite real del modelo, auditar cuál es para deepseek-v4-flash antes de fijar número
}
```

Sin límite arbitrario todavía — el baseline real (B) es lo que fija los números iniciales, no una meta inventada.

## L. Modelo de auditoría

Cada `SegmentDecision` (sección I) ya es auditable por diseño: `included`/`reason` explícitos, sin texto libre de "el LLM decidió". Esto se persiste junto al `ExecutionContextView` de cada invocación real (nueva tabla o extensión de `context_segments`/`context_snapshots` — a decidir en implementación, no aquí). Pregunta respondible determinísticamente: *"¿por qué esta policy no llegó al modelo?"* → se busca su `SegmentDecision.Reason`.

## M. Fallback / rollback

- **Fallback**: si `TaskClass` no resuelve a un `ContextProfile` conocido (hoy solo existe `research.corpus_curate/v1`), el compilador NO produce una vista mínima arbitraria — usa el comportamiento canónico actual (snapshot completo, sin proyección), igual que hoy. Ninguna otra tarea (CEO, code-runner, QA, etc.) se ve afectada por R10.
- **Rollback**: gate de configuración gobernado (ej. flag en `docs/canonical/model-routing.yaml`-equivalente o variable de entorno leída al construir el contexto) que desactiva el profile y vuelve al render completo sin necesidad de migración destructiva — el snapshot canónico completo sigue existiendo siempre, la proyección es una capa encima, nunca reemplaza los datos fuente.

## N. Tests (resumen — lista completa de 41 casos de la sección 46-51 del pedido se implementa 1:1 en la fase de código, no repetida aquí íntegra)

Los más críticos dado lo medido en B: (1) `role_catalog_self_entry` proyecta exactamente 1 entrada y preserva el contrato completo de ese rol byte-por-byte dentro de la proyección; (2) prefijo estable produce el mismo hash entre dos clusters DISTINTOS (test de la sección 48, crítico para caching real); (3) presupuesto nunca puede excluir `immutable_safety`/`owner_decisions`; (4) `authority_order_hash` idéntico entre el render legacy (R9) y el proyectado (R10) para los segmentos incluidos.

## O. Estimación de contexto R10 resultante

Usando la medición real de B y proyectando solo `role-catalog.yaml` (48,500-49,000 bytes de ahorro, resto sin cambios):

| cluster | bytes R9 (medido) | bytes R10 (estimado) | reducción |
|---|---:|---:|---:|
| 1 Work (piso, ref. cluster 7) | ~81,700* | ~32,700-33,200 | ~59% |
| 8 Works (ej. cluster 5/8/14) | ~88,900* | ~39,900-40,400 | ~55% |
| 18 Works (cluster 3) | ~106,700* | ~57,700-58,200 | ~46% |

*bytes estimados por extrapolación de la relación bytes≈tokens×1.1 medida en B sobre los tokens reales de r9 (sección A) — no medidos directamente para estos 3 clusters específicos, solo para el de 2 Works. El porcentaje de reducción SÍ es confiable (proviene directamente de B, independiente de la conversión bytes/token); el valor absoluto en bytes es una extrapolación razonable, marcada como tal.

## P. Confirmaciones

- **No Memory OS**: no implementado, no anticipado más allá de dejar los tiers `approved_memory`/`matched_approved_skills`/`project_context`/`rag_evidence` como `ConditionalClass` vacíos hoy (gancho futuro, sin lógica).
- **No Tree-RAG**: el payload del cluster sigue siendo exactamente el mismo Knowledge input de r9, sin progressive retrieval.
- **No Full Silver**: no se ejecuta nada sobre los 2,009 clusters.
- **No cambio de modelo**: `deepseek/deepseek-v4-flash` sin cambios, `rubric_version=v1` sin cambios, `cluster_algorithm_version` sin cambios, threshold 0.88 sin cambios.

---

Pendiente tu aprobación antes de implementar. La palanca de mayor impacto y menor riesgo identificada es una sola: proyectar `role-catalog.yaml` a la entrada propia del rol — 47 roles ajenos completamente irrelevantes para curar papers, ~59% del contexto medido, cero pérdida de autoridad real.


---

**Historical runtime evidence referenced by R9–R10.5 was destroyed in the development-database incident of 2026-08-12. The reports and committed implementation remain intact, but the referenced database rows are no longer independently queryable.**
