-- Migration 000042 rollback: Remove agent message authorization columns
-- This is a destructive rollback - only use if you understand data loss implications

BEGIN;

DROP INDEX IF EXISTS idx_agent_messages_request_hash;

ALTER TABLE agent_messages DROP COLUMN IF EXISTS request_hash;
ALTER TABLE agent_messages DROP COLUMN IF EXISTS schema_version;
ALTER TABLE agent_messages DROP COLUMN IF EXISTS payload_byte_size;

COMMIT;
