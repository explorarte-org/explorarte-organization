-- Migration 000061: seed a provider_wallets row for xai.
--
-- G2-001 (ORGANIZATION-GRAND-AUDIT-001): migration 000054 gave xai/grok-4.6
-- real pricing, but no migration ever followed it with a provider_wallets
-- row -- the exact "pricing added, wallet funding forgotten" gap this
-- finding is about, reproduced here in the migration history itself.
-- costgate.Reserve (internal/modelruntime/costgate/gate.go) has always
-- failed closed on the missing row (by design -- money must fail closed),
-- so this was never a silent overspend risk; it meant xai's every
-- model.invoke had no path to ever reserve cost and dispatch, surfacing as
-- an indistinguishable budget_exceeded rather than the missing-wallet
-- classification RegistryService.Sync / DispatchService now give it.
--
-- Production already carries a real, manually-funded xai wallet (2026-09-02,
-- owner action via `orgctl budget set-balance --provider xai --usd 5.00`,
-- predating this migration) -- ON CONFLICT DO NOTHING leaves that real
-- balance untouched. This migration exists so every OTHER database (test
-- harness, a fresh deploy) gets the row a proper onboarding step would
-- have created alongside 000054, instead of relying on that same manual
-- action being remembered and repeated by hand forever.
--
-- The seeded balance is a placeholder for a fresh/test database, not a
-- production funding decision (that remains the owner's, exactly as it
-- already was for openai_responses in 000047) -- deliberately small so a
-- fresh database's xai wallet needs a real top-up before carrying
-- meaningful traffic, the same posture 000047 already established.
INSERT INTO provider_wallets (provider_id, balance_usd_nanos, reserved_usd_nanos, updated_at) VALUES
    ('xai', 1000000000, 0, NOW())
ON CONFLICT (provider_id) DO NOTHING;
