-- Audit finding: rag_knowledge_versions_document_fk only ties
-- (organization_id, document_id) back to rag_knowledge_documents. Nothing
-- prevents a version from being inserted with a different
-- namespace_kind/namespace_id than the document it belongs to — the two
-- copies of "namespace" can silently diverge. (organization_id, document_id)
-- is already rag_knowledge_documents' primary key, so widening it to include
-- namespace_kind/namespace_id in a unique constraint is automatically
-- satisfied by every existing row; this is purely additive integrity, not a
-- behavior change for any row that was already consistent.
ALTER TABLE rag_knowledge_documents
    ADD CONSTRAINT rag_knowledge_documents_namespace_key
    UNIQUE (organization_id, document_id, namespace_kind, namespace_id);

ALTER TABLE rag_knowledge_versions
    DROP CONSTRAINT rag_knowledge_versions_document_fk;

ALTER TABLE rag_knowledge_versions
    ADD CONSTRAINT rag_knowledge_versions_document_fk
    FOREIGN KEY (organization_id, document_id, namespace_kind, namespace_id)
    REFERENCES rag_knowledge_documents (organization_id, document_id, namespace_kind, namespace_id)
    ON DELETE RESTRICT;
