# Rama 20 — Approved Knowledge + RAG

## Estado y base

- Rama: `feat/20-approved-knowledge-rag`
- Base exacta: `661b31799a307c81ead84adfc3a226bcbf9060be` (`main` post R18/R19)
- Migración reservada para esta rama: `000017_create_approved_knowledge_rag`
- R21 se desarrolla en paralelo sin migración propia hasta rebasear sobre R20.

## Objetivo funcional

R20 implementa la frontera de **conocimiento organizacional aprobado** y la convierte en una fuente RAG real para el Context Engine.

El flujo objetivo es:

```text
research / agent / human source
  -> knowledge candidate
  -> classification + provenance + admission/sanitization evidence
  -> independent review / approval
  -> approved immutable knowledge version
  -> deterministic chunking
  -> retrieval index generation
  -> authorized query scoped to actor namespace
  -> cited RAG evidence
  -> Context Engine tier 6 (untrusted data)
```

Un agente puede proponer conocimiento, pero no publicarlo directamente como RAG aprobado. `organization.yaml` ya fija `investigacion.publishes_directly_to_approved_rag: false` y esa propiedad no puede relajarse en esta rama.

## Fuentes canónicas existentes que R20 debe respetar

`capability-matrix.yaml` ya define:

- `rag.propose_candidate`
- `rag.publish_approved`
- `rag.read_department`
- `rag.read_own_namespace`

No crear capabilities paralelas salvo que un test demuestre que el modelo actual es insuficiente; cualquier cambio canónico requiere justificación explícita y no debe hacerse silenciosamente.

`instruction-precedence.yaml` coloca `rag_evidence` en tier 6 (`memory_and_rag`), con `may_grant_capabilities=false` y `treated_as_untrusted_data=true`.

`internal/contextengine` ya define `RAGEvidenceProvider`, `SourceRAGEvidence` y `TierRAGEvidence`; bootstrap sigue usando `UnavailableRAGProvider`. R20 debe sustituir ese stub por el provider productivo de esta rama.

## Alcance A — Approved Knowledge publication boundary

Implementar un dominio nuevo bajo `internal/rag` (o subpaquetes equivalentes sin dependencia circular) con entidades durables para:

- `KnowledgeDocument`: identidad lógica dentro de organización + namespace;
- `KnowledgeVersion`: contenido/provenance inmutable y versión secuencial;
- lifecycle mutable con optimistic concurrency;
- evidence/admission provenance append-only;
- review provenance inmutable;
- supersession explícita de versiones;
- idempotencia durable.

### Contenido mínimo de una versión

- organization ID
- namespace ID
- title
- body/text normalizado
- source kind
- source reference
- source run/task/project reference cuando aplique
- evidence references
- proposed by role
- admission attestation
- content hash / canonical hash
- supersedes version/document reference
- created at

### Admission boundary

Reutilizar las garantías conceptuales de R18 sin acoplar RAG al store concreto de memoria:

Clases admitidas:

- `public`
- `organizational`
- `sanitized`

Clases rechazadas fail-closed:

- `clinical`
- `secret`
- desconocidas

`sanitized` requiere evidencia explícita de sanitización. R20 jamás persiste el payload clínico original.

### Lifecycle

Máquina default-deny recomendada:

```text
candidate -> approved | rejected
approved -> deprecated
deprecated -> archived
rejected -> archived
archived -> <none>
```

Corregir contenido aprobado crea una nueva versión/supersession; no muta el contenido publicado anterior.

### Authorization

- propose -> `rag.propose_candidate`
- approve/reject/publish/deprecate/archive/reindex destructive changes -> `rag.publish_approved`
- todo intento se autoriza antes de persistir, incluyendo retries idempotentes;
- no existe bypass desde research workers, Context Engine, model runtime o CLI.

## Alcance B — Namespace isolation

El namespace durable debe derivarse de la organización materializada, no de texto libre aportado por el modelo.

Mínimo soportado:

- namespace de departamento: `unit_id`
- namespace propio del rol: `role_id` o namespace canónico derivado del rol

