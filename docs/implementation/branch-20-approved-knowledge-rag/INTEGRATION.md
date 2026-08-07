# Rama 20 — Approved Knowledge + RAG

## Estado

Implementación cerrada para validación externa en PostgreSQL real.

- Base exacta: `661b31799a307c81ead84adfc3a226bcbf9060be` (`main` post R18/R19).
- SHA final de esta rama (worktree local, sin push): `9f1deae73a3172730e84afd20405be8e9b0cb939`.
- Migración propia: `000017_create_approved_knowledge_rag`.
- No hay PR ni merge a `main` en este estado. **No se hizo push ni merge**, por instrucción explícita del prompt de R20.
- R21 se desarrolla en paralelo desde el mismo SHA base sin competir por `000017`.

## Objetivo

R20 implementa la frontera de conocimiento organizacional aprobado y su conversión en una fuente RAG real para el Context Engine:

```text
source (research/agent/human) -> knowledge candidate
  -> classification + provenance + admission/sanitization evidence
  -> independent review/approval
  -> approved immutable knowledge version
  -> deterministic chunking
  -> retrieval index generation
  -> authorized query scoped a namespace del actor
  -> cited RAG evidence -> Context Engine tier 6 (untrusted data)
```

Un agente puede **proponer** conocimiento (`rag.propose_candidate`); nunca puede convertirlo unilateralmente en conocimiento aprobado/indexado (`rag.publish_approved`). `organization.yaml` fija `investigacion.publishes_directly_to_approved_rag: false`; esta rama no relaja esa propiedad.

## Modelo de dominio (`internal/rag`)

- `KnowledgeDocument`: identidad lógica dentro de organización + namespace.
- `KnowledgeVersion`: contenido/provenance inmutable (namespace, título, body normalizado, source kind/reference, evidence refs, admisión, content/canonical hash, supersession) + proyección de lifecycle mutable (lifecycle, reviewer, revision) con optimistic concurrency.
- `Chunk` / `IndexGeneration`: salida determinista del chunking y las generaciones de índice.

### Lifecycle

Máquina default-deny, idéntica en forma a la de Rama 18:

```text
candidate -> approved | rejected
approved  -> deprecated
deprecated -> archived
rejected   -> archived
archived   -> <none>
```

Corregir contenido aprobado crea una nueva versión/supersession; nunca muta el contenido publicado.

### Admission boundary

Reutiliza el modelo conceptual de R18 sin acoplar RAG al store de memoria: `public|organizational|sanitized` admitidos; `clinical|secret|desconocido` rechazados fail-closed; `sanitized` exige `sanitization_evidence_ref` explícito. R20 nunca persiste payload clínico original ni consulta tablas clínicas.

### Namespace isolation

El namespace se deriva del registry materializado (`internal/rag/roles.Resolver`), nunca de texto libre: `own` → `role.ID`; `department` → `role.UnitID`. `rag_topics_source_text` se trata explícitamente como señal temática, no como corpus (verificado por fitness).

### Chunking

`internal/rag/chunking.go`: empaquetado determinista por párrafos hasta 1200 bytes, con hard-split por runas para párrafos que exceden el límite. Mismos bytes + mismo `chunker_id`/`chunker_version` ⇒ mismos ordinal/offsets/hash. Cubierto por `TestDeterministicChunkingIsStableAcrossRuns` y `TestChunkingBoundsLargeParagraphs`.

### Index generations

Cada `Reindex` crea una generación `building`, inserta sus chunks (el trigger de Postgres rechaza indexar cualquier versión que no esté `approved`), desactiva la generación activa previa (`active`→`superseded`) y activa la nueva — las dos últimas operaciones en la misma transacción serializable, por lo que nunca coexisten dos generaciones activas. La query de retrieval solo lee la generación `active` y hace `JOIN` contra `rag_knowledge_versions` filtrando `lifecycle='approved'` como segunda barrera (una versión deprecada desaparece de resultados nuevos aunque el reindex todavía no haya corrido).

### Embedding boundary (sin pgvector)

`EmbeddingProvider`/`EmbeddingRef` quedan definidos como puerto; `rag_knowledge_chunks` tiene columnas opcionales `embedding_model_id/version/dimension` (nulas en producción). El retrieval productivo de esta rama es 100% PostgreSQL 17 stock: `tsvector` generado (`content_tsv`), índice GIN, `plainto_tsquery` + `ts_rank`, orden estable por `(score DESC, chunk_id ASC)`. No se agregó pgvector ni se tocó la imagen `postgres:17-bookworm`.

## Authorization

Reutiliza las capabilities existentes — **no se agregó ninguna nueva** a `capability-matrix.yaml`:

| Operación | Capability | approval mode |
|---|---|---|
| `propose` | `rag.propose_candidate` | (vacío) |
| `review` / `reindex` / `deprecate` / `archive` | `rag.publish_approved` | `policy_or_human` |
| query scope `department` | `rag.read_department` | (vacío) |
| query scope `own` | `rag.read_own_namespace` | (vacío) |

