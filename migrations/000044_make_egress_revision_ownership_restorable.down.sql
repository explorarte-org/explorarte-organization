-- Reverse of 000044: restore the three cross-table CHECK constraints.
--
-- This deliberately reinstates the non-restorable state. A down migration is
-- an inverse, not an improvement: whoever rolls back to 000043 must get the
-- schema 000043 actually described, including its defect. The restorability
-- fitness is expected to fail against this state, which is the point.

DROP TRIGGER IF EXISTS model_egress_policy_versions_revision_owner ON model_egress_policy_versions;
DROP TRIGGER IF EXISTS model_egress_revision_bindings_revision_owner ON model_egress_revision_bindings;
DROP TRIGGER IF EXISTS model_egress_evaluations_revision_owner ON model_egress_evaluations;

DROP FUNCTION IF EXISTS model_egress_assert_revision_ownership();

CREATE OR REPLACE FUNCTION model_egress_revision_belongs_to_organization(p_organization_id TEXT, p_revision_id BIGINT)
RETURNS BOOLEAN
LANGUAGE SQL
STABLE
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

ALTER TABLE model_egress_policy_versions
    ADD CONSTRAINT model_egress_policy_versions_revision_owner_check
        CHECK (model_egress_revision_belongs_to_organization(organization_id, introduced_by_organization_revision_id));
ALTER TABLE model_egress_revision_bindings
    ADD CONSTRAINT model_egress_revision_bindings_revision_owner_check
        CHECK (model_egress_revision_belongs_to_organization(organization_id, organization_revision_id));
ALTER TABLE model_egress_evaluations
    ADD CONSTRAINT model_egress_evaluations_revision_owner_check
        CHECK (model_egress_revision_belongs_to_organization(organization_id, organization_revision_id));
