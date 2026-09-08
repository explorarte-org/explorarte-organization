-- Migration 000068: seed the zero-value wallet anchor for Cloudflare Workers AI.
--
-- Workers AI is quota-billed in this deployment: the first bucket is the
-- provider's free daily neuron allowance, not a PAYG USD balance. The
-- subscription-aware cost gate skips price resolution and wallet reservation,
-- but durable resource-consumption events still reference provider_wallets.
INSERT INTO provider_wallets (provider_id, balance_usd_nanos, reserved_usd_nanos, updated_at)
VALUES ('cloudflare_workers_ai', 0, 0, NOW())
ON CONFLICT (provider_id) DO NOTHING;
