# Rama 18 — Organizational Memory

## Estado

En desarrollo en paralelo con Rama 17. El dominio puro vive en
`internal/memory`. La persistencia PostgreSQL, el provider productivo para
`internal/contextengine` y la CLI se agregan después de rebasear sobre `main`
cuando Rama 17 cierre, para tomar el siguiente número de migración disponible y
no crear una colisión de migraciones entre ramas paralelas.

## Base exacta

- Rama: `feat/18-organizational-memory`.
- Base: `292495132d69757a87e1e86aff57f62dadb72fcc`.
- La base ya contiene Rama 16 (`Task Obligation & Completion Verifier`).
- No modifica `docs/canonical/*` durante el desarrollo de esta rama.

## Objetivo

Implementar memoria organizacional durable, versionable, evidence-backed y
revisada, consumible por el `MemoryProvider` que Rama 07 dejó como puerto en el
Context Engine. La memoria es conocimiento organizacional de baja autoridad,
no scratchpad del agente, no chain-of-thought y nunca una fuente de
capabilities.

## Canon que gobierna la rama

`docs/canonical/memory-policy.yaml` fija:

- estados: `candidate`, `approved`, `deprecated`, `archived`, `rejected`;
- propuesta con evidencia antes de publicación;
- revisión por rol autorizado u owner;
- publicación versionada;
- solo entradas aprobadas entran al contexto;
- un agente no edita memoria canónica directamente durante su propia corrida;
- memoria no concede capabilities ni sobreescribe policy;
- toda entrada aprobada tiene provenance y reviewer;
- deprecación conserva historial;
- datos clínicos nunca entran a memoria organizacional;
- `MEMORY.md` legacy se clasifica antes de importar y nunca se copia wholesale.

`architecture-characteristics.yaml#memory_provenance` exige además que la base
de datos y un integration test rechacen publicación sin reviewer.

## Decisiones conservadoras de Rama 18

El canon enumera estados pero no publica una matriz completa de transiciones.
Rama 18 aplica una máquina default-deny mínima, sin inventar reaperturas:

```text
candidate  -> approved | rejected
approved   -> deprecated
deprecated -> archived
rejected   -> archived
archived   -> <none>
```

Una corrección de una memoria ya aprobada no muta el contenido aprobado. Se
crea un nuevo candidate con `supersedes_entry_id`, preservando la versión
anterior y su audit trail.

No se implementa expiración automática ni una política temporal de retención:
el canon todavía no define esos parámetros. `archive` es explícito.

## Dominio puro actual

### `Entry`

Contenido requerido por el canon:

- `role_id`
- `category`
- `problem`
- `correction`
- `source_run_id`
- `evidence_refs`
- `status`
- `created_at`

Rama 18 añade metadatos necesarios para hacer verificables los invariantes:

- `organization_id`
- `proposed_by`
- `reviewer_id` / `reviewed_at`
- `classification`
- `supersedes_entry_id`
- `revision` para optimistic concurrency
- `updated_at`

`reviewer_id` no figura en `required_fields` del YAML actual, pero es requerido
por dos fuentes canónicas de mayor precisión operacional: el invariante de
`memory-policy.yaml` y el fitness `memory_provenance` de
`architecture-characteristics.yaml`. Por eso `approved` es estructuralmente
inválido sin reviewer y timestamp de revisión.

### Clinical/secret isolation

Todo candidate exige `ContentClassification` previa. Solo `public`,
`organizational` o `sanitized` pueden entrar al dominio. `clinical` y `secret`
fallan con `ErrForbiddenDataClass` antes de crear el candidate.

La clasificación contiene `classifier_id` y `evidence_ref`. El adapter durable
debe validar esa referencia contra una fuente autorizada; el dominio puro no
confía en texto libre para convertir una clasificación en evidencia.

### Hash canónico

`Entry.CanonicalHash()` fija contenido y provenance, pero excluye ID, estado,
reviewer, timestamps y revisión de optimistic concurrency. Por tanto aprobar,
deprecar o archivar no cambia la identidad del contenido que fue revisado.

