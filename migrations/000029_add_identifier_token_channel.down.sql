DROP INDEX IF EXISTS organizational_memory_versions_identifier_tokens_idx;
ALTER TABLE organizational_memory_versions DROP COLUMN IF EXISTS identifier_tokens;

DROP INDEX IF EXISTS rag_knowledge_chunks_identifier_tokens_idx;
ALTER TABLE rag_knowledge_chunks DROP COLUMN IF EXISTS identifier_tokens;

DROP FUNCTION IF EXISTS extract_digit_runs(text);
