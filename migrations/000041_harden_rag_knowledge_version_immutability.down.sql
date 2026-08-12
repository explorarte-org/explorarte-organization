-- Revert rag_guard_version_update to its form as declared in migration
-- 000017_create_approved_knowledge_rag.up.sql.
CREATE OR REPLACE FUNCTION rag_guard_version_update() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE event_found BOOLEAN;
BEGIN
    IF NEW.organization_id <> OLD.organization_id OR NEW.version_id <> OLD.version_id OR NEW.document_id <> OLD.document_id OR NEW.version <> OLD.version THEN RAISE EXCEPTION 'rag knowledge version identity is immutable' USING ERRCODE = '23514'; END IF;
    IF NEW.body <> OLD.body OR NEW.title <> OLD.title OR NEW.content_hash <> OLD.content_hash OR NEW.canonical_hash <> OLD.canonical_hash THEN RAISE EXCEPTION 'rag knowledge version content is immutable' USING ERRCODE = '23514'; END IF;
    IF NEW.data_class <> OLD.data_class OR NEW.admission_attested_by <> OLD.admission_attested_by OR NEW.admission_attested_at <> OLD.admission_attested_at THEN RAISE EXCEPTION 'rag knowledge version admission provenance is immutable' USING ERRCODE = '23514'; END IF;
    IF NEW.supersedes_version_id IS DISTINCT FROM OLD.supersedes_version_id OR NEW.created_at <> OLD.created_at THEN RAISE EXCEPTION 'rag knowledge version provenance is immutable' USING ERRCODE = '23514'; END IF;
    IF NEW.revision <> OLD.revision + 1 THEN RAISE EXCEPTION 'rag knowledge version revision must advance exactly by one' USING ERRCODE = '23514'; END IF;
    IF NEW.updated_at < OLD.updated_at THEN RAISE EXCEPTION 'rag knowledge version updated_at cannot move backwards' USING ERRCODE = '23514'; END IF;
    IF NOT ((OLD.lifecycle = 'candidate' AND NEW.lifecycle IN ('approved', 'rejected'))
        OR (OLD.lifecycle = 'approved' AND NEW.lifecycle = 'deprecated')
        OR (OLD.lifecycle = 'deprecated' AND NEW.lifecycle = 'archived')
        OR (OLD.lifecycle = 'rejected' AND NEW.lifecycle = 'archived')) THEN
        RAISE EXCEPTION 'invalid rag knowledge lifecycle transition % -> %', OLD.lifecycle, NEW.lifecycle USING ERRCODE = '23514';
    END IF;
    IF OLD.lifecycle <> 'candidate' AND (NEW.reviewer_role_id IS DISTINCT FROM OLD.reviewer_role_id OR NEW.reviewed_at IS DISTINCT FROM OLD.reviewed_at) THEN RAISE EXCEPTION 'rag knowledge review provenance is immutable after review' USING ERRCODE = '23514'; END IF;
    SELECT EXISTS (SELECT 1 FROM rag_knowledge_lifecycle_events WHERE organization_id = NEW.organization_id AND version_id = NEW.version_id AND revision = NEW.revision AND from_lifecycle = OLD.lifecycle AND to_lifecycle = NEW.lifecycle AND created_at = NEW.updated_at) INTO event_found;
    IF NOT event_found THEN RAISE EXCEPTION 'rag knowledge transition requires matching audit event' USING ERRCODE = '23514'; END IF;
    RETURN NEW;
END;
$$;
