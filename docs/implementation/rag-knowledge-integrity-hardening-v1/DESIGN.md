# RAG/Knowledge/Memory/Embedding/Context Integrity Hardening V1 — DESIGN.md

Branch `feat/rag-knowledge-integrity-hardening-v1`, base commit `e3f66e42a3a9fd0ad5d9ae5851c66b49bdb7581c`. Este documento cubre los **5 findings completados con rigor completo** en esta fase (de un total de 30 secciones en la spec original) — el resto queda explícitamente diferido, ver `HANDOFF.md`.

## Matriz de findings

| FINDING | STATUS | FILES | FIX | TEST | MIGRATION | ROLLBACK |
|---|---|---|---|---|---|---|
| §6 Gemini vector validation | DONE | `internal/embeddingruntime/adapter/gemini/{online,batch,errors}.go` | `validateVector` valida dimensión exacta + NaN/Inf en `Embed` y `ReadBatchResults`, mirroring bgem3 | 4 tests nuevos + 3 fixtures corregidos | N/A | revert de archivos |
| §7 BGE-M3 full identity attestation | DONE | `internal/embeddingruntime/adapter/bgem3/{wire,health,adapter}.go` | wire contract exige `tokenizer_revision`/`normalization`/`pooling` en `/v1/health` y `/v1/embed`, comparados exacto contra Config | fixtures actualizados en 4 archivos de test (incl. 2 en `bootstrap/`) | N/A | revert de archivos |
| §13 Object Storage immutable evidence | DONE | `internal/objectstorage/{client,keys}.go` + tests | `PutObjectIfAbsent` con `If-None-Match: *`, `ErrImmutableObjectConflict`, keys canónicas con SHA256 completo | 38 tests (`client_test.go`+`keys_test.go`) | N/A | revert de archivos |
| §18.3 ProviderRender/contextcompiler partition SoT | DONE | `internal/contextengine/providerrender.go`, `internal/contextcompiler/contextcompiler_compiler.go` | `IsDynamicProviderTier` exportada, única fuente de verdad usada por ambos paquetes | 2 tests nuevos (partición cruzada + byte-equivalencia) | N/A | revert de archivos |
| §21 RAG DB immutability trigger | DONE | `migrations/000041_*`, `internal/rag/postgres/integration_test.go` | trigger `rag_guard_version_update` extendido a 9 campos adicionales (namespace_kind/id, source_kind/reference/run_ref, proposed_by_role_id, source_boundary, admission_evidence_ref, sanitization_evidence_ref) | 1 test de integración (4 mutaciones rechazadas + transición normal aceptada), verificado real contra Postgres alternando función vieja/nueva | 000041 up+down | down restaura la función exacta pre-000041 |

## 1. Defecto confirmado, invariante, corrección elegida

