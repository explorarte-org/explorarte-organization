DROP INDEX IF EXISTS provider_wallet_events_embedding_invocation_idx;
DROP INDEX IF EXISTS provider_wallet_events_invocation_idx;
CREATE INDEX provider_wallet_events_invocation_idx ON provider_wallet_events (provider_id, invocation_id);

DROP INDEX IF EXISTS provider_wallet_events_one_terminal_embedding_idx;
DROP INDEX IF EXISTS provider_wallet_events_one_terminal_chat_idx;
CREATE UNIQUE INDEX provider_wallet_events_one_terminal_idx
    ON provider_wallet_events (provider_id, invocation_id)
    WHERE kind IN ('committed', 'released');

DROP INDEX IF EXISTS provider_wallet_events_unique_kind_embedding_idx;
DROP INDEX IF EXISTS provider_wallet_events_unique_kind_chat_idx;

-- Rows with embedding_invocation_id set (and therefore invocation_id NULL)
-- cannot survive this rollback — the prior schema requires invocation_id
-- NOT NULL. This is only safe to run before any embedding call has ever
-- happened for real; it is not a general-purpose "undo with data intact"
-- path.
DELETE FROM provider_wallet_events WHERE embedding_invocation_id IS NOT NULL;

ALTER TABLE provider_wallet_events DROP CONSTRAINT provider_wallet_events_exactly_one_invocation;
ALTER TABLE provider_wallet_events DROP COLUMN embedding_invocation_id;
ALTER TABLE provider_wallet_events ALTER COLUMN invocation_id SET NOT NULL;
ALTER TABLE provider_wallet_events ADD CONSTRAINT provider_wallet_events_unique_kind UNIQUE (provider_id, invocation_id, kind);

DROP TABLE IF EXISTS embedding_invocations;
