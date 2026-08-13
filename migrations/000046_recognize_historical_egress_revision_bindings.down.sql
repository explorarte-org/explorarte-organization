-- Reverts to the 000044 predicate (units.source_revision_id OR
-- organizations.current_revision_id only).
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
