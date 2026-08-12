-- Multimodal PDF-page embedding (owner-approved design, feat/knowledge-
-- ingestion-object-storage): a chunk's embedding vector can now come from
-- embedding the ORIGINAL binary media (a single-page PDF, an image, ...)
-- via gemini-embedding-2's multimodal input, rather than from embedding
-- chunk.content. This reuses the existing rag_knowledge_chunks/
-- rag_chunk_embeddings tables and hybrid_query.go unchanged -- Google's
-- own documentation states gemini-embedding-2 maps every modality into one
-- unified vector space, so a media-derived embedding and a text-derived
-- embedding for the same dimension/identity are directly comparable, not a
-- different retrieval channel.
--
-- media_source_ref/media_mime_type are both NULL for the existing
-- text-chunking path (chunk.content is embedded directly, as always) and
-- both set together for a media-backed chunk: media_source_ref is an
-- Object Storage key pointing at the exact bytes to embed (a pre-split
-- single-page PDF, one image, etc — never a multi-page container, so the
-- backfill embedding step never needs its own PDF-splitting logic, only a
-- GetObject + Embed call). chunk.content still holds this page/item's
-- extracted text (FTS, citations, snippets, debugging, provenance keep
-- working exactly as they do today) even though it is not what gets
-- embedded for this chunk.
ALTER TABLE rag_knowledge_chunks
    ADD COLUMN media_source_ref TEXT,
    ADD COLUMN media_mime_type TEXT;

-- Keep this MIME allowlist in exact sync with
-- internal/embeddingruntime/adapter/gemini/online.go's SupportedMediaMimeTypes
-- -- a SQL CHECK cannot reference a Go constant, so this is a manual mirror,
-- the same kind of duplicated-allowlist gap as the model-egress compiled
-- provider lists elsewhere in this codebase.
ALTER TABLE rag_knowledge_chunks
    ADD CONSTRAINT rag_knowledge_chunks_media_ref_check
    CHECK (media_source_ref IS NULL OR length(media_source_ref) BETWEEN 1 AND 1024),
    ADD CONSTRAINT rag_knowledge_chunks_media_mime_check
    CHECK (media_mime_type IS NULL OR media_mime_type IN ('application/pdf', 'image/png', 'image/jpeg', 'audio/mpeg', 'audio/wav', 'video/mp4', 'video/quicktime')),
    ADD CONSTRAINT rag_knowledge_chunks_media_pair_check
    CHECK ((media_source_ref IS NULL AND media_mime_type IS NULL) OR (media_source_ref IS NOT NULL AND media_mime_type IS NOT NULL));
