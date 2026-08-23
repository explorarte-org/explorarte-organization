-- PR #107 made repository_evidence a productive source kind, but the
-- persistence contract was never extended: context_segments still admitted
-- only the ten kinds that existed when the Context Engine was created, so a
-- design that observed its own repository failed at the first durable write
-- with 23514. The sensor was correct; the schema had not been told about it.
--
-- The kind is admitted here together with the four properties
-- repositoryevidence.Render already fixes for every fragment it produces.
-- They are stated as schema invariants, not as a comment, because a domain
-- rule that only exists in Go is one refactor away from being persistable in
-- a shape the boundary forbids: repository evidence is untrusted
-- organizational DATA that never carries authority, and the database is the
-- last place that can still say so.

ALTER TABLE context_segments DROP CONSTRAINT context_segments_source_kind_check;

ALTER TABLE context_segments ADD CONSTRAINT context_segments_source_kind_check
    CHECK (source_kind IN (
        'canonical_document','owner_constraint','organization_agent','department_agent','role_profile',
        'approved_memory','approved_skill','project_context','task_context','rag_evidence',
        'repository_evidence'
    ));

-- Code the organization reads about itself must never become an instruction
-- to itself, and must never be mistaken for sanitized output.
ALTER TABLE context_segments ADD CONSTRAINT context_segments_repository_evidence_grants_no_capability
    CHECK (source_kind <> 'repository_evidence' OR NOT may_grant_capabilities);

ALTER TABLE context_segments ADD CONSTRAINT context_segments_repository_evidence_is_data
    CHECK (source_kind <> 'repository_evidence' OR instruction_class = 'data');

ALTER TABLE context_segments ADD CONSTRAINT context_segments_repository_evidence_is_untrusted
    CHECK (source_kind <> 'repository_evidence' OR trust_class = 'untrusted');

ALTER TABLE context_segments ADD CONSTRAINT context_segments_repository_evidence_is_organizational
    CHECK (source_kind <> 'repository_evidence' OR data_class = 'organizational');
