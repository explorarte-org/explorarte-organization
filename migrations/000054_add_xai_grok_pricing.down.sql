-- model_pricing rows are immutable by design (see 000020): a row a past call
-- may already have been priced against must never disappear out from under
-- it, even on rollback. A row-level trigger rejects DELETE and UPDATE, so
-- there is nothing to undo here beyond letting the row stand.
SELECT 1;
