-- R29: embeddings live in derived tables, never as a mutated column on
-- rag_knowledge_chunks or organizational_memory_versions. Both of those are
-- canonical, fully immutable evidence tables (see 000017/000015) — a
-- derived, rebuildable vector index does not belong mixed into that
-- immutability contract. This also means: re-embedding under a new model
-- version is a new row, not an UPDATE; rollback to a prior model is
-- deleting rows, not a schema change; and multiple embedding model
-- versions can coexist during a migration between them.
--
-- Dimension is fixed at 768 (R29's chosen default) via the pgvector column
-- type itself. A future embedding model at a different dimension needs a
-- new table/migration, not a retrofit of this one — deliberately not
-- over-engineered to support arbitrary dimensions from day one.

CREATE TABLE rag_chunk_embeddings (
    organization_id TEXT NOT NULL,
    chunk_id TEXT NOT NULL,
    embedding_model_id TEXT NOT NULL CHECK (length(trim(embedding_model_id)) BETWEEN 1 AND 120),
    embedding_model_version TEXT NOT NULL CHECK (length(trim(embedding_model_version)) BETWEEN 1 AND 60),
    embedding_dimension INTEGER NOT NULL CHECK (embedding_dimension = 768),
    prompt_template_version TEXT NOT NULL CHECK (length(trim(prompt_template_version)) BETWEEN 1 AND 60),
    input_hash TEXT NOT NULL CHECK (input_hash ~ '^[0-9a-f]{64}$'),
    embedding vector(768) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (organization_id, chunk_id, embedding_model_id, embedding_model_version),
    CONSTRAINT rag_chunk_embeddings_chunk_fk FOREIGN KEY (organization_id, chunk_id)
        REFERENCES rag_knowledge_chunks (organization_id, chunk_id) ON DELETE RESTRICT
);

-- UPDATE is blocked (unlike canonical rag tables, DELETE stays allowed —
-- deleting a row IS the supported rollback path for a bad/stale embedding).
-- The primary key already identifies exactly one (chunk, model, model
-- version) triple; an UPDATE against an existing row would silently swap
-- its vector out from under that identity instead of the caller deleting
-- and re-inserting explicitly, which is the only safe way to correct one.
CREATE OR REPLACE FUNCTION reject_rag_chunk_embedding_update()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'rag_chunk_embeddings rows cannot be updated in place — delete and re-insert';
END;
$$;

CREATE TRIGGER rag_chunk_embeddings_no_update
BEFORE UPDATE ON rag_chunk_embeddings
FOR EACH ROW EXECUTE FUNCTION reject_rag_chunk_embedding_update();

-- Exact/brute-force search only in R29 (ORDER BY embedding <=> $1 LIMIT N,
-- no ANN index): production has 0 chunks today, so HNSW is premature, and
-- HNSW filters *after* traversing the index — worse than exact search once
-- namespace/organization scoping is applied inside the same query. Add an
-- ANN index later only if an EXPLAIN ANALYZE against real data justifies it.

CREATE TABLE organizational_memory_embeddings (
    organization_id TEXT NOT NULL,
    entry_key TEXT NOT NULL,
    embedding_model_id TEXT NOT NULL CHECK (length(trim(embedding_model_id)) BETWEEN 1 AND 120),
    embedding_model_version TEXT NOT NULL CHECK (length(trim(embedding_model_version)) BETWEEN 1 AND 60),
    embedding_dimension INTEGER NOT NULL CHECK (embedding_dimension = 768),
    prompt_template_version TEXT NOT NULL CHECK (length(trim(prompt_template_version)) BETWEEN 1 AND 60),
    input_hash TEXT NOT NULL CHECK (input_hash ~ '^[0-9a-f]{64}$'),
    embedding vector(768) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (organization_id, entry_key, embedding_model_id, embedding_model_version),
    CONSTRAINT organizational_memory_embeddings_entry_fk FOREIGN KEY (organization_id, entry_key)
        REFERENCES organizational_memory_versions (organization_id, entry_key) ON DELETE RESTRICT
);

CREATE TRIGGER organizational_memory_embeddings_no_update
BEFORE UPDATE ON organizational_memory_embeddings
FOR EACH ROW EXECUTE FUNCTION reject_rag_chunk_embedding_update();

-- Batch job tracking for the asynchronous embeddings Batch API (R29 phase
-- 3/4). A single reindex can produce more chunks than fit in one Google
-- batch job, so a generation's embedding work is sharded across possibly
-- several jobs; each job's items are tracked individually so a partial
-- failure, expiry, or cancellation never loses track of which chunks still
-- need embedding.

CREATE TABLE rag_embedding_batch_jobs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    namespace_kind TEXT NOT NULL CHECK (namespace_kind IN ('department', 'own')),
    namespace_id TEXT NOT NULL,
    generation_id TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    provider_model_id TEXT NOT NULL,
    provider_job_name TEXT,
    status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled', 'expired')),
    shard_index INTEGER NOT NULL CHECK (shard_index >= 0),
    item_count INTEGER NOT NULL CHECK (item_count > 0),
    failed_item_count INTEGER NOT NULL DEFAULT 0 CHECK (failed_item_count >= 0),
    submitted_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (organization_id, generation_id, shard_index),
    CONSTRAINT rag_embedding_batch_jobs_generation_fk FOREIGN KEY (organization_id, generation_id)
        REFERENCES rag_index_generations (organization_id, generation_id) ON DELETE RESTRICT,
    CHECK (updated_at >= created_at),
    CHECK (submitted_at IS NULL OR submitted_at >= created_at),
    CHECK (completed_at IS NULL OR (submitted_at IS NOT NULL AND completed_at >= submitted_at)),
    CHECK ((status IN ('succeeded', 'failed', 'cancelled', 'expired')) = (completed_at IS NOT NULL))
);

CREATE INDEX rag_embedding_batch_jobs_generation_idx ON rag_embedding_batch_jobs (organization_id, generation_id, status);
CREATE INDEX rag_embedding_batch_jobs_pending_idx ON rag_embedding_batch_jobs (status) WHERE status IN ('pending', 'running');

CREATE TABLE rag_embedding_batch_job_items (
    job_id BIGINT NOT NULL REFERENCES rag_embedding_batch_jobs (id) ON DELETE RESTRICT,
    item_key TEXT NOT NULL CHECK (length(trim(item_key)) BETWEEN 1 AND 240),
    organization_id TEXT NOT NULL,
    chunk_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'succeeded', 'failed')),
    error_message TEXT,
    PRIMARY KEY (job_id, item_key),
    CONSTRAINT rag_embedding_batch_job_items_chunk_fk FOREIGN KEY (organization_id, chunk_id)
        REFERENCES rag_knowledge_chunks (organization_id, chunk_id) ON DELETE RESTRICT,
    CHECK ((status = 'failed') = (error_message IS NOT NULL))
);

CREATE INDEX rag_embedding_batch_job_items_chunk_idx ON rag_embedding_batch_job_items (organization_id, chunk_id);
