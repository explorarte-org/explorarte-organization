package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
)

// CreateRevision idempotently creates a new proposal revision with lineage.
func (s *Store) CreateRevision(ctx context.Context, cmd campaign.CreateRevisionCommand) (campaign.CampaignProposal, bool, error) {
	if err := validateCreateRevisionCommand(cmd); err != nil {
		return campaign.CampaignProposal{}, false, err
	}

	criteriaJSON, _ := json.Marshal(cmd.AcceptanceCriteria)
	reqsJSON, _ := json.Marshal(cmd.Requirements)
	budgetJSON, _ := json.Marshal(cmd.Budget)
	assumptionsJSON, _ := json.Marshal(cmd.Assumptions)
	risksJSON, _ := json.Marshal(cmd.Risks)
	questionsJSON, _ := json.Marshal(cmd.OpenQuestions)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return campaign.CampaignProposal{}, false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Lock and load parent proposal.
	var parent campaign.CampaignProposal
	var parentRootID *int64
	err = tx.QueryRow(ctx, `
		SELECT id, organization_id, revision_number, root_proposal_id, canonical_hash
		FROM campaign_proposals
		WHERE id = $1 AND organization_id = $2
		FOR UPDATE
	`, cmd.ParentProposalID, cmd.OrganizationID).Scan(
		&parent.ID, &parent.OrganizationID, &parent.RevisionNumber, &parentRootID, &parent.CanonicalHash,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return campaign.CampaignProposal{}, false, campaign.ErrProposalNotFound
		}
		return campaign.CampaignProposal{}, false, fmt.Errorf("load parent: %w", err)
	}

	// Determine root ID (parent.root or parent.id if no root yet).
	rootID := cmd.ParentProposalID
	if parentRootID != nil {
		rootID = *parentRootID
	}

	// Check idempotency.
	var existing campaign.CampaignProposal
	err = tx.QueryRow(ctx, `
		SELECT id, canonical_hash FROM campaign_proposals
		WHERE organization_id = $1 AND idempotency_key = $2
	`, cmd.OrganizationID, cmd.IdempotencyKey).Scan(&existing.ID, &existing.CanonicalHash)
	if err == nil {
		// Idempotent replay or conflict.
		if existing.CanonicalHash != cmd.CanonicalHash {
			tx.Rollback(ctx)
			return campaign.CampaignProposal{}, false, campaign.ErrIdempotencyConflict
		}
		tx.Rollback(ctx)
		full, err := s.GetProposal(ctx, cmd.OrganizationID, existing.ID)
		if err != nil {
			return campaign.CampaignProposal{}, false, err
		}
		return full, true, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return campaign.CampaignProposal{}, false, fmt.Errorf("check idempotency: %w", err)
	}

	// Verify parent is latest revision: no child with a higher revision number should exist.
	var maxRevision int
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(revision_number), 0)
		FROM campaign_proposals
		WHERE organization_id = $1 AND (root_proposal_id = $2 OR id = $2)
	`, cmd.OrganizationID, rootID).Scan(&maxRevision)
	if err != nil {
		return campaign.CampaignProposal{}, false, fmt.Errorf("check max revision: %w", err)
	}
	if maxRevision > parent.RevisionNumber {
		return campaign.CampaignProposal{}, false, campaign.ErrStaleParentRevision
	}

	newRevisionNumber := parent.RevisionNumber + 1

	// Insert new revision.
	var newID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO campaign_proposals (
			organization_id, conversation_id, created_by_role_id, created_from_message_id,
			task_id, attempt_id, tool_call_id, status, title, goal,
			acceptance_criteria, requirements, budget, assumptions, risks, open_questions,
			financial_review_required, execution_started, idempotency_key, canonical_hash,
			parent_proposal_id, revision_number, root_proposal_id
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, 'draft', $8, $9,
			$10, $11, $12, $13, $14, $15,
			TRUE, FALSE, $16, $17,
			$18, $19, $20
		) RETURNING id
	`,
		cmd.OrganizationID, cmd.ConversationID, cmd.CreatedByRoleID, cmd.CreatedFromMessageID,
		cmd.TaskID, cmd.AttemptID, cmd.ToolCallID, cmd.Title, cmd.Goal,
		criteriaJSON, reqsJSON, budgetJSON, assumptionsJSON, risksJSON, questionsJSON,
		cmd.IdempotencyKey, cmd.CanonicalHash,
		cmd.ParentProposalID, newRevisionNumber, rootID,
	).Scan(&newID)
	if err != nil {
		return campaign.CampaignProposal{}, false, fmt.Errorf("insert revision: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return campaign.CampaignProposal{}, false, fmt.Errorf("commit revision: %w", err)
	}

	proposal, err := s.GetProposal(ctx, cmd.OrganizationID, newID)
	if err != nil {
		return campaign.CampaignProposal{}, false, err
	}
	return proposal, false, nil
}

