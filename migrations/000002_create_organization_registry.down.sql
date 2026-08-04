DROP TABLE IF EXISTS organization_reporting_lines;

ALTER TABLE IF EXISTS organizational_units
    DROP CONSTRAINT IF EXISTS organizational_units_leader_role_fk;

ALTER TABLE IF EXISTS organizations
    DROP CONSTRAINT IF EXISTS organizations_ceo_role_fk;

ALTER TABLE IF EXISTS organizations
    DROP CONSTRAINT IF EXISTS organizations_owner_role_fk;

DROP TABLE IF EXISTS organization_roles;
DROP TABLE IF EXISTS organizational_units;
DROP TABLE IF EXISTS organizations;
DROP TABLE IF EXISTS organization_registry_revision_documents;
DROP TABLE IF EXISTS organization_registry_revisions;
