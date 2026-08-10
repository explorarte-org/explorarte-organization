-- Owner decision (PDF ingestion, feat/knowledge-ingestion-object-storage):
-- reproducibility fields, not retrieval fields -- so a vector produced two
-- years from now can be traced back to exactly which source document
-- page, run through which parser version, without inferring anything from
-- media_source_ref's path. Deliberately not folded into a new table (see
-- migration 000035's design note): these live directly on the chunk row
-- they describe.
--
-- source_page_number: 1-indexed page within the ORIGINAL multi-page
-- source PDF (not the standalone single-page PDF media_source_ref points
-- at -- that file only ever has one page).
--
-- media_sha256: the separated single-page PDF's own hash, recorded
-- because poppler's pdfseparate output is NOT byte-for-byte deterministic
-- across runs on identical input (verified empirically, see
-- internal/pdfingest/poppler's idempotency test) -- retry/duplicate
-- idempotency for the ingestion pipeline must key on (original document
-- SHA-256, page number), never on re-deriving this hash.
--
-- media_parser / media_parser_version: e.g. "poppler" / "24.02.0" --
-- pinned and recorded per owner decision point 9.
--
-- text_extraction_status: 'ok' | 'empty' | 'unavailable' -- see
-- internal/pdfingest.TextExtractionStatus. An 'empty' page (no text
-- poppler could find, e.g. scanned/visual) is not an ingestion failure
-- (owner decision point 7); OCR is deliberately not implemented in this
-- branch (owner decision: measure how common 'empty' actually is first).
ALTER TABLE rag_knowledge_chunks
    ADD COLUMN source_page_number INTEGER,
    ADD COLUMN media_sha256 TEXT,
    ADD COLUMN media_parser TEXT,
    ADD COLUMN media_parser_version TEXT,
    ADD COLUMN text_extraction_status TEXT;

ALTER TABLE rag_knowledge_chunks
    ADD CONSTRAINT rag_knowledge_chunks_source_page_number_check
    CHECK (source_page_number IS NULL OR source_page_number > 0),
    ADD CONSTRAINT rag_knowledge_chunks_media_sha256_check
    CHECK (media_sha256 IS NULL OR media_sha256 ~ '^[0-9a-f]{64}$'),
    ADD CONSTRAINT rag_knowledge_chunks_media_parser_check
    CHECK (media_parser IS NULL OR length(media_parser) BETWEEN 1 AND 120),
    ADD CONSTRAINT rag_knowledge_chunks_media_parser_version_check
    CHECK (media_parser_version IS NULL OR length(media_parser_version) BETWEEN 1 AND 120),
    ADD CONSTRAINT rag_knowledge_chunks_text_extraction_status_check
    CHECK (text_extraction_status IS NULL OR text_extraction_status IN ('ok', 'empty', 'unavailable')),
    ADD CONSTRAINT rag_knowledge_chunks_media_provenance_pair_check
    CHECK (
        (media_source_ref IS NULL AND source_page_number IS NULL AND media_sha256 IS NULL AND media_parser IS NULL AND media_parser_version IS NULL AND text_extraction_status IS NULL)
        OR
        (media_source_ref IS NOT NULL AND source_page_number IS NOT NULL AND media_sha256 IS NOT NULL AND media_parser IS NOT NULL AND media_parser_version IS NOT NULL AND text_extraction_status IS NOT NULL)
    );

-- Relax the original content-length lower bound (1..8192) to allow an
-- empty string for a media-backed chunk whose page had no extractable
-- text (text_extraction_status='empty') -- text chunks keep the original
-- 1..8192 bound unchanged.
DO $$
DECLARE c RECORD;
BEGIN
    FOR c IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'rag_knowledge_chunks'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) LIKE '%length(content)%'
    LOOP
        EXECUTE format('ALTER TABLE rag_knowledge_chunks DROP CONSTRAINT %I', c.conname);
    END LOOP;
END;
$$;

ALTER TABLE rag_knowledge_chunks
    ADD CONSTRAINT rag_knowledge_chunks_content_check
    CHECK (
        length(content) <= 8192
        AND (media_source_ref IS NOT NULL OR length(content) >= 1)
    );
