DROP INDEX IF EXISTS idx_campaign_proposal_root_rev;
DROP INDEX IF EXISTS uq_campaign_proposal_revision_lineage;
ALTER TABLE campaign_proposals
  DROP COLUMN IF EXISTS root_proposal_id,
  DROP COLUMN IF EXISTS revision_number,
  DROP COLUMN IF EXISTS parent_proposal_id;
