# Rama 18 — Organizational Memory

## Estado

Implementación cerrada para validación externa en PostgreSQL real.

- Base efectiva: `main` con Rama 17 (`0499c9d57eb79f2f4918c687d73d2d0232e09090`).
- Migración propia: `000015_create_organizational_memory`.
- Rama de trabajo rebaseada: `rebase/18-organizational-memory-on-r17`.
- La rama pública `feat/18-organizational-memory` debe apuntar a esta historia antes del handoff final.
- No hay PR ni merge a `main` en este estado.
- La validación PostgreSQL/`make verify-all` debe ejecutarse en el worker del VPS antes de considerar la rama mergeable.

## Objetivo

Rama 18 implementa memoria organizacional durable, evidence-backed, revisada y consumible por el `MemoryProvider` del Context Engine. No implementa scratchpad, chain-of-thought, memoria clínica, RAG ni búsqueda vectorial.

La propiedad central es que un agente puede **proponer** aprendizaje, pero no convertirlo por sí solo en conocimiento organizacional aprobado.

## Modelo de dominio

Una `Entry` conserva de forma inmutable:

- `organization_id`
- `role_id`
- `category`
- `problem`
- `correction`
- `source_kind`
- `source_run_id`
- `evidence_refs`
- `proposed_by`
- `admission`
- `supersedes_entry_id`

El lifecycle mutable conserva `status`, reviewer, timestamps y `revision` de optimistic concurrency.

### Source kind

Rama 18 distingue explícitamente la procedencia de la experiencia:

- `operational`: aprendizaje observado en una ejecución/proyecto operativo;
- `simulation`: experiencia generada por un proyecto o escenario simulado;
- `synthetic_test`: benchmark/test sintético determinista.

`source_kind` forma parte del hash canónico y de la versión durable. Una experiencia simulada no puede convertirse silenciosamente en evidencia operacional.

Esto prepara el contrato para futuros generadores masivos de escenarios sin acoplar el núcleo de memoria a un proveedor/modelo concreto.

## Clinical boundary

Rama 18 es **admission-only** respecto de datos sensibles. No lee bases clínicas, no clasifica registros clínicos y no sanitiza payloads clínicos.

Todo candidate recibe una `AdmissionAttestation` producida aguas arriba:

- `data_class`
- `attested_by`
- `source_boundary`
- `evidence_ref`
- `sanitization_evidence_ref` cuando corresponda
- `attested_at`

Clases admitidas:

- `public`
- `organizational`
- `sanitized`

Clases rechazadas fail-closed:

- `clinical`
- `secret`
- desconocidas

`sanitized` requiere evidencia explícita de sanitización. R18 nunca persiste el payload clínico original.

## Lifecycle

Máquina default-deny:

```text
candidate  -> approved | rejected
approved   -> deprecated
deprecated -> archived
rejected   -> archived
archived   -> <none>
```

Una corrección de contenido no muta una versión aprobada. Se propone un nuevo candidate con `supersedes_entry_id`.

La review provenance se fija en la primera transición desde `candidate` y no puede reescribirse posteriormente.

## Authorization

Rama 18 reutiliza capabilities canónicas existentes:

- `memory.propose`
- `memory.approve`

`internal/memory.Manager` autoriza antes de persistir.

- `Propose` requiere `memory.propose`.
- `Review`, `Deprecate` y `Archive` requieren `memory.approve`.
- La autorización se evalúa contra la revisión organizacional materializada actual.
- El dominio puro no importa `internal/authorization`; `internal/memory/authz` adapta el engine canónico.

No existe bypass de publicación desde el Context Engine ni desde un modelo.

## Persistencia PostgreSQL

Migración `000015` crea:

- `organizational_memory_versions`: contenido/provenance inmutable;
- `organizational_memory_entries`: proyección actual + optimistic concurrency;
- `organizational_memory_evidence_refs`: evidencia append-only;
- `organizational_memory_state_events`: audit trail append-only;
- `organizational_memory_idempotency`: replay protection durable.

Invariantes DB relevantes:

- `data_class` solo puede ser `public|organizational|sanitized`;
- `source_kind` solo puede ser `operational|simulation|synthetic_test`;
- contenido, evidence refs, eventos e idempotencia no aceptan UPDATE/DELETE;
- lifecycle no acepta DELETE;
- un lifecycle row nace únicamente como `candidate`, revision 1;
- cada creación/transición requiere un evento de auditoría correspondiente **ya insertado en la misma transacción**;
- revisión requiere reviewer + timestamp;
- reviewer/reviewed_at quedan inmutables tras la primera revisión;
- revision avanza exactamente en +1;
- `supersedes_entry_id` debe pertenecer al mismo role namespace.

El store usa transacciones serializables y `FOR UPDATE` para mutaciones.

## Idempotencia y duplicados

`CreateCandidate` requiere idempotency key.

