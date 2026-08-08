DROP TRIGGER IF EXISTS provider_wallet_events_no_mutation ON provider_wallet_events;
DROP FUNCTION IF EXISTS reject_provider_wallet_event_mutation();
DROP TABLE IF EXISTS provider_wallet_events;
DROP TABLE IF EXISTS provider_wallets;
