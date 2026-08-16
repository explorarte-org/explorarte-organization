DROP TRIGGER IF EXISTS execution_run_events_immutable ON execution_run_events;
DROP FUNCTION IF EXISTS execution_run_events_reject_mutation();
DROP TABLE IF EXISTS execution_run_events;
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_id_organization_unique;
