ALTER TABLE execution_context_views
    DROP CONSTRAINT execution_context_views_selection_kind_valid;

ALTER TABLE execution_context_views
    DROP COLUMN selection_kind,
    DROP COLUMN selector_algorithm_version;

ALTER TABLE context_snapshots
    DROP CONSTRAINT context_snapshots_task_class_syntax,
    DROP CONSTRAINT context_snapshots_execution_purpose_syntax,
    DROP CONSTRAINT context_snapshots_actor_unit_id_length;

ALTER TABLE context_snapshots
    DROP COLUMN task_class,
    DROP COLUMN execution_purpose,
    DROP COLUMN actor_unit_id;

ALTER TABLE tasks
    DROP CONSTRAINT tasks_task_class_syntax;

ALTER TABLE tasks
    DROP COLUMN task_class;
