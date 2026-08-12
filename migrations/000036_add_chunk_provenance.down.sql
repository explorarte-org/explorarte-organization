-- Only safe to run before any media-backed chunk with empty content
-- (text_extraction_status='empty') has been written for real -- same
-- caveat as every other narrowing CHECK rollback in this schema.
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
    CHECK (length(content) BETWEEN 1 AND 8192);

ALTER TABLE rag_knowledge_chunks
    DROP CONSTRAINT rag_knowledge_chunks_media_provenance_pair_check,
    DROP CONSTRAINT rag_knowledge_chunks_text_extraction_status_check,
    DROP CONSTRAINT rag_knowledge_chunks_media_parser_version_check,
    DROP CONSTRAINT rag_knowledge_chunks_media_parser_check,
    DROP CONSTRAINT rag_knowledge_chunks_media_sha256_check,
    DROP CONSTRAINT rag_knowledge_chunks_source_page_number_check;

ALTER TABLE rag_knowledge_chunks
    DROP COLUMN source_page_number,
    DROP COLUMN media_sha256,
    DROP COLUMN media_parser,
    DROP COLUMN media_parser_version,
    DROP COLUMN text_extraction_status;