// GetLatestRevisionForRoot retrieves the latest revision for a proposal root lineage.
func (s *Store) GetLatestRevisionForRoot(ctx context.Context, organizationID string, rootProposalID int64) (campaign.CampaignProposal, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, conversation_id, created_by_role_id, created_from_message_id,
		       task_id, attempt_id, tool_call_id, status, title, goal,
		       acceptance_criteria, requirements, budget, assumptions, risks, open_questions,
		       financial_review_required, execution_started,
		       parent_proposal_id, revision_number, root_proposal_id,
		       idempotency_key, canonical_hash, created_at, updated_at
		FROM campaign_proposals
		WHERE organization_id = $1 AND (root_proposal_id = $2 OR id = $2)
		ORDER BY revision_number DESC
		LIMIT 1
	`, organizationID, rootProposalID)

	return scanProposalWithRevision(row)
}

// CreateOwnerApproval idempotently creates an owner execution approval.
func (s *Store) CreateOwnerApproval(ctx context.Context, cmd campaign.CreateOwnerApprovalCommand) (campaign.CampaignOwnerApproval, bool, error) {
	if err := validateCreateOwnerApprovalCommand(cmd); err != nil {
		return campaign.CampaignOwnerApproval{}, false, err
	}

	budgetJSON, err := json.Marshal(cmd.ExecutionBudget)
	if err != nil {
		return campaign.CampaignOwnerApproval{}, false, fmt.Errorf("marshal execution budget: %w", err)
	}

	// ON CONFLICT DO NOTHING (no explicit target) suppresses a violation of
	// EITHER of this table's two unique constraints -- uq_approval_org_key
	// (organization_id, idempotency_key), the same turn's own retry, and
	// uq_approval_exact_tuple (organization_id, proposal_canonical_hash,
	// financial_review_canonical_hash), the owner repeating an approval for
	// the same tuple from a DIFFERENT turn (a genuinely new idempotency
	// key) -- and returns no row for either, rather than letting either
	// raise a raw 23505 out of this function. A row IS returned if and
	// only if this call's own INSERT actually committed a new row: that,
	// not field-equality after the fact (which a fresh insert would also
	// trivially satisfy), is what "reused" means.
	row := s.pool.QueryRow(ctx, `
		INSERT INTO campaign_owner_approvals (
			organization_id, proposal_id, proposal_canonical_hash,
			financial_review_id, financial_review_canonical_hash,
			approved_by_role_id, conversation_id, message_id, turn_task_id, tool_call_id,
			status, execution_budget, idempotency_key, canonical_hash
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
			'approved_for_execution', $11, $12, $13
		)
		ON CONFLICT DO NOTHING
		RETURNING id, organization_id, proposal_id, proposal_canonical_hash,
		          financial_review_id, financial_review_canonical_hash,
		          approved_by_role_id, conversation_id, message_id, turn_task_id, tool_call_id,
		          status, execution_budget, idempotency_key, canonical_hash, created_at
	`, cmd.OrganizationID, cmd.ProposalID, cmd.ProposalCanonicalHash,
		cmd.FinancialReviewID, cmd.FinancialReviewCanonicalHash,
		cmd.ApprovedByRoleID, cmd.ConversationID, cmd.MessageID, cmd.TurnTaskID, cmd.ToolCallID,
		budgetJSON, cmd.IdempotencyKey, cmd.CanonicalHash,
	)

	inserted, err := scanApproval(row)
	if err == nil {
		return inserted, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return campaign.CampaignOwnerApproval{}, false, fmt.Errorf("insert approval: %w", err)
	}

	// No row inserted: something already conflicted. Load whichever
	// existing row actually caused it -- the same idempotency key first
	// (the more specific, same-turn-retry case), falling back to the
	// exact proposal/review tuple (the cross-turn convergence case) --
	// then decide reuse vs. conflict purely from that existing row's own
	// canonical hash, never from the input we just tried to insert.
	existingRow := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, proposal_id, proposal_canonical_hash,
		       financial_review_id, financial_review_canonical_hash,
		       approved_by_role_id, conversation_id, message_id, turn_task_id, tool_call_id,
		       status, execution_budget, idempotency_key, canonical_hash, created_at
		FROM campaign_owner_approvals
		WHERE organization_id = $1
		  AND (idempotency_key = $2
		       OR (proposal_canonical_hash = $3 AND financial_review_canonical_hash = $4))
		ORDER BY (idempotency_key = $2) DESC, id ASC
		LIMIT 1
	`, cmd.OrganizationID, cmd.IdempotencyKey, cmd.ProposalCanonicalHash, cmd.FinancialReviewCanonicalHash)
	existing, err := scanApproval(existingRow)
	if err != nil {
		return campaign.CampaignOwnerApproval{}, false, fmt.Errorf("load conflicting approval: %w", err)
	}
	if existing.CanonicalHash != cmd.CanonicalHash {
		return campaign.CampaignOwnerApproval{}, false, campaign.ErrApprovalConflict
	}
	return existing, true, nil
}

