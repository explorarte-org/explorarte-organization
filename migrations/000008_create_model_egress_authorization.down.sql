DROP TRIGGER IF EXISTS model_egress_evaluations_no_mutation ON model_egress_evaluations;
DROP TRIGGER IF EXISTS model_egress_revision_bindings_no_mutation ON model_egress_revision_bindings;
DROP TRIGGER IF EXISTS model_egress_rules_no_mutation ON model_egress_rules;
DROP TRIGGER IF EXISTS model_egress_rules_insert_window ON model_egress_rules;
DROP TRIGGER IF EXISTS model_egress_policy_versions_no_mutation ON model_egress_policy_versions;
DROP FUNCTION IF EXISTS reject_model_egress_immutable_mutation();
DROP FUNCTION IF EXISTS enforce_model_egress_rule_insert_window();
DROP FUNCTION IF EXISTS enforce_model_egress_policy_version_immutability();

ALTER TABLE model_egress_evaluations
    DROP CONSTRAINT model_egress_evaluations_revision_owner_check;
ALTER TABLE model_egress_revision_bindings
    DROP CONSTRAINT model_egress_revision_bindings_revision_owner_check;
ALTER TABLE model_egress_policy_versions
    DROP CONSTRAINT model_egress_policy_versions_revision_owner_check;

DROP FUNCTION model_egress_revision_belongs_to_organization(TEXT, BIGINT);

ALTER TABLE model_invocations
    DROP CONSTRAINT model_invocations_egress_revision_binding_fk,
    DROP CONSTRAINT model_invocations_egress_policy_fk,
    DROP CONSTRAINT model_invocations_egress_hash_check,
    DROP CONSTRAINT model_invocations_egress_pair_check,
    DROP COLUMN model_egress_policy_hash,
    DROP COLUMN model_egress_policy_version_id;

DROP TABLE model_egress_evaluations;
DROP FUNCTION model_egress_normalized_reason_codes(JSONB);
DROP FUNCTION model_egress_normalized_classifications(JSONB);
DROP TABLE model_egress_revision_bindings;
DROP TABLE model_egress_rules;
DROP TABLE model_egress_policy_versions;
