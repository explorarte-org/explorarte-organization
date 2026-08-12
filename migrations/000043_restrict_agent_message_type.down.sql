-- Restore the wider 000024 constraint. The rows rewritten by the up
-- migration are not restored: their original 'status' meaning was removed
-- from the application and cannot be reconstructed from the row itself.
ALTER TABLE agent_messages DROP CONSTRAINT IF EXISTS agent_messages_message_type_check;
ALTER TABLE agent_messages ADD CONSTRAINT agent_messages_message_type_check
    CHECK (message_type IN ('delegation', 'completion', 'status'));
