-- R30 phase 7: ephemeral, task-scoped web evidence. This table is
-- deliberately isolated from every other retrieval surface — no FK to
-- rag_knowledge_chunks, organizational_memory_versions, or any embeddings
-- table. Web evidence is never promoted into permanent organizational
-- knowledge by anything that writes to this table; a human/policy that
-- wants to promote a piece of web evidence must go through
-- rag.Manager.Propose/memory.Manager.Propose with a real
-- AdmissionAttestation, exactly like any other candidate.
--
-- expires_at is NOT NULL and constrained relative to captured_at — there
-- is no representable "forever" row. Application code (internal/
-- webevidence) additionally always filters WHERE expires_at > now() on
-- every read, so an unreaped-but-expired row is invisible even before a
-- reaper physically deletes it.
CREATE TABLE web_evidence (
    id TEXT NOT NULL,
    organization_id TEXT NOT NULL REFERENCES organizations (id) ON DELETE RESTRICT,
    task_id BIGINT NOT NULL REFERENCES tasks (id) ON DELETE RESTRICT,
    url TEXT NOT NULL CHECK (length(url) BETWEEN 1 AND 8192),
    content_hash TEXT NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    captured_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    chunks JSONB NOT NULL CHECK (jsonb_typeof(chunks) = 'array' AND jsonb_array_length(chunks) > 0),
    sanitization_findings JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(sanitization_findings) = 'array'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (organization_id, id),
    CHECK (expires_at > captured_at)
);

CREATE INDEX web_evidence_task_idx ON web_evidence (organization_id, task_id, expires_at DESC);
CREATE INDEX web_evidence_expiry_idx ON web_evidence (expires_at);

-- Same immutability discipline as every other evidence table in this
-- system, adapted to ephemerality: content never changes in place
-- (delete-and-reinsert, or simply let it expire, are the only ways to
-- "change" a piece of web evidence). Unlike rag_chunk_embeddings/
-- organizational_memory_embeddings, DELETE here is not just a rollback
-- path — it is the normal, expected outcome of a reaper removing expired
-- rows, so this trigger only blocks UPDATE.
CREATE OR REPLACE FUNCTION reject_web_evidence_update()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'web_evidence rows cannot be updated in place — delete and re-insert';
END;
$$;

CREATE TRIGGER web_evidence_no_update
BEFORE UPDATE ON web_evidence
FOR EACH ROW EXECUTE FUNCTION reject_web_evidence_update();