### §6 — Gemini vector validation
**Defecto confirmado**: `online.go`'s `Embed` solo chequeaba `len(embedding.Values) == 0` — nunca dimensión ni NaN/Inf. `batch.go`'s `ReadBatchResults` tenía el mismo gap.
**Invariante**: un vector que llega al caller (RAG/Memory) debe ser garantizadamente comparable (misma dimensión) y finito, o el caller debe recibir un error explícito, nunca un vector corrupto silencioso.
**Corrección**: función `validateVector(vector, expectedDimension)` (mirroring exacto de bgem3's función homónima ya existente), invocada en ambos paths. Batch path usa `expectedDimension=0` (solo NaN/Inf) porque la interfaz `ReadBatchResults` no transporta el `OutputDimensionality` original.
**Alternativas rechazadas**: fusionar la validación en un tipo compartido `embeddingruntime.ValidateVector` — rechazado porque el pedido original (sección 5) explícitamente prohíbe fusionar las familias de adapters en abstracciones gigantes; se mantiene la duplicación deliberada (2 implementaciones casi idénticas, una por adapter) consistente con el patrón ya establecido por bgem3.

### §7 — BGE-M3 full identity attestation
**Defecto confirmado**: `Config` pinea `TokenizerRevision`/`Normalization`/`Pooling`, pero ni `/v1/health` ni `/v1/embed` los pedían al sidecar — un sidecar podía correr con tokenizer/pooling distinto bajo el mismo `model_revision`/`artifact_sha256` sin que el adapter lo detectara.
**Invariante**: nunca persistir como "observed identity" algo que el sidecar no atestiguó explícitamente.
**Corrección**: 3 campos nuevos en `embedWireResponse` y `Health`, comparados exacto contra `Config` en `Healthy()` y `Embed()`, fail-closed (`ErrModelIdentityDrift`) si faltan o difieren.
**Bloqueo externo real**: el sidecar productivo (`/opt/explorarte/bgem3-sidecar/server.py`) sigue sin control de versiones (confirmado: no existe `.git` ahí). No se editó. Documentado como `BGE_SIDECAR_CONTRACT_UPDATE_REQUIRED` (ver `HANDOFF.md`). Confirmado que BGE-M3 no está activo en el `compose.yaml` real — el cambio no rompe nada en producción, pero significa que el perfil seguirá sin poder activarse hasta que el sidecar se actualice para enviar los 3 campos nuevos.

### §13 — Object Storage immutable evidence
**Defecto confirmado**: `PutObject` podía sobrescribir cualquier key; no existía primitive condicional atómica.
**Invariante**: evidence/provenance objects nunca se sobrescriben.
**Corrección**: `PutObjectIfAbsent` con header `If-None-Match: *` (real precondition de OCI, no un HEAD-then-PUT con TOCTOU). En 412 (ya existe), hace `HeadObject` para comparar MD5/Size; si `Content-MD5` no está disponible (objetos multipart), cae a un `GetObject` acotado por `MaxResponseBytes` para comparar bytes directo. Coincide → `PutOutcomeReused`; difiere → `ErrImmutableObjectConflict`.
**Alternativas rechazadas**: simular atomicidad con HEAD-then-PUT — rechazado explícitamente por el pedido original (TOCTOU real).
**Riesgo operacional documentado, no bloqueante**: el soporte real de `If-None-Match` en el signer/client existente nunca se verificó contra un endpoint OCI vivo (nada en esta rama llama a OCI real) — queda como nota en el doc comment de `PutObjectIfAbsent`, a confirmar antes de depender de esto en producción.

### §18.3 — ProviderRender / contextcompiler partition single source of truth
**Defecto confirmado**: `providerrender.go`'s `dynamicAuthorityTiers` trataba 5 tiers como dinámicos (`TierTask`, `TierProject`, `TierRAGEvidence`, `TierApprovedMemory`, `TierApprovedSkill`); `contextcompiler_compiler.go`'s telemetry solo trataba `TierTask`. Divergencia real, aunque latente (research.corpus_curate/v1 no usa hoy los otros 4 tiers, así que nunca produjo un número visiblemente incorrecto en producción).
**Invariante**: la partición stable/dynamic debe ser una única regla, nunca dos implementaciones independientes.
**Corrección**: `contextengine.IsDynamicProviderTier(tier)` exportada, única fuente; ambos paquetes la consumen.
**No se modificó V1 retroactivamente** — el comportamiento real de `BuildProviderRender` no cambió (su propio mapa ya tenía los 5 tiers correctos); solo se corrigió la telemetry de contextcompiler para dejar de divergir.

### §21 — RAG DB immutability trigger
**Defecto confirmado**: la función de trigger `rag_guard_version_update` (migración 000017) no comparaba todos los campos declarados inmutables — verificado en vivo: revirtiendo a la función vieja, un `UPDATE` directo de `source_reference`, `source_boundary`, o `sanitization_evidence_ref` (en el caso `data_class='sanitized'`) pasaba sin error.
**Invariante**: solo `lifecycle`, `reviewer_role_id`, `reviewed_at`, `revision`, `updated_at` pueden cambiar en un `UPDATE` de `rag_knowledge_versions`; todo lo demás es inmutable a nivel DB, no solo a nivel aplicación.
**Corrección**: migración 000041 reemplaza la función del trigger, agregando comparación `IS DISTINCT FROM` para `namespace_kind`, `namespace_id`, `source_kind`, `source_reference`, `source_run_ref`, `proposed_by_role_id`, `source_boundary`, `admission_evidence_ref`, `sanitization_evidence_ref`. Nombres de columna verificados exactos contra el schema real — no hubo discrepancias con la lista de la spec.
**Migración**: 000041 (tip real era 40 al momento de crearla). DOWN restaura la función exacta pre-000041. No se tocó la migración 000017 histórica.

## 2. Seguridad

Ningún cambio de esta fase llama a un provider de IA real, a OCI real, ni al sidecar BGE-M3 real. Todos los tests usan `httptest`/fakes, salvo el test de integración de §21 que usa Postgres real de desarrollo (ver `HANDOFF.md` sección de incidente).

## 3. Costo

Ningún cambio de esta fase introduce llamadas nuevas a providers pagos. §7 hace que BGE-M3 sea temporalmente inutilizable en cualquier ambiente hasta que el sidecar se actualice (ya no estaba activo en producción, así que no hay impacto de costo real).
