-- Migration 000045: make audit_events append-only.
--
-- ORG-AUDIT-006: every other durable ledger in this schema
-- (context_segments, model_egress_*, model_execution_identity_assertions,
-- organizational_memory_*, provider_wallet_events, improvement_promotion_decisions)
-- rejects UPDATE/DELETE with a trigger. audit_events -- the table that
-- records authorization decisions via internal/authorization/postgres's
-- appendEvent, and organization.registry_synced events for every registry
-- sync -- had no such trigger: a bug, a compromised app-role connection,
-- or an operator running ad-hoc SQL could rewrite or delete audit history
-- with the schema raising no objection.
CREATE OR REPLACE FUNCTION audit_events_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_events rows are append-only' USING ERRCODE = '23514';
END;
$$;

CREATE TRIGGER audit_events_immutable
    BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_reject_mutation();
