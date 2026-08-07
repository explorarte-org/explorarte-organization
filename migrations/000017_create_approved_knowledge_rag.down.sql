DROP TRIGGER IF EXISTS rag_generations_no_delete ON rag_index_generations;
DROP TRIGGER IF EXISTS rag_generation_update_guard ON rag_index_generations;
DROP TRIGGER IF EXISTS rag_chunk_insert_guard ON rag_knowledge_chunks;
DROP TRIGGER IF EXISTS rag_versions_no_delete ON rag_knowledge_versions;
DROP TRIGGER IF EXISTS rag_version_update_guard ON rag_knowledge_versions;
DROP TRIGGER IF EXISTS rag_version_insert_guard ON rag_knowledge_versions;
DROP TRIGGER IF EXISTS rag_knowledge_chunks_immutable ON rag_knowledge_chunks;
DROP TRIGGER IF EXISTS rag_knowledge_idempotency_immutable ON rag_knowledge_idempotency;
DROP TRIGGER IF EXISTS rag_knowledge_lifecycle_events_immutable ON rag_knowledge_lifecycle_events;
DROP TRIGGER IF EXISTS rag_knowledge_evidence_immutable ON rag_knowledge_evidence_refs;
DROP TRIGGER IF EXISTS rag_knowledge_documents_immutable ON rag_knowledge_documents;

DROP FUNCTION IF EXISTS rag_reject_generation_delete();
DROP FUNCTION IF EXISTS rag_guard_generation_update();
DROP FUNCTION IF EXISTS rag_guard_chunk_insert();
DROP FUNCTION IF EXISTS rag_reject_version_delete();
DROP FUNCTION IF EXISTS rag_guard_version_update();
DROP FUNCTION IF EXISTS rag_guard_version_insert();
DROP FUNCTION IF EXISTS rag_reject_mutation();

DROP TABLE IF EXISTS rag_knowledge_chunks;
DROP TABLE IF EXISTS rag_index_generations;
DROP TABLE IF EXISTS rag_knowledge_idempotency;
DROP TABLE IF EXISTS rag_knowledge_lifecycle_events;
DROP TABLE IF EXISTS rag_knowledge_evidence_refs;
DROP TABLE IF EXISTS rag_knowledge_versions;
DROP TABLE IF EXISTS rag_knowledge_documents;
