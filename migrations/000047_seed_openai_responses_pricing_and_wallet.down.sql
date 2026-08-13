-- model_pricing rows are immutable by design (see 000020): a row a past
-- call may already have been priced against must never disappear out from
-- under it, even on rollback. Same pattern as 000026/000027's down
-- migrations -- the seeded openai_responses/gpt-5.6-luna price tiers stand.
--
-- provider_wallets has no such trigger (balances legitimately change), so
-- the wallet row this migration seeded is safe to remove on rollback.
DELETE FROM provider_wallets WHERE provider_id = 'openai_responses';