// GetOwnerApproval retrieves an owner approval by ID.
func (s *Store) GetOwnerApproval(ctx context.Context, organizationID string, approvalID int64) (campaign.CampaignOwnerApproval, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, proposal_id, proposal_canonical_hash,
		       financial_review_id, financial_review_canonical_hash,
		       approved_by_role_id, conversation_id, message_id, turn_task_id, tool_call_id,
		       status, execution_budget, idempotency_key, canonical_hash, created_at
		FROM campaign_owner_approvals
		WHERE id = $1 AND organization_id = $2
	`, approvalID, organizationID)

	approval, err := scanApproval(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return campaign.CampaignOwnerApproval{}, campaign.ErrApprovalNotFound
		}
		return campaign.CampaignOwnerApproval{}, err
	}
	return approval, nil
}

// GetOwnerApprovalByProposal retrieves the owner approval for a specific proposal.
func (s *Store) GetOwnerApprovalByProposal(ctx context.Context, organizationID string, proposalID int64) (campaign.CampaignOwnerApproval, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, proposal_id, proposal_canonical_hash,
		       financial_review_id, financial_review_canonical_hash,
		       approved_by_role_id, conversation_id, message_id, turn_task_id, tool_call_id,
		       status, execution_budget, idempotency_key, canonical_hash, created_at
		FROM campaign_owner_approvals
		WHERE organization_id = $1 AND proposal_id = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, organizationID, proposalID)

	approval, err := scanApproval(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return campaign.CampaignOwnerApproval{}, campaign.ErrApprovalNotFound
		}
		return campaign.CampaignOwnerApproval{}, err
	}
	return approval, nil
}

func scanApproval(row pgx.Row) (campaign.CampaignOwnerApproval, error) {
	var a campaign.CampaignOwnerApproval
	var budgetJSON []byte
	err := row.Scan(
		&a.ID, &a.OrganizationID, &a.ProposalID, &a.ProposalCanonicalHash,
		&a.FinancialReviewID, &a.FinancialReviewCanonicalHash,
		&a.ApprovedByRoleID, &a.ConversationID, &a.MessageID, &a.TurnTaskID, &a.ToolCallID,
		&a.Status, &budgetJSON, &a.IdempotencyKey, &a.CanonicalHash, &a.CreatedAt,
	)
	if err != nil {
		return a, err
	}
	if err := json.Unmarshal(budgetJSON, &a.ExecutionBudget); err != nil {
		return a, fmt.Errorf("unmarshal execution budget: %w", err)
	}
	return a, nil
}

func scanProposalWithRevision(row pgx.Row) (campaign.CampaignProposal, error) {
	var p campaign.CampaignProposal
	var criteriaJSON, reqsJSON, budgetJSON, assumptionsJSON, risksJSON, questionsJSON []byte

	err := row.Scan(
		&p.ID, &p.OrganizationID, &p.ConversationID, &p.CreatedByRoleID, &p.CreatedFromMessageID,
		&p.TaskID, &p.AttemptID, &p.ToolCallID, &p.Status, &p.Title, &p.Goal,
		&criteriaJSON, &reqsJSON, &budgetJSON, &assumptionsJSON, &risksJSON, &questionsJSON,
		&p.FinancialReviewRequired, &p.ExecutionStarted,
		&p.ParentProposalID, &p.RevisionNumber, &p.RootProposalID,
		&p.IdempotencyKey, &p.CanonicalHash, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return p, err
	}
	_ = json.Unmarshal(criteriaJSON, &p.AcceptanceCriteria)
	_ = json.Unmarshal(reqsJSON, &p.Requirements)
	_ = json.Unmarshal(budgetJSON, &p.Budget)
	_ = json.Unmarshal(assumptionsJSON, &p.Assumptions)
	_ = json.Unmarshal(risksJSON, &p.Risks)
	_ = json.Unmarshal(questionsJSON, &p.OpenQuestions)
	return p, nil
}

func validateCreateRevisionCommand(cmd campaign.CreateRevisionCommand) error {
	if cmd.OrganizationID == "" {
		return fmt.Errorf("%w: organization_id is required", campaign.ErrInvalidInput)
	}
	if cmd.ParentProposalID <= 0 {
		return fmt.Errorf("%w: parent_proposal_id must be positive", campaign.ErrInvalidInput)
	}
	if cmd.Title == "" {
		return fmt.Errorf("%w: title is required", campaign.ErrInvalidInput)
	}
	if cmd.Goal == "" {
		return fmt.Errorf("%w: goal is required", campaign.ErrInvalidInput)
	}
	if cmd.IdempotencyKey == "" {
		return fmt.Errorf("%w: idempotency_key is required", campaign.ErrInvalidInput)
	}
	if !hashRegex.MatchString(cmd.CanonicalHash) {
		return fmt.Errorf("%w: canonical_hash must be a 64-char hex string", campaign.ErrInvalidInput)
	}
	return nil
}

func validateCreateOwnerApprovalCommand(cmd campaign.CreateOwnerApprovalCommand) error {
	if cmd.OrganizationID == "" {
		return fmt.Errorf("%w: organization_id is required", campaign.ErrInvalidInput)
	}
	if cmd.ProposalID <= 0 {
		return fmt.Errorf("%w: proposal_id must be positive", campaign.ErrInvalidInput)
	}
	if !hashRegex.MatchString(cmd.ProposalCanonicalHash) {
		return fmt.Errorf("%w: proposal_canonical_hash must be a 64-char hex string", campaign.ErrInvalidInput)
	}
	if cmd.FinancialReviewID <= 0 {
		return fmt.Errorf("%w: financial_review_id must be positive", campaign.ErrInvalidInput)
	}
	if !hashRegex.MatchString(cmd.FinancialReviewCanonicalHash) {
		return fmt.Errorf("%w: financial_review_canonical_hash must be a 64-char hex string", campaign.ErrInvalidInput)
	}
	if cmd.ApprovedByRoleID == "" {
		return fmt.Errorf("%w: approved_by_role_id is required", campaign.ErrInvalidInput)
	}
	if cmd.IdempotencyKey == "" {
		return fmt.Errorf("%w: idempotency_key is required", campaign.ErrInvalidInput)
	}
	if !hashRegex.MatchString(cmd.CanonicalHash) {
		return fmt.Errorf("%w: canonical_hash must be a 64-char hex string", campaign.ErrInvalidInput)
	}
	return nil
}
