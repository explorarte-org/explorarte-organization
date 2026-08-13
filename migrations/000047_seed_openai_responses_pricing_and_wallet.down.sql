DELETE FROM provider_wallets WHERE provider_id = 'openai_responses';
DELETE FROM model_pricing WHERE provider_id = 'openai_responses' AND provider_model_id = 'gpt-5.6-luna';
