DROP TRIGGER IF EXISTS execution_run_events_immutable ON execution_run_events;
DROP FUNCTION IF EXISTS execution_run_events_reject_mutation();
DROP TABLE IF EXISTS execution_run_events;
