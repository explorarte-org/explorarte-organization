-- R30.1-2: a resumable, idempotent backfill fills in embeddings for
-- organizational memory entries approved before the active embedding
-- profile existed (or before a re-embedding under a new identity) — a
-- distinct operation from memory_propose (which only ever fires once, at
-- the moment Review approves an entry) so the cost ledger can tell the two
-- apart instead of a backfill run silently masquerading as approval-time
-- embedding traffic.
DO $$
DECLARE c RECORD;
BEGIN
    FOR c IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'embedding_invocations'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) LIKE '%operation%'
    LOOP
        EXECUTE format('ALTER TABLE embedding_invocations DROP CONSTRAINT %I', c.conname);
    END LOOP;
END;
$$;

ALTER TABLE embedding_invocations
    ADD CONSTRAINT embedding_invocations_operation_check
    CHECK (operation IN ('rag_query', 'rag_reindex', 'memory_propose', 'memory_search', 'memory_backfill'));
