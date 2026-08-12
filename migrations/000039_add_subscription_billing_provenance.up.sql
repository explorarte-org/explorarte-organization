-- MiMo-V2.5 (Xiaomi) integration: MiMo is billed via a fixed Token Plan
-- (subscription/quota), NOT pay-as-you-go like DeepSeek/Gemini/OpenAI. The
-- existing cost_provenance/financial_outcome CHECK constraints (migration
-- 000037) only allow the PAYG-shaped vocabulary
-- (actual_provider_reported/estimated_locally/reconciled_provider/unknown
-- and released_not_sent/actual/estimated_pending_reconciliation/reconciled)
-- -- none of which honestly describes "this call consumed real Token Plan
-- resources but has no real USD amount, by design, not because it was
-- free". Per the owner's explicit instruction: never let
-- amount_usd_nanos=0 appear without an explicit, non-null provenance
-- explaining why, and never claim financial_outcome=actual for a call
-- whose real USD cost is genuinely unknown/inapplicable.
--
-- subscription_resource_consumed / resource_consumed are additive, clearly
-- distinct from every existing PAYG value -- a reader can immediately tell
-- these rows apart from a real DeepSeek/Gemini/OpenAI charge or estimate.
-- See internal/costledger/postgres/store.go's RecordSubscriptionConsumption
-- (the only writer of these two values) and
-- internal/modelruntime/costgate/gate.go's Reserve, which skips PriceTier
-- resolution and wallet reservation entirely for a provider named in
-- costgate.New's subscriptionProviders list -- no PriceTier exists for
-- mimo today and none is fabricated by this change.
ALTER TABLE provider_wallet_events
    DROP CONSTRAINT provider_wallet_events_cost_provenance_check,
    DROP CONSTRAINT provider_wallet_events_financial_outcome_check;

ALTER TABLE provider_wallet_events
    ADD CONSTRAINT provider_wallet_events_cost_provenance_check
    CHECK (cost_provenance IS NULL OR cost_provenance IN ('actual_provider_reported', 'estimated_locally', 'reconciled_provider', 'unknown', 'subscription_resource_consumed')),
    ADD CONSTRAINT provider_wallet_events_financial_outcome_check
    CHECK (financial_outcome IS NULL OR financial_outcome IN ('released_not_sent', 'actual', 'estimated_pending_reconciliation', 'reconciled', 'resource_consumed'));

-- provider_wallet_events.provider_id has a NOT NULL FK to
-- provider_wallets(provider_id) (migration 000021) -- RecordSubscriptionConsumption
-- never touches provider_wallets.balance_usd_nanos/reserved_usd_nanos (there
-- is no real balance for a Token Plan), but the FK still requires a row to
-- exist. This seeds one for 'mimo' with balance_usd_nanos=0: a pure FK
-- anchor, not a real balance -- deliberately never credited, debited, or
-- reserved against by any code path (Gate.Reserve skips ledger.Reserve
-- entirely for this provider; see gate.go).
INSERT INTO provider_wallets (provider_id, balance_usd_nanos, reserved_usd_nanos, updated_at)
VALUES ('mimo', 0, 0, NOW())
ON CONFLICT (provider_id) DO NOTHING;
