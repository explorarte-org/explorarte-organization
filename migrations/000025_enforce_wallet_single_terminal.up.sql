-- A reservation may become committed OR released, never both. The existing
-- UNIQUE(provider_id, invocation_id, kind) only prevented the SAME kind
-- from being inserted twice — it did nothing to stop 'committed' and
-- 'released' from both being inserted for the same reservation, which
-- would double-decrement reserved_usd_nanos and silently free capacity
-- that belonged to a real, still-outstanding reservation. Enforced here at
-- the database level rather than trusted to application code.
CREATE UNIQUE INDEX provider_wallet_events_one_terminal_idx
    ON provider_wallet_events (provider_id, invocation_id)
    WHERE kind IN ('committed', 'released');
