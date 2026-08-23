-- Deliberately strict: if repository_evidence segments exist, restoring the
-- original list fails rather than deleting durable observations. Losing the
-- record of what a design was shown is worse than a failed rollback.

ALTER TABLE context_segments DROP CONSTRAINT context_segments_repository_evidence_is_organizational;
ALTER TABLE context_segments DROP CONSTRAINT context_segments_repository_evidence_is_untrusted;
ALTER TABLE context_segments DROP CONSTRAINT context_segments_repository_evidence_is_data;
ALTER TABLE context_segments DROP CONSTRAINT context_segments_repository_evidence_grants_no_capability;

ALTER TABLE context_segments DROP CONSTRAINT context_segments_source_kind_check;

ALTER TABLE context_segments ADD CONSTRAINT context_segments_source_kind_check
    CHECK (source_kind IN (
        'canonical_document','owner_constraint','organization_agent','department_agent','role_profile',
        'approved_memory','approved_skill','project_context','task_context','rag_evidence'
    ));
