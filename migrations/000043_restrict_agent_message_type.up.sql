-- Migration 000043: drop the obsolete 'status' agent message type.
--
-- 000024 created agent_messages with
--   CHECK (message_type IN ('delegation', 'completion', 'status'))
-- Security hardening v1 removed MessageStatus from the Go type entirely --
-- MessageType.Valid() now admits only delegation and completion, and no live
-- code path can produce 'status'. The database constraint outlived the type it
-- was written for, leaving a value writable at the storage layer that the
-- application can no longer produce or interpret. Constraints wider than the
-- code they guard are where forged or replayed rows hide, so the storage
-- layer is narrowed to match.
--
-- Surviving 'status' rows must be rewritten before the constraint tightens,
-- because the new CHECK would otherwise reject the table. They are retired to
-- 'dead' rather than merely relabelled: a 'status' row still in 'pending' would
-- become claimable as a delegation, and its payload cannot satisfy
-- DelegationPayloadV1, so it would be retried to exhaustion. Its original
-- meaning was deleted along with the type and cannot be reconstructed from
-- the row, so the only honest outcome is to stop processing it and say why.
UPDATE agent_messages
SET message_type = 'delegation',
    status       = 'dead',
    last_error   = COALESCE(last_error, '') ||
                   'retired by migration 000043: message_type=status was removed in security hardening v1'
WHERE message_type = 'status';

ALTER TABLE agent_messages DROP CONSTRAINT IF EXISTS agent_messages_message_type_check;
ALTER TABLE agent_messages ADD CONSTRAINT agent_messages_message_type_check
    CHECK (message_type IN ('delegation', 'completion'));