Reglas:

- `rag.read_department` permite consultar el namespace del departamento actual del actor;
- `rag.read_own_namespace` permite consultar únicamente el namespace propio derivado del actor;
- owner wildcard sigue sujeto a organization scoping;
- ningún query puede solicitar un namespace arbitrario para elevar alcance;
- cross-organization access es imposible por esquema + query predicates + authorization.

`rag_topics_source_text` es una señal temática para ranking/query expansion, no contenido aprobado. No indexarlo como conocimiento.

## Alcance C — Chunking e index generations

Solo versiones `approved` pueden generar chunks recuperables.

Chunking debe ser determinista y versionado:

- `chunker_id`
- `chunker_version`
- chunk ordinal
- byte/character offsets
- content hash del chunk
- parent knowledge version hash

No cortar a ciegas un documento completo como un solo vector/chunk salvo que esté bajo el límite definido.

Cada indexación crea una `index_generation` identificable. Reindexar no reescribe evidencia histórica: crea nueva generación y activa la nueva generación de forma atómica cuando quedó completa.

Deprecation/archive debe retirar la versión de nuevas búsquedas y propagar tombstone/invalidation a sus chunks/index rows sin borrar la evidencia histórica append-only.

## Alcance D — Embedding model/version boundary

R20 debe modelar explícitamente:

- embedding model ID
- embedding model version
- embedding dimension
- embedding/content hash linkage
- index generation

No introducir un proveedor cloud vendor-specific dentro del dominio RAG.

Usar un puerto `EmbeddingProvider`/`Embedder` para producción futura o para un servicio local dedicado. Tests pueden usar un embedder determinista fake.

**R20 no debe bloquear el Context Engine esperando un proveedor externo de embeddings que todavía no existe.** La recuperación productiva mínima de esta rama debe funcionar sobre PostgreSQL 17 stock mediante full-text retrieval determinista (`tsvector`/`tsquery`) y ranking estable. El esquema deja embeddings/versionado preparados para una posterior ruta vectorial; no agregar pgvector ni cambiar la imagen `postgres:17-bookworm` en esta rama.

La existencia de metadata de embeddings nunca convierte un documento en aprobado ni salta authorization.

## Alcance E — Authorized retrieval + citations

Implementar una query service que recibe como mínimo:

- organization ID
- actor role ID
- organization revision
- query text normalizado
- limit acotado

El query text puede derivarse de `BuildRequest.Purpose`; project/task refs pueden participar únicamente como scope/provenance, no como authority.

La query debe:

1. resolver el actor/namespace desde registry;
2. evaluar `rag.read_department` / `rag.read_own_namespace` según scope;
3. filtrar exclusivamente knowledge `approved` e index generation activa;
4. usar PostgreSQL FTS determinista en R20;
5. producir resultados con citation metadata completa;
6. aplicar límite de segmentos y bytes antes de entregar al Context Engine.

Cada resultado debe preservar al menos:

- knowledge document/version ID
- chunk ID + ordinal
- namespace
- source reference/evidence refs
- content hash
- index generation
- retrieval score/rank
- data class

## Alcance F — Context Engine

Crear provider productivo que implemente `contextengine.RAGEvidenceProvider`.

Solo evidence recuperada/autorizada se convierte en `SourceRecord` con:

- `Kind=SourceRAGEvidence`
- `AuthorityTier=TierRAGEvidence`
- `InstructionClass=InstructionData`
- `TrustClass=TrustUntrusted`
- `MayGrantCapabilities=false`
- data class preservada (`public|organizational|sanitized`)
- reference/version suficientes para citation + drift validation

`ValidateVersion` debe invalidar snapshots si:

- knowledge deja de estar approved;
- cambia/supersede la versión recuperada;
- cambia la active index generation;
- chunk/hash deja de coincidir;
- namespace/authorization ya no es válido.

No modificar la precedencia ni elevar RAG sobre task/project/skills.

## Persistencia PostgreSQL — migración 000017

El worker debe diseñar nombres finales, pero la migración debe cubrir como mínimo:

