-- model_pricing rows are immutable by design (see 000020): a row a past
-- call may already have been priced against must never disappear out from
-- under it, even on rollback. A row-level trigger rejects DELETE and
-- UPDATE, so the original DELETE this down migration used to attempt
-- would always fail against it -- same pattern as 000047/000054's down
-- migrations for the same reason: the seeded mistral/ministral-8b-2512
-- Standard price tier stands.
SELECT 1;
