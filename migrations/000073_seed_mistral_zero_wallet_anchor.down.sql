-- Non-destructive down, same reasoning as migration 000047's down: once
-- this migration has run, there is no reliable way to distinguish "the
-- zero-balance row this migration created" from "a wallet that already
-- existed before this migration ran" or "a wallet this migration created
-- that has since been legitimately funded and used" -- deleting
-- unconditionally on rollback risks destroying real ledger state this
-- migration never owned. provider_wallets carries no version/effective_at
-- column to seed a safer conditional delete against either.
SELECT 1;
