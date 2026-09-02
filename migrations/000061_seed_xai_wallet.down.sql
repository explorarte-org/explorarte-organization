-- Same reasoning as 000047's down migration: this migration's up only
-- creates the xai wallet if one was not already present (ON CONFLICT DO
-- NOTHING), so there is no reliable way on rollback to distinguish "the
-- wallet this migration created" from "a wallet that predates it and
-- already carries real balance or ledger references" -- production has
-- exactly the latter. Deleting it unconditionally on down would risk
-- destroying real state this migration never owned, so this down is
-- deliberately non-destructive.
SELECT 1;
