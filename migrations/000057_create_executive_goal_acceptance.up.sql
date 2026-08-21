-- Phase ownership for owner acceptance criteria.
--
-- A criterion is not simply a sentence the campaign must satisfy: it is a
-- sentence that becomes checkable at a particular moment. "The design records
-- what will change" can be judged before anything is built. "The host gates
-- passed" cannot -- there are no gates yet. Handing both to the design
-- reviewer asked it to verify the future, and it correctly refused, forever.
--
-- The phase is declared by the owner and stored here rather than inferred from
-- the text. Deciding by keyword would put the same contradiction back, hidden
-- one layer down where nothing would compare it to anything.
CREATE TABLE executive_goal_acceptance (
    root_task_id BIGINT      NOT NULL REFERENCES tasks (id),
    ordinal      INT         NOT NULL,
    phase        TEXT        NOT NULL
        CHECK (phase IN ('design', 'implementation', 'promotion')),
    criterion    TEXT        NOT NULL CHECK (length(btrim(criterion)) > 0),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (root_task_id, ordinal)
);

-- The freeze reads one phase at a time and does it on every round.
CREATE INDEX executive_goal_acceptance_phase
    ON executive_goal_acceptance (root_task_id, phase);
