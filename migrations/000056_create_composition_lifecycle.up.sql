-- Durable composition lifecycle: episodes and the bindings they committed.
--
-- This state has to outlive the processes it describes. That is the entire
-- premise: a binding held in memory disappears with the process that held it,
-- which is convenient and wrong -- it means a crash silently releases a grip
-- that was never adjudicated. Here the grip survives, and liveness (the lease)
-- rather than process death is what decides whether it still holds.
CREATE TABLE composition_episodes (
    id               TEXT PRIMARY KEY,
    component_id     TEXT        NOT NULL,
    state            TEXT        NOT NULL
        CHECK (state IN ('inactive', 'reloading', 'active', 'unloading', 'failed')),
    lease_expires_at TIMESTAMPTZ NOT NULL,
    adjudicated      BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One activation of a component may be Active at a time, because two would be
-- two answers to a fact that has one owner. The domain enforces this already;
-- enforcing it here too means no path -- a repair script, a manual UPDATE, a
-- future writer nobody has written yet -- can produce a state the domain
-- refuses to produce.
CREATE UNIQUE INDEX composition_one_active_episode_per_component
    ON composition_episodes (component_id)
    WHERE state = 'active';

CREATE INDEX composition_episodes_lease
    ON composition_episodes (lease_expires_at)
    WHERE state IN ('active', 'reloading', 'unloading');

-- A committed binding names episodes, never components, and names the key,
-- never merely the type. That is what lets relied() answer with a name
-- instead of a boolean.
CREATE TABLE composition_bindings (
    consumer_episode TEXT        NOT NULL REFERENCES composition_episodes (id),
    key              TEXT        NOT NULL,
    provider_episode TEXT        NOT NULL REFERENCES composition_episodes (id),
    committed_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer_episode, key),
    CHECK (consumer_episode <> provider_episode)
);

CREATE INDEX composition_bindings_provider
    ON composition_bindings (provider_episode);

-- No ON DELETE CASCADE anywhere above, deliberately. A binding is the record
-- of what was committed; deleting the episode must not quietly delete the
-- evidence that it was relied upon.