**Hallazgo importante durante la validación:** `rag.publish_approved` (igual que `organization.activate_skill` en R19) declara `approval: policy_or_human`/`owner` en la matriz canónica. `internal/authorization.Authorizer.Evaluate` (el evaluador "crudo") devuelve **siempre** `EffectApprovalRequired` para cualquier capability con `approval` no vacío — incluso para el owner actuando directamente — y nunca resuelve una aprobación consumida. Solo `internal/authorization.Service.Evaluate` (que envuelve al `Authorizer` y sabe resolver `ApprovalRequestID` contra una decisión ya tomada) puede convertir eso en `Allow`. `internal/rag/bootstrap` fue corregido para construir el gate contra `authorization/bootstrap.Open(...).Service` en vez del `Authorizer` crudo; sin este cambio, **ninguna** mutación de RAG habría sido autorizable jamás, ni siquiera por el owner. `internal/rag/manager.go` y el CLI (`orgctl rag review|reindex`) ahora aceptan un `approval_request_id` opcional que se reenvía tal cual al motor de autorización.

Todo intento se autoriza antes de persistir, incluyendo reintentos idempotentes (`TestManagerProposeIsIdempotentAndAlwaysAuthorized`). No hay bypass para research worker, Context Engine, model runtime, CLI ni CEO agent — el owner también pasa por el mismo `Service.Evaluate`.

## Persistencia PostgreSQL — migración `000017`

Tablas: `rag_knowledge_documents`, `rag_knowledge_versions` (contenido inmutable + proyección de lifecycle guardada por trigger), `rag_knowledge_evidence_refs`, `rag_knowledge_lifecycle_events`, `rag_knowledge_idempotency`, `rag_index_generations`, `rag_knowledge_chunks` (con `content_tsv` generado + índice GIN).

Invariantes DB (todas con trigger, no solo validación Go):

- contenido/provenance/admission inmutables tras el insert de una versión;
- transición de lifecycle default-deny, exige evento de auditoría insertado en la misma transacción con el mismo `updated_at`;
- provenance de revisión inmutable tras la primera revisión;
- **indexar exige `lifecycle='approved'`** (`rag_guard_chunk_insert`, probado directamente vía SQL crudo en el test de integración);
- una sola generación `active` por `(organization_id, namespace_kind, namespace_id)` (índice único parcial);
- filas de auditoría/evidencia/idempotencia/chunks sin `UPDATE`/`DELETE`; versiones y generaciones sin `DELETE`;
- concurrencia optimista en lifecycle y en activación de generación.

Tres bugs reales encontrados y corregidos únicamente al correr contra Postgres real (no aparecían en tests con fakes):

1. Faltaba `UNIQUE (organization_id, version_id, canonical_hash)` en `rag_knowledge_versions`, requerido por la FK de `rag_knowledge_idempotency`.
2. `SELECT MAX(generation) ... FOR UPDATE` — Postgres prohíbe `FOR UPDATE` con funciones de agregado; se removió el lock y se confía en la transacción `SERIALIZABLE`.
3. Los `chunk_id` se derivaban solo de `version_id`+`ordinal`, así que reindexar la misma versión aprobada en una segunda generación chocaba con los chunks de la primera; se agregó `generation_id` a la identidad del chunk.

## CLI

`orgctl rag`: `propose | review | get | list | reindex | query`. Mutaciones con JSON estricto (`DisallowUnknownFields`, single top-level value), igual que `memory`/`skill`. `review` acepta `outcome` = `approve|reject|deprecate|archive` y despacha al método de `Manager` correspondiente. No existe `rag publish-direct`. `query` deriva el namespace del actor vía registry — el payload no puede pedir un namespace arbitrario.

## Context Engine

`internal/rag/contextprovider.Provider` implementa `contextengine.RAGEvidenceProvider` y reemplaza `UnavailableRAGProvider` en `internal/contextengine/bootstrap` (que ahora construye el runtime de RAG completo vía `rag/bootstrap.Open`). Cada resultado de `Query` se convierte en `SourceRecord` con `Kind=SourceRAGEvidence`, `AuthorityTier=TierRAGEvidence`, `InstructionClass=InstructionData`, `TrustClass=TrustUntrusted`, `MayGrantCapabilities=false`, `DataClass` preservado. `ValidateVersion` detecta drift si la versión dejó de estar aprobada, cambió de hash, o cambió la generación activa del namespace — codificado en el campo `Version` del `SourceRecord` (`rag-knowledge-chunk.v1:<namespace_kind>:<namespace_id>:<version_id>:<canonical_hash>:<generation_id>:<chunk_hash>`).

## Tests ejecutados