- knowledge documents / versions
- lifecycle events append-only
- evidence/admission refs append-only
- idempotency
- chunks
- index generations
- retrieval/index metadata
- opcional embedding metadata/values sin pgvector

Invariantes DB requeridos:

- content/provenance inmutable por version;
- lifecycle default-deny;
- event-before-state o atomic event+state en la misma transaction;
- review provenance no reescribible;
- approved-only indexing;
- solo una active generation por namespace/index scope;
- audit/evidence/idempotency/chunk identity sin UPDATE/DELETE destructivo;
- organization + namespace FKs/scoping donde sea posible;
- optimistic concurrency para lifecycle/reindex activation.

## CLI mínima

Agregar `orgctl rag`:

```text
propose
review
get
list
reindex
query
```

Mutations usan JSON estricto (`DisallowUnknownFields`) y single top-level value, igual R18/R19.

`query` no acepta organization/namespace arbitrarios desde payload si runtime puede derivarlos del actor/config.

No existe comando `publish-direct` para research workers.

## Tests obligatorios

### Unitarios

- lifecycle exhaustivo default-deny;
- admission clinical/secret fail-closed;
- sanitized requiere evidence;
- canonical hash/idempotency/supersession;
- namespace derivation y cross-namespace denial;
- capability mapping propose/publish/read;
- deterministic chunking y chunk hashes;
- active-generation switching;
- deprecated/archived exclusion;
- stable deterministic FTS ranking for fixtures;
- citations/provenance completas;
- Context Engine RAG records siempre untrusted/data/non-capability;
- snapshot drift después de deprecate/reindex/supersede.

### PostgreSQL 17 integration

- migration tip `000017`, down/up/reapply;
- candidate -> approved persistence;
- idempotent propose/conflict;
- stale revision conflicts;
- DB blocks direct candidate->deprecated/other illegal transitions;
- DB blocks indexing non-approved knowledge;
- immutable versions/evidence/events/chunks/idempotency;
- atomic active index generation;
- deprecation removes chunks from retrieval without deleting audit history;
- cross-organization and cross-namespace retrieval isolation;
- Context Engine build includes authorized approved RAG and excludes candidate/deprecated;
- FTS query returns citation metadata and deterministic ordering.

### Fitness

Agregar:

```bash
make test-rag-fitness
make test-rag-integration
```

`make verify` incluye fitness; `make verify-all` incluye integration.

Revisar tests legacy que hardcodeen migration tip 16 y elevar únicamente los que realmente representan el tip global.

## Fuera de alcance

- indexar datos clínicos raw;
- auto-aprobar publicaciones generadas por un LLM;
- `investigacion` publicando directo a approved RAG;
- usar `rag_topics_source_text` como corpus;
- chain-of-thought/scratchpads;
- web crawling o ingestion automática de Internet;
- búsqueda activa de papers;
- provider específico de embeddings;
- pgvector/vector DB externo;
- cambiar model routing / R21;
- conectar Skill Registry de R19 al Context Engine;
- DGM/self-modifying code.

## Definition of done / handoff VPS

Antes de declarar R20 mergeable:

```bash
go fmt ./...
go test ./internal/rag/... ./internal/contextengine/... ./cmd/orgctl
go test -race -short ./internal/rag/... ./internal/contextengine/... ./cmd/orgctl
make test-rag-fitness
make test-rag-integration
make verify
make verify-all
```

El worker debe además ejecutar un smoke real en PostgreSQL:

1. proponer conocimiento organizational/sanitized;
2. demostrar que candidate no aparece en query;
3. aprobar con actor autorizado;
4. reindexar;
5. consultar desde un rol autorizado y obtener citation;
6. consultar desde rol/namespace no autorizado y obtener deny;
7. construir Context Engine snapshot con el chunk como `rag_evidence` untrusted;
8. deprecar la versión;
9. validar que un snapshot previo detecte drift y un query nuevo ya no la recupere.

No abrir PR ni mergear `main` hasta que `make verify-all` y esta secuencia estén verdes.