## Persistencia durable — después del rebase de Rama 17

Diseño previsto, sujeto al número de migración disponible tras el rebase:

- `organizational_memory_entries`: proyección actual y optimistic concurrency;
- `organizational_memory_versions`: contenido/provenance append-only;
- `organizational_memory_evidence_refs`: referencias verificables;
- `organizational_memory_state_events`: transición/actor append-only;
- constraints/triggers default-deny que reproduzcan la matriz de estados;
- constraint/trigger que haga imposible `approved` sin reviewer;
- UPDATE/DELETE prohibido sobre versiones y eventos de auditoría;
- no raw clinical/secret source payloads en ninguna tabla.

La migración no se numera en el corte paralelo para no competir con la
persistencia que Rama 17 pueda agregar.

## Integración con Context Engine

Rama 07 ya expone:

```go
type MemoryProvider interface {
    ListApproved(context.Context, BuildRequest) ([]SourceRecord, error)
    ValidateVersion(context.Context, SourceRecord) error
}
```

Rama 18 reemplazará `UnavailableMemoryProvider` en el bootstrap productivo por
un adapter que:

1. consulta solo memoria `approved` aplicable al `role_id`/namespace;
2. devuelve `SourceApprovedMemory` en `TierApprovedMemory`;
3. fuerza `InstructionData`, `TrustUntrusted` y nunca
   `MayGrantCapabilities`;
4. devuelve contenido versionado y hash verificable;
5. permite a `ValidateVersion` detectar deprecación, archive, cambio de versión
   o desaparición durante un context build.

No se modifica el assembler de Rama 07.

## Authorization boundary

La rama no inventa nuevas capabilities. Usa las ya canónicas:

- `memory.propose`
- `memory.approve`

El dominio puro no decide permisos. La composition root durable debe resolver
authorization antes de `propose`/`review`; actualmente ninguna autoridad de
agente recibe `memory.approve` por grant general, mientras owner conserva la
capacidad por su grant `*`. Esto mantiene publicación fail-closed.

## Duplicados y conflictos

Rama 18 separa dos problemas:

- duplicado exacto: mismo hash canónico; el store debe resolverlo de forma
  idempotente y no crear dos publicaciones equivalentes;
- corrección distinta de un aprendizaje existente: debe ser un candidate nuevo
  y referenciar explícitamente la entrada que supersede. No se sobrescribe
  contenido aprobado.

No se usa similitud LLM como backstop de integridad. Una futura detección
semántica puede sugerir conflictos, pero nunca publicar o fusionar memoria por
sí sola.

## Legacy MEMORY.md

Los tres archivos legacy declarados por el canon no se importan completos.
Cualquier migración futura debe extraer observaciones individualmente,
clasificarlas, adjuntar evidencia y pasarlas por el mismo lifecycle de
candidate/review que una memoria nueva.

## Fuera de alcance

- RAG/indexación vectorial (Rama 20).
- Skills (Rama 19).
- Memoria clínica o acceso a bases clínicas.
- Chain-of-thought, scratchpad o prompts completos.
- Capabilities derivadas desde memoria.
- Expiración automática no definida por policy.
- Auto-approval por LLM.
- Modificar el Context Engine assembler.

## Criterios de cierre

- matriz de transición default-deny probada exhaustivamente;
- datos `clinical` y `secret` rechazados antes de candidate y nuevamente en DB;
- `approved` imposible sin reviewer/provenance en Go y PostgreSQL;
- optimistic concurrency en mutaciones;
- versiones/eventos append-only;
- exact duplicates idempotentes;
- provider productivo entrega solo approved y detecta drift;
- negative integration tests para clinical isolation;
- migración down/up real en PostgreSQL;
- fitness de memoria integrado a `make verify`;
- `make verify-all` verde después del rebase final;
- sin PR hasta tener la validación completa y autorización explícita del owner.
