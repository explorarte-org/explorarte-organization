-- Only safe to run before any embedding_invocations row with
-- operation='memory_backfill' has ever been written for real, same caveat
-- as every other narrowing CHECK rollback in this schema.
DELETE FROM embedding_invocations WHERE operation = 'memory_backfill';

ALTER TABLE embedding_invocations DROP CONSTRAINT embedding_invocations_operation_check;

ALTER TABLE embedding_invocations
    ADD CONSTRAINT embedding_invocations_operation_check
    CHECK (operation IN ('rag_query', 'rag_reindex', 'memory_propose', 'memory_search'));
