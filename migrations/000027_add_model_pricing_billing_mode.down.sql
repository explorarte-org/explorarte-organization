-- model_pricing rows are immutable by design (see 000020) — the seeded
-- gemini-embedding-2 rows are not deleted on rollback for the same reason
-- 000026's down migration leaves its seed data standing: a row a past call
-- may already have been priced against must never disappear out from under
-- it, even on rollback.
ALTER TABLE model_pricing DROP CONSTRAINT model_pricing_unique_tier;
ALTER TABLE model_pricing ADD CONSTRAINT model_pricing_unique_tier UNIQUE (provider_id, provider_model_id, context_tier_name, effective_at);
ALTER TABLE model_pricing DROP COLUMN billing_mode;
