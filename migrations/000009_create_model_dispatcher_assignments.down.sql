ALTER TABLE model_dispatch_attempts
    DROP CONSTRAINT model_dispatch_attempts_invocation_principal_fk,
    DROP COLUMN execution_principal_id;

ALTER TABLE model_invocations
    DROP CONSTRAINT model_invocations_dispatcher_assignment_fk,
    DROP CONSTRAINT model_invocations_execution_principal_fk,
    DROP CONSTRAINT model_invocations_id_principal_unique,
    DROP CONSTRAINT model_invocations_dispatcher_pair_check,
    DROP COLUMN execution_principal_id,
    DROP COLUMN dispatcher_assignment_id;

DROP TRIGGER IF EXISTS model_dispatcher_assignment_uses_no_mutation ON model_dispatcher_assignment_uses;
DROP FUNCTION IF EXISTS reject_model_dispatcher_assignment_use_mutation();

DROP TABLE model_dispatcher_assignment_uses;
DROP TABLE model_dispatcher_assignments;
DROP TABLE model_execution_principals;
