-- Migration 000044: make model-egress revision ownership restorable.
--
-- 000008 enforced "this organization revision belongs to this organization"
-- with three CHECK constraints calling a function that reads
-- organizational_units and organizations:
--
--   model_egress_policy_versions_revision_owner_check
--   model_egress_revision_bindings_revision_owner_check
--   model_egress_evaluations_revision_owner_check
--
-- A CHECK constraint must not depend on other tables. PostgreSQL documents
-- this, and the failure mode it warns about is exactly the one that blocked
-- the 07199f4 release: a CHECK is evaluated row by row during COPY and is
-- not deferrable, so restoring a dump fails whenever the referenced rows
-- have not been loaded yet. Four different restore procedures were tried --
-- pg_restore direct, by section, with PGOPTIONS, and a plain SQL dump with
-- the search_path reset stripped -- and every one lost the same rows. The
-- database was serving correctly and could not be recovered from its own
-- backup, which is only visible if the restore is actually performed.
--
-- The invariant is not weakened. It moves to deferrable constraint triggers
-- that run the same ownership predicate and still fail the transaction
-- before COMMIT. What changes is *when* within the transaction, which is
-- what lets a restore load every table first and validate once at the end.
--
-- A FK would be the usual answer and does not fit here:
-- organization_registry_revisions carries no organization_id, so a revision's
-- ownership is derived from organizational_units.source_revision_id or
-- organizations.current_revision_id. Remodelling the registry to obtain a
-- composite key is a much larger change and is deliberately not attempted.

-- The predicate is unchanged, but pinned to an explicit search_path. Without
-- this it resolves organizational_units and organizations against whatever
-- the caller's search_path happens to be, and dumps deliberately set it to
-- the empty string -- which is why the first restore attempt failed with
-- "relation organizational_units does not exist" rather than with a
-- constraint violation.
CREATE OR REPLACE FUNCTION model_egress_revision_belongs_to_organization(p_organization_id TEXT, p_revision_id BIGINT)
RETURNS BOOLEAN
LANGUAGE SQL
STABLE
SET search_path = pg_catalog, public
AS $$
    SELECT EXISTS (
        SELECT 1
        FROM organizational_units u
        WHERE u.organization_id = p_organization_id
          AND u.source_revision_id = p_revision_id
    ) OR EXISTS (
        SELECT 1
        FROM organizations o
        WHERE o.id = p_organization_id
          AND o.current_revision_id = p_revision_id
    );
$$;

-- One trigger function for all three surfaces. The revision column differs
-- per table (introduced_by_organization_revision_id on policy versions,
-- organization_revision_id on the other two), so it arrives as a trigger
-- argument and is read through to_jsonb(NEW) rather than duplicating the
-- body three times.
CREATE OR REPLACE FUNCTION model_egress_assert_revision_ownership()
RETURNS TRIGGER
LANGUAGE plpgsql
SET search_path = pg_catalog, public
AS $$
DECLARE
    revision_column TEXT := TG_ARGV[0];
    row_json JSONB := to_jsonb(NEW);
    organization TEXT := row_json ->> 'organization_id';
    revision_id BIGINT := (row_json ->> revision_column)::BIGINT;
BEGIN
    IF organization IS NULL OR revision_id IS NULL THEN
        RETURN NULL;
    END IF;
    IF NOT model_egress_revision_belongs_to_organization(organization, revision_id) THEN
        RAISE EXCEPTION
            'organization revision % does not belong to organization % (table %.%)',
            revision_id, organization, TG_TABLE_SCHEMA, TG_TABLE_NAME
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$;

ALTER TABLE model_egress_policy_versions
    DROP CONSTRAINT model_egress_policy_versions_revision_owner_check;
ALTER TABLE model_egress_revision_bindings
    DROP CONSTRAINT model_egress_revision_bindings_revision_owner_check;
ALTER TABLE model_egress_evaluations
    DROP CONSTRAINT model_egress_evaluations_revision_owner_check;

-- DEFERRABLE INITIALLY DEFERRED: the check still runs inside the writing
-- transaction and still aborts it, so a cross-organization revision can
-- never be committed. It simply runs at COMMIT instead of per row, which is
-- what makes a whole-database restore possible.
CREATE CONSTRAINT TRIGGER model_egress_policy_versions_revision_owner
    AFTER INSERT OR UPDATE ON model_egress_policy_versions
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION model_egress_assert_revision_ownership('introduced_by_organization_revision_id');

CREATE CONSTRAINT TRIGGER model_egress_revision_bindings_revision_owner
    AFTER INSERT OR UPDATE ON model_egress_revision_bindings
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION model_egress_assert_revision_ownership('organization_revision_id');

CREATE CONSTRAINT TRIGGER model_egress_evaluations_revision_owner
    AFTER INSERT OR UPDATE ON model_egress_evaluations
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION model_egress_assert_revision_ownership('organization_revision_id');
