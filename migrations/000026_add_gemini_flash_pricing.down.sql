-- model_pricing rows are immutable by design (see 000020): a row a past
-- call may already have been priced against must never disappear out from
-- under it, even on rollback. There is nothing to undo here beyond letting
-- the row stand; DROP TABLE in 000020's own down migration is what
-- actually removes it, bypassing the row-level immutability trigger.
SELECT 1;
