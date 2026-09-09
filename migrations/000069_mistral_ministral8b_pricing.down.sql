DELETE FROM model_pricing WHERE provider_id='mistral'
  AND provider_model_id='ministral-8b-2512'
  AND context_tier_name='standard'
  AND billing_mode='online'
  AND effective_at='2026-09-09T00:00:00Z'::timestamptz;
