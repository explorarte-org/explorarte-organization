-- CEO_CONVERSATIONAL_CAMPAIGN_REVISION_AND_OWNER_APPROVAL_V1: proposal revision lineage.
--
-- Adds parent/revision/root columns to campaign_proposals to support immutable
-- revision chains without a separate entity. Existing rows become revision 1.

ALTER TABLE campaign_proposals
  ADD COLUMN IF NOT EXISTS parent_proposal_id BIGINT REFERENCES campaign_proposals(id),
  ADD COLUMN IF NOT EXISTS revision_number    INTEGER NOT NULL DEFAULT 1,
  ADD COLUMN IF NOT EXISTS root_proposal_id   BIGINT  REFERENCES campaign_proposals(id);

-- Backfill existing proposals as revision 1 with root = self.
UPDATE campaign_proposals SET root_proposal_id = id WHERE root_proposal_id IS NULL;

-- Enforce lineage uniqueness: no two revisions with the same number for the same root.
CREATE UNIQUE INDEX IF NOT EXISTS uq_campaign_proposal_revision_lineage
  ON campaign_proposals (organization_id, root_proposal_id, revision_number);

-- Index for efficient latest-revision lookups.
CREATE INDEX IF NOT EXISTS idx_campaign_proposal_root_rev
  ON campaign_proposals (root_proposal_id, revision_number DESC);
