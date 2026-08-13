-- Migration 000046: recognize historical revisions in the egress ownership
-- predicate, not just the currently-materialized one.
--
-- ORG-AUDIT-004: model_egress_revision_belongs_to_organization (000044)
-- accepts a revision if some unit's source_revision_id equals it, or if
-- it is the organization's current_revision_id. registry.Apply (see
-- internal/organization/registry/postgres_repository.go) advances EVERY
-- unit's source_revision_id to the new revision on every sync, and
-- organizations.current_revision_id moves too -- so a revision N that was
-- genuinely materialized for this organization stops satisfying either
-- branch the moment N+1 is applied, even though model_egress_revision_bindings
-- still carries an immutable, permanent row proving N belonged to this org
-- (bindings are never deleted -- see the FK from model_invocations pinning
-- to them). A pre-send egress evaluation for an invocation still in flight
-- under N would fail the constraint trigger after a routine sync, purely
-- because nothing checked the one table that actually remembers N.
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
    ) OR EXISTS (
        SELECT 1
        FROM model_egress_revision_bindings b
        WHERE b.organization_id = p_organization_id
          AND b.organization_revision_id = p_revision_id
    );
$$;
