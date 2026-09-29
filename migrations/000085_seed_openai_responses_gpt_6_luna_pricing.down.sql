-- model_pricing rows are immutable by design (see 000020): a row a past call may
-- already have been priced against must never disappear out from under it, even on
-- rollback. Same pattern as 000047's and 000083's down migrations -- the seeded
-- openai_responses/gpt-6-luna price tiers stand.
SELECT 1;
