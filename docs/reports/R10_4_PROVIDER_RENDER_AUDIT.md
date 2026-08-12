# R10_4_PROVIDER_RENDER_AUDIT.md

Fase 1 de R10.4 (design audit, sin implementar todavía, sin llamadas a provider). Precondición verificada: los 4 reportes de R10.3 existen y fueron leídos (`FULL_SILVER_READY=NO`, Gate C fallido por capacidad de créditos — irrelevante para R10.4, que no gasta créditos MiMo; Gate A/B pasaron y no se reinterpretan aquí).

## Preflight

```
HEAD:              7cd60785683cb197b3941974d1727311447af4fa (sin cambios)
Branch:             feat/bootstrap-closure-observability-prolog
Dirty:              50 archivos (acumulado de fases previas, sin tocar)
Migration tip:      000039_add_subscription_billing_provenance (sin cambios)
go test ./...:      verde (confirmado antes de esta fase, ver R10.3 preflight — sin cambios de código desde entonces)
```

## Renderer actual — identificado

`internal/contextengine/renderer.go`, `PortableRenderer.Render(ctx, snapshot) ([]byte, error)`. Serializa un struct `portableRender` completo vía `json.Marshal`:

```go
type portableRender struct {
    SchemaVersion          string
    SnapshotID             int64             // DINÁMICO -- nuevo por cada context snapshot (1 por cluster)
    OrganizationID         string            // estable
    OrganizationRevisionID int64             // estable salvo cambio real de organización
    ActorRoleID            string            // estable (mismo actor en toda la corrida)
    Purpose                string            // estable
    ProjectRef             string            // estable/vacío
    TaskRef                string            // DINÁMICO -- nuevo por cada task (1 por cluster)
    RequestHash            string            // DINÁMICO -- depende del snapshot completo, incluido su propio contenido dinámico
    PrecedenceHash         string            // estable si la política de precedencia no cambia
    CanonicalBundleHash    string            // estable si el bundle canónico no cambia entre clusters
    RenderedHashBasis      string            // constante literal
    Segments                []portableSegment // el contenido real, ver abajo
}
```

## Dónde nace la contaminación — confirmado, no inferido

`json.Marshal` en Go serializa los campos de un struct en el orden de declaración. `SnapshotID` y `TaskRef` (ambos dinámicos, nuevos en cada invocación) están declarados y por tanto se serializan **antes** de `Segments` (el contenido real). El JSON resultante literalmente empieza:

```json
{"schema_version":"explorarte.context.v1","snapshot_id":<N>,"organization_id":"...","organization_revision_id":<N>,"actor_role_id":"...","purpose":"...","task_ref":"task:<N>","request_hash":"<dinámico>","precedence_hash":"...","canonical_bundle_hash":"...","rendered_hash_basis":"...","segments":[...
```

Todo antes de `"segments":[` cambia con cada cluster (al menos `snapshot_id`, `task_ref`, `request_hash`) — **el primer byte del prompt nunca es estable entre invocaciones**, sin importar cuán idénticos sean los segmentos reales.

## Serialization path — confirmado end-to-end

`RenderContextSnapshot` (`internal/modelruntime/bootstrap/runtime.go:362`) llama `contextcompiler.CompileForTaskClass` (proyección de R10, sin cambios) y luego `contextengine.NewRenderer().Render(...)`, devolviendo el `[]byte` completo. `dispatch_service.go:450` lo asigna a `CanonicalRequest.RenderedContext`. Los adapters DeepSeek (`adapter.go:421`) y MiMo (`adapter.go:433`) hacen `renderedContext := string(request.RenderedContext)` y lo insertan **tal cual, completo, como el `content` del único mensaje `role:"user"`** enviado al provider (`Messages: []chatMessage{{Role: "user", Content: renderedContext}}`). No hay ningún paso intermedio que reordene o filtre — el JSON completo del renderer es literalmente el prompt.

## Dónde nace request_hash

`Snapshot.RequestHash` se calcula en `internal/contextengine` al construir el snapshot (fuera del alcance de esta auditoría de rendering — no se tocó, no se investigó su fórmula exacta en esta fase, ya que no es el foco: el problema no es que `RequestHash` sea inestable en sí, es que **está presente en el prompt en absoluto**, cuando su única función real es de auditoría/integridad, no de contenido semántico para el modelo).

## Qué metadata llega actualmente al modelo (y no debería)

