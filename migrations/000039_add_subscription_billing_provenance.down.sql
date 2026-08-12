DELETE FROM provider_wallet_events WHERE provider_id = 'mimo' AND cost_provenance = 'subscription_resource_consumed';
DELETE FROM provider_wallets WHERE provider_id = 'mimo' AND balance_usd_nanos = 0 AND reserved_usd_nanos = 0;

ALTER TABLE provider_wallet_events
    DROP CONSTRAINT provider_wallet_events_cost_provenance_check,
    DROP CONSTRAINT provider_wallet_events_financial_outcome_check;

ALTER TABLE provider_wallet_events
    ADD CONSTRAINT provider_wallet_events_cost_provenance_check
    CHECK (cost_provenance IS NULL OR cost_provenance IN ('actual_provider_reported', 'estimated_locally', 'reconciled_provider', 'unknown')),
    ADD CONSTRAINT provider_wallet_events_financial_outcome_check
    CHECK (financial_outcome IS NULL OR financial_outcome IN ('released_not_sent', 'actual', 'estimated_pending_reconciliation', 'reconciled'));
