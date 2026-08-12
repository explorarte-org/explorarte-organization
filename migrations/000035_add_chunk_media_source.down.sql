ALTER TABLE rag_knowledge_chunks
    DROP CONSTRAINT rag_knowledge_chunks_media_pair_check,
    DROP CONSTRAINT rag_knowledge_chunks_media_mime_check,
    DROP CONSTRAINT rag_knowledge_chunks_media_ref_check;

ALTER TABLE rag_knowledge_chunks
    DROP COLUMN media_source_ref,
    DROP COLUMN media_mime_type;
