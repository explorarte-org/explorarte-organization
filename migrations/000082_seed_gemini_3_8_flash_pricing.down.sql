-- model_pricing rows are immutable by design (see 000020): a row a past call may
-- already have been priced against must never disappear out from under it, even on
-- rollback. Same pattern as 000047's down migration -- the seeded
-- gemini/gemini-3.8-flash price rows stand.
SELECT 1;
