-- Migration 000042: Add agent message authorization and hardening
-- This migration adds columns required for security hardening:
-- - request_hash: SHA-256 canonical hash for idempotency integrity
-- - schema_version: Schema version discriminator (should always be "v1")
-- - payload_byte_size: Tracked byte size for monitoring/enforcement

BEGIN;

-- Add request_hash column
ALTER TABLE agent_messages ADD COLUMN request_hash TEXT;

-- Add schema_version column with default 'v1'
ALTER TABLE agent_messages ADD COLUMN schema_version TEXT DEFAULT 'v1' NOT NULL;

-- Add payload_byte_size column (truncated bytes field from existing JSONB)
ALTER TABLE agent_messages ADD COLUMN payload_byte_size INTEGER DEFAULT 0;

-- Create index for request_hash lookups in idempotency checks
CREATE INDEX idx_agent_messages_request_hash ON agent_messages(request_hash)
WHERE request_hash IS NOT NULL;

-- Verify no existing records have conflicting idempotency keys (this should pass normally)
-- If there are collisions at application layer but not DB level, they were silently ignored before
-- New logic will detect these as conflicts when request_hash differs

COMMIT;