| campo | ubicación actual | necesario para el modelo | clasificación |
|---|---|---|---|
| `snapshot_id` | top-level, antes de segments | NO — el modelo no usa un ID de snapshot para curar | AUDIT_ONLY |
| `task_ref` | top-level | NO — el `cluster_id` real ya viaja dentro de `task_context` (segmento dinámico), es lo único que el modelo necesita para saber "de qué cluster hablamos" | AUDIT_ONLY |
| `request_hash` | top-level | NO — hash de integridad, sin valor semántico para la curación | AUDIT_ONLY |
| `precedence_hash` | top-level | NO — hash de integridad de política | AUDIT_ONLY |
| `canonical_bundle_hash` | top-level | NO — hash de integridad del bundle canónico | AUDIT_ONLY |
| `rendered_hash_basis` | top-level | NO — string constante, ni siquiera informativo | AUDIT_ONLY |
| `organization_id`/`organization_revision_id` | top-level | Parcialmente — puede ser semánticamente relevante que el modelo sepa la organización, pero es estable entre clusters de la misma corrida, así que no es el problema (no contamina el prefijo si es constante) | MODEL_IRRELEVANT (para el propósito de cache) pero puede quedarse si es estable |
| `actor_role_id`/`purpose` | top-level | Sí, contextualmente relevante y estable — no contamina el prefijo | mantener |
| `ordinal`/`render_ordinal`/`content_hash`/`byte_count` por segmento | dentro de cada `portableSegment` | NO — son metadata de auditoría del pipeline de render, no contenido para el modelo; `content_hash` en particular es puramente derivado del propio contenido (determinista si el contenido es determinista, así que no rompe el prefijo por sí solo, pero sigue siendo ruido no semántico en el prompt) | AUDIT_ONLY (seguro dejarlo fuera del texto visible al provider, persistirlo solo en el `Snapshot`/DB) |
| `source_reference`/`source_version`/`authority_tier`/`data_class`/`trust_class`/`instruction_class` por segmento | dentro de cada `portableSegment` | Potencialmente sí — pueden ser señales legítimas de autoridad que el modelo debería ver (p.ej. "esto es una política canónica vs. contenido de tarea"). **Requiere decisión explícita, no asumida** — ver sección "Riesgos de autoridad" abajo. | A DECIDIR, no descartar por default |

**Todos estos son producto de auditoría real del código, no suposición** — leídos directamente de `renderer.go` y de las llamadas reales en `dispatch_service.go`/los dos adapters.

## Orden actual de mensajes

Uno solo: `role:"user"`, contenido = el JSON completo descrito arriba. No hay mensaje `system` separado — DeepSeek y MiMo reciben todo, incluida la rúbrica y las instrucciones de formato, dentro de ese único mensaje `user` (mas la instrucción `jsonObjectModeInstruction` que el adapter le concatena al final, fuera del renderer).

## Segmentos — estables vs dinámicos (confirmado contra R10_CONTEXT_SHADOW_REPORT.md, sección segmento-por-segmento)

Para `research.corpus_curate/v1` (único profile con proyección activa), el orden real de segmentos, ya confirmado en R10:

```
1. immutable_safety                    ESTABLE (mismo contenido, mismo actor/purpose)
2. owner_decisions                     ESTABLE
3. canonical_registry_and_policies     ESTABLE (incluye role-catalog.yaml YA proyectado desde R10 a la entrada propia del rol)
4. organization_agent                  ESTABLE
5. department_agent                    ESTABLE
6. role_profile                        ESTABLE
7. task_context                        DINÁMICO -- el único segmento que legítimamente cambia por cluster (Work IDs, títulos, abstracts, cluster_id, candidate_work_ids para adjudicación)
```

Esto ya fue verificado byte-a-byte en R10 (`R10_CONTEXT_SHADOW_REPORT.md`: "task_context permanece exactamente igual en cada caso" para lo que NO debe cambiar, y confirmado que solo `role-catalog.yaml` se proyecta). **La separación StablePrefix=segmentos 1-6, DynamicSuffix=segmento 7 ya existe conceptualmente en los datos** — el problema no es que falte identificar qué es estable, es que el renderer actual serializa TODO (incluida la metadata dinámica top-level) en un solo blob sin esa frontera.

## Riesgos de autoridad — identificados, no resueltos todavía

1. **No perder `AuthorityTier`/`DataClass`/`InstructionClass` por segmento** si el modelo actualmente los usa para distinguir "esto es política canónica inviolable" de "esto es contenido de tarea sugerible" — no se verificó en esta fase si el prompt actual comunica esa distinción de forma legible al modelo más allá del orden implícito de aparición. **Antes de remover estos campos del texto visible, hay que confirmar que el orden de aparición por sí solo preserva la semántica de autoridad** (los segmentos ya aparecen en orden de prioridad: safety primero, task_context último) — es plausible que la autoridad ya se comunique posicionalmente y los campos JSON sean redundantes para el modelo, pero esto se declara explícitamente como **a verificar en el gate de autoridad de la Fase 2 (sección 21 del pedido), no asumido aquí**.
2. **`organization_id`/`organization_revision_id`**: mantenerlos en el prefijo es seguro para cache (estables entre clusters de la misma corrida) pero su remoción total del texto visible debería, en teoría, ser neutral para el modelo — no se recomienda quitarlos sin necesidad, ya que no son la causa del problema.

