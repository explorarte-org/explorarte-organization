-- M1.3: durable semantic selector identity (TaskClass, ExecutionPurpose,
-- ActorUnitID) needed to replace the ActorRoleID-only TaskClass proxy
-- (contextcompiler.TaskClassOf) with deterministic, host-validated
-- selection facts. All changes here are additive; no existing column is
-- altered or dropped, no existing row's meaning changes.

-- tasks.task_class: WHAT KIND OF WORK a durable task represents.
-- NOT NULL with a safe default so every row -- historical and future --
-- always has an explicit value; "legacy.unspecified" is a stored FACT
-- about a pre-M1.3 row, never a value a runtime classifier is allowed to
-- assign going forward (see internal/tasks/taskclass.go).
ALTER TABLE tasks
    ADD COLUMN task_class TEXT NOT NULL DEFAULT 'legacy.unspecified';

-- Length bound matches internal/tasks.maxTaskClassBytes (100) exactly --
-- the Go syntax validator and this CHECK must never silently diverge.
ALTER TABLE tasks
    ADD CONSTRAINT tasks_task_class_syntax
        CHECK (length(task_class) <= 100 AND task_class ~ '^[a-z0-9]+(_[a-z0-9]+)*(\.[a-z0-9]+(_[a-z0-9]+)*)+$');

-- One-time historical compatibility backfill (M1.3 section 3): the exact
-- two known research roles already had their tasks measurably behaving
-- as research.corpus_curate under the old ActorRoleID proxy. This UPDATE
-- is a one-time data migration, not a runtime classifier -- no Go code
-- path derives TaskClass from AssignedRoleID after this migration.
UPDATE tasks
SET task_class = 'research.corpus_curate'
WHERE task_class = 'legacy.unspecified'
  AND assigned_role_id IN (
      'investigacion/research_worker_hourly',
      'investigacion/research_worker_hourly_mimo_canary'
  );

-- The DEFAULT above exists ONLY to give every row NOT NULL to backfill
-- against during this migration; independent review correctly flagged
-- that leaving it in place afterward would mean a FUTURE insert that
-- somehow omits task_class (bypassing tasks.Service.CreateTask's own
-- defaulting -- it always supplies an explicit value, but this is
-- defense in depth) gets silently mislabeled as historical. Every
-- historical row above already has its permanent value now; the
-- standing default going forward is the same safe generic class new
-- rows already default to at the Go layer, never the historical marker.
ALTER TABLE tasks
    ALTER COLUMN task_class SET DEFAULT 'general.work';

-- context_snapshots selector facts: durable enough to reproduce selection
-- after restart without contextcompiler ever querying the Task Engine.
-- Nullable and NOT backfilled for historical rows -- NULL is the accurate
-- fact ("this snapshot predates semantic selector facts"), never
-- reinterpreted as a value. A light syntax CHECK applies only when
-- non-NULL; contextengine does not enforce presence (that is Executive's
-- and the Tasks Engine's responsibility, not Context Assembly's).
ALTER TABLE context_snapshots
    ADD COLUMN task_class TEXT,
    ADD COLUMN execution_purpose TEXT,
    ADD COLUMN actor_unit_id TEXT;

ALTER TABLE context_snapshots
    ADD CONSTRAINT context_snapshots_task_class_syntax
        CHECK (task_class IS NULL OR task_class ~ '^[a-z0-9]+(_[a-z0-9]+)*(\.[a-z0-9]+(_[a-z0-9]+)*)+$'),
    ADD CONSTRAINT context_snapshots_execution_purpose_syntax
        CHECK (execution_purpose IS NULL OR execution_purpose ~ '^[a-z][a-z-]*[a-z]$'),
    ADD CONSTRAINT context_snapshots_actor_unit_id_length
        CHECK (actor_unit_id IS NULL OR length(actor_unit_id) BETWEEN 1 AND 120);

-- execution_context_views selection provenance (M1.3 section 14): durable
-- enough to answer "why did this view use this profile" after restart,
-- without duplicating the selector facts themselves (those already live
-- durably on context_snapshots -- see the columns above).
ALTER TABLE execution_context_views
    ADD COLUMN selection_kind TEXT,
    ADD COLUMN selector_algorithm_version TEXT;

-- Historical (M1.1/M1.2-era) rows never went through the new selector
-- registry, but their ACTUAL behavior is exactly reconstructible from the
-- durable fell_back_to_canonical column they already have: a row that did
-- not fall back was resolved by matching TaskClassOf(ActorRoleID) against
-- the single-entry Registry() -- the historical equivalent of a
-- TASK-CLASS-tier match -- and a row that fell back used the canonical
-- render. This is a faithful backfill of what already happened, not a
-- fabricated value, and it is tagged with its own distinct algorithm
-- version so it is never confused with a genuine M1.3 selection.
UPDATE execution_context_views
SET selection_kind = CASE WHEN fell_back_to_canonical THEN 'canonical' ELSE 'task_class' END,
    selector_algorithm_version = 'legacy_task_class_of/v0'
WHERE selection_kind IS NULL;

ALTER TABLE execution_context_views
    ALTER COLUMN selection_kind SET NOT NULL,
    ALTER COLUMN selector_algorithm_version SET NOT NULL;

ALTER TABLE execution_context_views
    ADD CONSTRAINT execution_context_views_selection_kind_valid
        CHECK (selection_kind IN ('exact','task_class','execution_purpose','canonical'));