Unitarios (`internal/rag`, `internal/rag/authz`, `internal/rag/roles`, `internal/rag/contextprovider`, `cmd/orgctl`): lifecycle default-deny, admisión clínica/secreta fail-closed, sanitized requiere evidencia, hash canónico detecta tampering, chunking determinista y con bounds, idempotencia de propose (autoriza en cada intento), conflicto de revisión obsoleta, flujo completo draft→approved→reindex→query, `Reindex` rechaza versiones no aprobadas aunque el repositorio se comporte mal (`misbehavingApprovedRepository`), ruteo de capability por operación, fail-closed en deny/approval-required, resolución de namespace desde el registry (nunca texto libre), y el `RAGEvidenceProvider` siempre produce registros untrusted/no-capability y detecta drift.

PostgreSQL integration (`internal/rag/postgres`, tag `integration`): tip de migración 17, alta de conocimiento idempotente con conflicto ante contenido distinto, candidate invisible a `Query`, aprobación + reindex + `Query` con metadata de citación completa, activación atómica de generaciones (exactamente una activa, la anterior queda `superseded`), revisión obsoleta rechazada, DB rechaza una transición saltada escrita directamente contra la tabla, DB rechaza indexar una versión no aprobada, inmutabilidad de contenido/eventos/idempotencia/versión, deprecar retira el conocimiento de queries nuevas preservando el audit trail, rechazo de clases de datos clínicas/secretas escritas directo contra la tabla.

`go test -race -short ./internal/rag/... ./cmd/orgctl`: verde.

Smoke real end-to-end contra PostgreSQL vía `make test-integration` (bloque `all` de `scripts/test-integration.sh`, roles reales del registry canónico: `empresa/human` propone/aprueba/reindexa/deprecia, `ingenieria_ia/orquestador` consulta autorizado y construye contexto, `creativo/copywriter` consulta sin autorización):

A. `rag propose` conocimiento `organizational` → `candidate`.
B. `rag query` antes de aprobar → `[]`.
C. request→decide→`rag review --outcome approve` con `approval_request_id` → `approved`.
D. request→decide→`rag reindex` → generación `active`.
E. `rag query` autorizado → chunk + `document_id`/`canonical_hash`/evidence refs.
F. `rag query` con rol sin `rag.read_department` → exit 6 (`authorization capability denied: grant_missing`).
G. `context build` + `context render` → segmento `source_kind:"rag_evidence"`, `trust_class:"untrusted"`, `may_grant_capabilities:false`.
H. request→decide→`rag review --outcome deprecate` → `deprecated`.
I. `rag query` tras deprecar → `[]` de nuevo.
J. `context validate` del snapshot previo → falla con drift (`rag knowledge cli-rag-smoke is no longer approved (lifecycle=deprecated)`).

`make verify`: verde. `make verify-all` (incluye `test-rag-integration`, `test-integration` con el smoke anterior, y las 7 suites legacy con el tip de migración corregido de 16→17): verde.

## Riesgos residuales

- **`rag.publish_approved` requiere el flujo completo `authorization request → decide → evaluate-with-approval-request-id`** para cada mutación (review/reindex/deprecate/archive), incluso para el owner. El digest de acción debe calcularse exactamente igual que `internal/rag/manager.go:mutationDigest`/el digest de reindex — no hay un endpoint que lo exponga de antemano; el smoke script lo replica en bash a partir del `canonical_hash` devuelto por `propose`. Un cliente real (CLI humano u orquestador) necesita conocer esta mecánica; vale la pena, en una rama futura, exponer el digest esperado en el mensaje de error de `ErrApprovalRequired` de forma estructurada (JSON) en vez de solo en texto.
- **El mismo patrón (`approval` no vacío + gate construido contra el `Authorizer` crudo) muy probablemente afecta a R19** (`organization.activate_skill` tiene `approval: owner`): `internal/skillregistry/bootstrap` construye su gate contra `authorization.NewWithPolicyReader(...)` (el `Authorizer` crudo), igual que R20 tenía antes de este fix. No se tocó R19 en esta rama (ya está mergeada a `main`); si nadie corrió un smoke end-to-end de `skill activate/suspend/retire/assign/revoke` contra autorización real, es probable que esas operaciones estén hoy en la misma situación en la que estaba RAG antes de este fix. Vale la pena una validación dedicada.
- El chunking es determinista pero no reconoce límites semánticos (oraciones/markdown); documentos con estructura compleja pueden cortar mitad de una idea. Aceptable para FTS; sería el primer punto a revisar si se introduce una ruta de embeddings.
- `EmbeddingProvider` no tiene ninguna implementación de producción todavía (a propósito, ver Alcance D del DESIGN).

## Comandos exactos de reproducción

```bash
cd /opt/explorarte/worktrees/approved-knowledge-rag   # branch feat/20-approved-knowledge-rag @ 9f1deae7
go fmt ./...
go build ./...
go vet ./...
go test ./internal/rag/... ./internal/contextengine/... ./cmd/orgctl
go test -race -short ./internal/rag/... ./internal/contextengine/... ./cmd/orgctl
make test-rag-fitness
make test-rag-integration
make verify
make verify-all
```

No se abrió PR. No se hizo merge a `main`.
