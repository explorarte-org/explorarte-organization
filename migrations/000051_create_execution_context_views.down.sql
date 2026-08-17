DROP TRIGGER IF EXISTS execution_context_views_immutable ON execution_context_views;
DROP FUNCTION IF EXISTS reject_execution_context_view_mutation();
DROP TABLE IF EXISTS execution_context_views;
