ALTER TABLE rag_knowledge_versions
    DROP CONSTRAINT rag_knowledge_versions_document_fk;

ALTER TABLE rag_knowledge_versions
    ADD CONSTRAINT rag_knowledge_versions_document_fk
    FOREIGN KEY (organization_id, document_id)
    REFERENCES rag_knowledge_documents (organization_id, document_id)
    ON DELETE RESTRICT;

ALTER TABLE rag_knowledge_documents
    DROP CONSTRAINT rag_knowledge_documents_namespace_key;