- misma key + mismo hash => devuelve la entrada existente;
- misma key + contenido/provenance diferente => conflicto;
- duplicate exacto por hash canónico => reutiliza la versión existente y registra la nueva key;
- aprendizaje diferente => nuevo candidate; no overwrite.

El hash canónico incluye contenido, evidence refs, admission provenance, `source_kind` y supersession. Excluye estado/reviewer/revision para que el lifecycle no cambie la identidad de lo revisado.

## Context Engine

`internal/contextengine/bootstrap` reemplaza `UnavailableMemoryProvider` por el provider productivo de Rama 18.

Solo memoria `approved`, del `organization_id` y `actor_role_id` solicitados, puede convertirse en `SourceRecord`.

Toda memoria aprobada entra como:

- `SourceApprovedMemory`
- `TierApprovedMemory`
- `InstructionData`
- `TrustUntrusted`
- `MayGrantCapabilities=false`

El contenido renderizado expone `source_kind`, de modo que un agente puede distinguir aprendizaje simulado de operacional.

`ValidateVersion` invalida un snapshot si la memoria dejó de estar aprobada o cambió su versión/hash.

Rama 18 no modifica el assembler.

## CLI

`orgctl memory` agrega:

```text
propose
review
deprecate
archive
get
list
```

Los comandos de mutación aceptan JSON estricto (`DisallowUnknownFields`) y rechazan múltiples valores top-level.

`propose` requiere `source_kind` explícito. El `organization_id` no se toma del payload: viene de configuración/runtime.

## Simulated experience pipeline — contrato futuro

Rama 18 no ejecuta simulaciones ni llama modelos. Sí deja preparado el boundary para una rama posterior:

```text
Scenario Generator
  -> simulated project/task execution
  -> structured outcome + evidence
  -> memory candidate (source_kind=simulation)
  -> independent evaluator/reviewer evidence
  -> memory.approve policy gate
  -> approved organizational memory
  -> optional retrieval/vector index
```

Reglas para esa integración futura:

1. un simulador solo propone candidates;
2. un reviewer LLM puede producir verdict/evidence, pero no escribir `approved` directamente;
3. simulación y experiencia operacional nunca pierden su etiqueta de origen;
4. retrieval/embeddings indexa preferentemente memoria aprobada y debe respetar deprecación/archive;
5. embeddings no son un mecanismo de verdad, autorización ni aprobación;
6. no debe usarse la salida del mismo bucle como única evidencia de su propia corrección.

Esto permite usar distintos proveedores/modelos sin introducir dependencias vendor-specific en `internal/memory`.

## Tests incluidos

Unitarios:

- lifecycle exhaustivo default-deny;
- clinical/secret fail-closed;
- sanitization evidence;
- admission temporal;
- reviewer provenance;
- canonical hash;
- source kind obligatorio y simulation preservada;
- authorization manager;
- strict CLI JSON;
- Context Engine untrusted/non-capability boundary y drift.

PostgreSQL integration:

- migration tip `000015`;
- candidate round-trip;
- idempotencia/conflictos;
- `source_kind=simulation` round-trip;
- review/deprecate + optimistic concurrency;
- audit-event requirement;
- DB rejection de clinical/secret;
- immutability de versiones/evidence/events/idempotencia;
- lifecycle delete rejection;
- sanitization evidence.

Fitness:

```bash
make test-memory-fitness
make test-memory-integration
```

`make verify` incluye memory fitness y `make verify-all` incluye memory integration.

## Handoff al worker del VPS

El worker debe tratar esta rama como **candidate implementation**, no como código ya validado.

Orden recomendado:

```bash
go fmt ./...
go test ./internal/memory/... ./cmd/orgctl
make test-memory-fitness
make test-memory-integration
go test -race -short ./internal/memory/... ./cmd/orgctl
make verify
make verify-all
```

Además debe revisar todas las suites legacy que hardcodean el migration tip. Rama 17 dejó `14`; Rama 18 eleva el tip a `15`. Cambiar solo assertions que representan el tip global; no incrementar mecánicamente contadores de reapply en tests que bajan únicamente migraciones anteriores.

El worker debe validar explícitamente:

- `000015 down/up` real;
- triggers y orden event-before-state;
- concurrent duplicate proposal/idempotency;
- concurrent stale review;
- owner `memory.approve` y denial para roles sin grant;
- CLI propose/review/list;
- un context build que incluya memoria aprobada y excluya candidate/deprecated;
- `source_kind=simulation` visible en el contexto;
- ausencia de raw clinical payloads.

Cualquier corrección descubierta por estos tests debe permanecer en la rama R18. No abrir PR ni mergear `main` hasta que la suite esté verde y exista autorización explícita.

## Fuera de alcance

- llamadas a Gemini/otros proveedores;
- generación automática de escenarios;
- scheduling 24/7 de reviewers;
- embeddings/vector index/RAG;
- auto-approval por LLM;
- memoria clínica;
- chain-of-thought o scratchpad;
- DGM/self-modifying code.
