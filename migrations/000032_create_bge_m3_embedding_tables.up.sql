-- R30 phase 5: BGE-M3's local, operational embedding index — separate
-- tables from R29's rag_chunk_embeddings/organizational_memory_embeddings
-- (000028, vector(768), Gemini's frozen reference/canary baseline). Never
-- the same table, never the same column: a query must pick exactly one
-- vector family and can only ever compare vectors from that family's own
-- table, by construction (there is no column here that could hold a
-- Gemini vector, and vice versa).
--
-- Dimension is fixed at 1024 (BAAI/bge-m3 dense output) via the pgvector
-- column type itself, same discipline as 000028: a different dimension
-- needs a new table/migration, not a retrofit.
--
-- Metadata is richer than 000028's tables on purpose — R30 requires every
-- BGE-M3 row to identify: organization_id, the source object (chunk_id/
-- entry_key), model_id, model_revision, artifact_sha256, tokenizer_
-- revision, embedding_dimension, normalization, pooling, prompt_template_
-- version, input_hash, created_at. A local, self-hosted model has no
-- provider-assigned version string the way Gemini does — the pinned
-- artifact hash is what actually identifies which weights produced a
-- given vector, so it is part of the row identity, not just informational.
CREATE TABLE rag_chunk_embeddings_bge_m3 (
    organization_id TEXT NOT NULL,
    chunk_id TEXT NOT NULL,
    embedding_model_id TEXT NOT NULL CHECK (embedding_model_id = 'bge-m3-local'),
    model_revision TEXT NOT NULL CHECK (length(trim(model_revision)) BETWEEN 1 AND 120),
    artifact_sha256 TEXT NOT NULL CHECK (artifact_sha256 ~ '^[0-9a-f]{64}$'),
    tokenizer_revision TEXT NOT NULL CHECK (length(trim(tokenizer_revision)) BETWEEN 1 AND 120),
    embedding_dimension INTEGER NOT NULL CHECK (embedding_dimension = 1024),
    normalization TEXT NOT NULL CHECK (normalization IN ('l2', 'none')),
    pooling TEXT NOT NULL CHECK (pooling IN ('cls', 'mean')),
    prompt_template_version TEXT NOT NULL CHECK (length(trim(prompt_template_version)) BETWEEN 1 AND 60),
    input_hash TEXT NOT NULL CHECK (input_hash ~ '^[0-9a-f]{64}$'),
    embedding vector(1024) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (organization_id, chunk_id, model_revision, artifact_sha256),
    CONSTRAINT rag_chunk_embeddings_bge_m3_chunk_fk FOREIGN KEY (organization_id, chunk_id)
        REFERENCES rag_knowledge_chunks (organization_id, chunk_id) ON DELETE RESTRICT
);

-- Same immutability contract as 000028's embedding tables: UPDATE
-- forbidden (re-embedding is a new row, i.e. a new generation, never an
-- in-place mutation of a vector), DELETE allowed as the rollback path. A
-- dedicated trigger function (not 000028's, which names its own table in
-- the error message) keeps the error message accurate.
CREATE OR REPLACE FUNCTION reject_bge_m3_embedding_update()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% rows cannot be updated in place — delete and re-insert', TG_TABLE_NAME;
END;
$$;

CREATE TRIGGER rag_chunk_embeddings_bge_m3_no_update
BEFORE UPDATE ON rag_chunk_embeddings_bge_m3
FOR EACH ROW EXECUTE FUNCTION reject_bge_m3_embedding_update();

-- Exact/brute-force search only, same reasoning as 000028: no ANN index
-- until real data volume and an EXPLAIN ANALYZE justify one.

CREATE TABLE organizational_memory_embeddings_bge_m3 (
    organization_id TEXT NOT NULL,
    entry_key TEXT NOT NULL,
    embedding_model_id TEXT NOT NULL CHECK (embedding_model_id = 'bge-m3-local'),
    model_revision TEXT NOT NULL CHECK (length(trim(model_revision)) BETWEEN 1 AND 120),
    artifact_sha256 TEXT NOT NULL CHECK (artifact_sha256 ~ '^[0-9a-f]{64}$'),
    tokenizer_revision TEXT NOT NULL CHECK (length(trim(tokenizer_revision)) BETWEEN 1 AND 120),
    embedding_dimension INTEGER NOT NULL CHECK (embedding_dimension = 1024),
    normalization TEXT NOT NULL CHECK (normalization IN ('l2', 'none')),
    pooling TEXT NOT NULL CHECK (pooling IN ('cls', 'mean')),
    prompt_template_version TEXT NOT NULL CHECK (length(trim(prompt_template_version)) BETWEEN 1 AND 60),
    input_hash TEXT NOT NULL CHECK (input_hash ~ '^[0-9a-f]{64}$'),
    embedding vector(1024) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (organization_id, entry_key, model_revision, artifact_sha256),
    CONSTRAINT organizational_memory_embeddings_bge_m3_entry_fk FOREIGN KEY (organization_id, entry_key)
        REFERENCES organizational_memory_versions (organization_id, entry_key) ON DELETE RESTRICT
);

CREATE TRIGGER organizational_memory_embeddings_bge_m3_no_update
BEFORE UPDATE ON organizational_memory_embeddings_bge_m3
FOR EACH ROW EXECUTE FUNCTION reject_bge_m3_embedding_update();
