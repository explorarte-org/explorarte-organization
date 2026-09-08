-- The up migration is idempotent and may coexist with a production wallet
-- anchor. Never delete provider accounting state during rollback.
SELECT 1;
