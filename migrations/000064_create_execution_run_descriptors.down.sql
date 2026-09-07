DROP TRIGGER IF EXISTS execution_run_descriptors_immutable ON execution_run_descriptors;
DROP TRIGGER IF EXISTS execution_run_descriptors_validate_tools ON execution_run_descriptors;
DROP FUNCTION IF EXISTS execution_run_descriptors_reject_mutation();
DROP FUNCTION IF EXISTS execution_run_descriptors_validate_tools();
DROP TABLE IF EXISTS execution_run_descriptors;