## Riesgos de hash/integridad

El bug ya documentado en R10 (`context_render_hash_mismatch`) ocurrió porque `dispatch_service.go` recalcula el render vía `RenderContextSnapshot` y lo compara contra `GetContextSnapshot`'s `RenderedHash` — **ambos deben derivar de la misma función de render determinista**. Cualquier `ProviderRender` v1 debe preservar este invariante: una única fuente de verdad para "cuál es el render real que se despachó," nunca dos caminos de cálculo divergentes. La introducción de `StablePrefix`/`DynamicSuffix`/`ProviderRenderHash` como conceptos NUEVOS y separados de `Snapshot.RenderedHash` (el hash canónico existente, que sigue representando la identidad del snapshot proyectado, no el render provider-visible) evita reabrir ese bug siempre que:
- `provider_render_hash` se calcule sobre la construcción REAL usada para el dispatch (nunca un cálculo paralelo).
- `Snapshot.RenderedHash` (el mecanismo de integridad ya existente) se mantenga intacto y sin modificar su semántica actual — `ProviderRender` es una capa nueva, adicional, no un reemplazo.

## Cómo se mantendrá context_render_hash integrity

Propuesta de diseño (no implementada todavía): un único punto de construcción `BuildProviderRender(ctx, snapshot) (ProviderRender, error)` en un paquete nuevo (p.ej. `internal/contextengine/providerrender` o dentro de `contextcompiler`, a decidir en la fase de implementación) que:
1. Toma el `Snapshot` ya proyectado (mismo `contextcompiler.CompileForTaskClass` de R10, sin cambios).
2. Separa segmentos en estables (1-6, todo excepto `task_context`) vs dinámicos (`task_context`).
3. Serializa cada mitad de forma determinista (orden de campos fijo, sin metadata de auditoría top-level, sin `ordinal`/`content_hash`/`byte_count` por segmento en el texto visible).
4. Calcula `stable_prefix_hash`, `dynamic_suffix_hash`, y `provider_render_hash` (sobre la concatenación real usada para el mensaje) — los tres persistidos, ninguno inventado.
5. Es la ÚNICA función que tanto `RenderContextSnapshot` (para el dispatch real) como `GetContextSnapshot` (para el hash de integridad pre-dispatch) invocan — eliminando estructuralmente la posibilidad de reabrir el bug de R10 por tener dos caminos.

## Fallback

Diseño propuesto: si `BuildProviderRender` falla por cualquier razón (profile no reconocido, proyección falla, etc.), fallback determinista y explícito al render legacy actual (`PortableRenderer.Render` sin cambios, tal como existe hoy) — **nunca silencioso**: debe loguearse/contarse (telemetría `provider_render_fallback_count` o similar), igual que el patrón ya establecido en R10 (`FellBackToCanonical`).

## Cobertura de tests planeada

Los ~28 tests enumerados en el pedido (10 de `ProviderRender` determinista, 5 de integridad, 5 de autoridad, 5 de runtime, 3 de regresión) se implementan en la Fase 2 (implementación), no en esta auditoría. Esta auditoría solo confirma que el diseño es viable sin tocar `internal/contextengine` (el `Snapshot`/`Segment`/`Service.Render` existentes permanecen sin modificar — `ProviderRender` es una capa nueva que consume el snapshot ya proyectado, no lo reemplaza).

## Conclusión de la Fase 1

El diagnóstico está confirmado con evidencia directa de código (no inferencia): la causa raíz del 0% cache-hit de DeepSeek en R10/R10.2/R10.3 es que `PortableRenderer.Render` serializa metadata dinámica de auditoría (`snapshot_id`, `task_ref`, `request_hash`) en los primeros bytes del único mensaje `user` enviado al provider, antes de cualquier contenido de segmento — destruyendo la identidad byte-a-byte del prefijo sin importar cuán estables sean los segmentos reales (que, según R10, ya lo son en un 6/7 de los casos). El diseño de separación `AuditEnvelope`/`StablePrefix`/`DynamicSuffix` propuesto por el pedido es arquitectónicamente correcto para resolver esto, es implementable sin modificar `internal/contextengine` core, y preserva el invariante de integridad de hash ya establecido en R10 mediante una única función de construcción compartida.

**Siguiente paso**: implementar `ProviderRender` v1 (Fase 2 del pedido), con tests, shadow determinism sobre los mismos 15 contexts de R10, y solo si el gate de autoridad pasa, el canario real de 5 clusters DeepSeek. No se ha tocado ningún código todavía en esta fase.
