package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// CreatePromotion idempotently creates a campaign promotion record.
func (s *Store) CreatePromotion(ctx context.Context, cmd campaign.CreatePromotionCommand) (campaign.CampaignPromotion, bool, error) {
	if err := validateCreatePromotionCommand(cmd); err != nil {
		return campaign.CampaignPromotion{}, false, err
	}

	// 1. Check idempotency by (organization_id, idempotency_key).
	existing, err := s.getPromotionByIdempotencyKey(ctx, cmd.OrganizationID, cmd.IdempotencyKey)
	if err == nil {
		if existing.CanonicalHash != cmd.CanonicalHash {
			return campaign.CampaignPromotion{}, false, fmt.Errorf("%w: existing hash %s != requested hash %s",
				campaign.ErrIdempotencyConflict, existing.CanonicalHash, cmd.CanonicalHash)
		}
		return existing, true, nil
	} else if !errors.Is(err, campaign.ErrPromotionNotFound) {
		return campaign.CampaignPromotion{}, false, fmt.Errorf("check promotion idempotency: %w", err)
	}

	// 2. Check if a promotion already exists for this owner approval.
	existingByApproval, err := s.GetPromotionByApprovalID(ctx, cmd.OrganizationID, cmd.OwnerApprovalID)
	if err == nil {
		if existingByApproval.IdempotencyKey == cmd.IdempotencyKey && existingByApproval.CanonicalHash == cmd.CanonicalHash {
			return existingByApproval, true, nil
		}
		return campaign.CampaignPromotion{}, false, fmt.Errorf("%w: approval %d already promoted under promotion %d",
			campaign.ErrPromotionAlreadyExists, cmd.OwnerApprovalID, existingByApproval.ID)
	} else if !errors.Is(err, campaign.ErrPromotionNotFound) {
		return campaign.CampaignPromotion{}, false, fmt.Errorf("check approval promotion: %w", err)
	}

	budgetJSON, err := json.Marshal(cmd.ExecutionBudget)
	if err != nil {
		return campaign.CampaignPromotion{}, false, fmt.Errorf("marshal execution budget: %w", err)
	}

	const query = `
INSERT INTO campaign_promotions (
    organization_id,
    owner_approval_id,
    owner_approval_canonical_hash,
    proposal_id,
    proposal_canonical_hash,
    financial_review_id,
    financial_review_canonical_hash,
    execution_budget,
    execution_mode,
    executive_root_task_id,
    executive_correlation_id,
    executive_submit_idempotency_key,
    status,
    promoted_by_role_id,
    conversation_id,
    message_id,
    turn_task_id,
    tool_call_id,
    idempotency_key,
    canonical_hash
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
RETURNING
    id,
    organization_id,
    owner_approval_id,
    owner_approval_canonical_hash,
    proposal_id,
    proposal_canonical_hash,
    financial_review_id,
    financial_review_canonical_hash,
    execution_budget,
    execution_mode,
    executive_root_task_id,
    executive_correlation_id,
    executive_submit_idempotency_key,
    status,
    promoted_by_role_id,
    conversation_id,
    message_id,
    turn_task_id,
    tool_call_id,
    idempotency_key,
    canonical_hash,
    created_at;`

	var convID, msgID, turnID *int64
	if cmd.ConversationID > 0 {
		convID = &cmd.ConversationID
	}
	if cmd.MessageID > 0 {
		msgID = &cmd.MessageID
	}
	if cmd.TurnTaskID > 0 {
		turnID = &cmd.TurnTaskID
	}

	statusStr := string(cmd.Status)
	if statusStr == "" {
		statusStr = string(campaign.StatusSubmitted)
	}

	row := s.pool.QueryRow(ctx, query,
		cmd.OrganizationID,
		cmd.OwnerApprovalID,
		cmd.OwnerApprovalCanonicalHash,
		cmd.ProposalID,
		cmd.ProposalCanonicalHash,
		cmd.FinancialReviewID,
		cmd.FinancialReviewCanonicalHash,
		budgetJSON,
		string(cmd.ExecutionMode.Normalized()),
		cmd.ExecutiveRootTaskID,
		cmd.ExecutiveCorrelationID,
		cmd.ExecutiveSubmitIdempotencyKey,
		statusStr,
		cmd.PromotedByRoleID,
		convID,
		msgID,
		turnID,
		cmd.ToolCallID,
		cmd.IdempotencyKey,
		cmd.CanonicalHash,
	)

	prom, err := scanCampaignPromotion(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Unique violation: concurrent insertion. Re-query.
			if existing, fetchErr := s.getPromotionByIdempotencyKey(ctx, cmd.OrganizationID, cmd.IdempotencyKey); fetchErr == nil {
				if existing.CanonicalHash != cmd.CanonicalHash {
					return campaign.CampaignPromotion{}, false, fmt.Errorf("%w: concurrent write hash mismatch", campaign.ErrIdempotencyConflict)
				}
				return existing, true, nil
			}
			if existingByApproval, fetchErr := s.GetPromotionByApprovalID(ctx, cmd.OrganizationID, cmd.OwnerApprovalID); fetchErr == nil {
				return existingByApproval, true, nil
			}
		}
		return campaign.CampaignPromotion{}, false, fmt.Errorf("insert campaign promotion: %w", err)
	}

	return prom, false, nil
}

// GetPromotion retrieves a campaign promotion by ID.
func (s *Store) GetPromotion(ctx context.Context, organizationID string, id int64) (campaign.CampaignPromotion, error) {
	const query = `
SELECT
    id,
    organization_id,
    owner_approval_id,
    owner_approval_canonical_hash,
    proposal_id,
    proposal_canonical_hash,
    financial_review_id,
    financial_review_canonical_hash,
    execution_budget,
    execution_mode,
    executive_root_task_id,
    executive_correlation_id,
    executive_submit_idempotency_key,
    status,
    promoted_by_role_id,
    conversation_id,
    message_id,
    turn_task_id,
    tool_call_id,
    idempotency_key,
    canonical_hash,
    created_at
FROM campaign_promotions
WHERE organization_id = $1 AND id = $2;`

	row := s.pool.QueryRow(ctx, query, organizationID, id)
	prom, err := scanCampaignPromotion(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return campaign.CampaignPromotion{}, campaign.ErrPromotionNotFound
	}
	if err != nil {
		return campaign.CampaignPromotion{}, fmt.Errorf("get campaign promotion %d: %w", id, err)
	}
	return prom, nil
}

// GetPromotionByApprovalID retrieves a campaign promotion by owner approval ID.
func (s *Store) GetPromotionByApprovalID(ctx context.Context, organizationID string, approvalID int64) (campaign.CampaignPromotion, error) {
	const query = `
SELECT
    id,
    organization_id,
    owner_approval_id,
    owner_approval_canonical_hash,
    proposal_id,
    proposal_canonical_hash,
    financial_review_id,
    financial_review_canonical_hash,
    execution_budget,
    execution_mode,
    executive_root_task_id,
    executive_correlation_id,
    executive_submit_idempotency_key,
    status,
    promoted_by_role_id,
    conversation_id,
    message_id,
    turn_task_id,
    tool_call_id,
    idempotency_key,
    canonical_hash,
    created_at
FROM campaign_promotions
WHERE organization_id = $1 AND owner_approval_id = $2;`

	row := s.pool.QueryRow(ctx, query, organizationID, approvalID)
	prom, err := scanCampaignPromotion(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return campaign.CampaignPromotion{}, campaign.ErrPromotionNotFound
	}
	if err != nil {
		return campaign.CampaignPromotion{}, fmt.Errorf("get campaign promotion by approval %d: %w", approvalID, err)
	}
	return prom, nil
}

func (s *Store) getPromotionByIdempotencyKey(ctx context.Context, organizationID, key string) (campaign.CampaignPromotion, error) {
	const query = `
SELECT
    id,
    organization_id,
    owner_approval_id,
    owner_approval_canonical_hash,
    proposal_id,
    proposal_canonical_hash,
    financial_review_id,
    financial_review_canonical_hash,
    execution_budget,
    execution_mode,
    executive_root_task_id,
    executive_correlation_id,
    executive_submit_idempotency_key,
    status,
    promoted_by_role_id,
    conversation_id,
    message_id,
    turn_task_id,
    tool_call_id,
    idempotency_key,
    canonical_hash,
    created_at
FROM campaign_promotions
WHERE organization_id = $1 AND idempotency_key = $2;`

	row := s.pool.QueryRow(ctx, query, organizationID, key)
	prom, err := scanCampaignPromotion(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return campaign.CampaignPromotion{}, campaign.ErrPromotionNotFound
	}
	if err != nil {
		return campaign.CampaignPromotion{}, fmt.Errorf("get campaign promotion by idempotency key: %w", err)
	}
	return prom, nil
}

func scanCampaignPromotion(row pgx.Row) (campaign.CampaignPromotion, error) {
	var p campaign.CampaignPromotion
	var budgetJSON []byte
	var mode string
	var convID, msgID, turnID *int64

	err := row.Scan(
		&p.ID,
		&p.OrganizationID,
		&p.OwnerApprovalID,
		&p.OwnerApprovalCanonicalHash,
		&p.ProposalID,
		&p.ProposalCanonicalHash,
		&p.FinancialReviewID,
		&p.FinancialReviewCanonicalHash,
		&budgetJSON,
		&mode,
		&p.ExecutiveRootTaskID,
		&p.ExecutiveCorrelationID,
		&p.ExecutiveSubmitIdempotencyKey,
		&p.Status,
		&p.PromotedByRoleID,
		&convID,
		&msgID,
		&turnID,
		&p.ToolCallID,
		&p.IdempotencyKey,
		&p.CanonicalHash,
		&p.CreatedAt,
	)
	if err != nil {
		return p, err
	}
	if convID != nil {
		p.ConversationID = *convID
	}
	if msgID != nil {
		p.MessageID = *msgID
	}
	if turnID != nil {
		p.TurnTaskID = *turnID
	}
	if err := json.Unmarshal(budgetJSON, &p.ExecutionBudget); err != nil {
		return p, fmt.Errorf("unmarshal execution budget: %w", err)
	}
	p.ExecutionMode = campaign.ExecutionMode(mode)
	return p, nil
}

func validateCreatePromotionCommand(cmd campaign.CreatePromotionCommand) error {
	if cmd.OrganizationID == "" {
		return fmt.Errorf("%w: organization_id is required", campaign.ErrInvalidInput)
	}
	if cmd.OwnerApprovalID <= 0 {
		return fmt.Errorf("%w: owner_approval_id must be positive", campaign.ErrInvalidInput)
	}
	if !hashRegex.MatchString(cmd.OwnerApprovalCanonicalHash) {
		return fmt.Errorf("%w: owner_approval_canonical_hash must be a 64-char hex string", campaign.ErrInvalidInput)
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
	if _, err := executive.ExecutionModeRequirements(cmd.ExecutionMode); err != nil {
		return fmt.Errorf("%w: execution_mode: %v", campaign.ErrInvalidInput, err)
	}
	if cmd.ExecutiveRootTaskID <= 0 {
		return fmt.Errorf("%w: executive_root_task_id must be positive", campaign.ErrInvalidInput)
	}
	if cmd.ExecutiveCorrelationID == "" {
		return fmt.Errorf("%w: executive_correlation_id is required", campaign.ErrInvalidInput)
	}
	if cmd.ExecutiveSubmitIdempotencyKey == "" {
		return fmt.Errorf("%w: executive_submit_idempotency_key is required", campaign.ErrInvalidInput)
	}
	if cmd.PromotedByRoleID == "" {
		return fmt.Errorf("%w: promoted_by_role_id is required", campaign.ErrInvalidInput)
	}
	if cmd.IdempotencyKey == "" {
		return fmt.Errorf("%w: idempotency_key is required", campaign.ErrInvalidInput)
	}
	if !hashRegex.MatchString(cmd.CanonicalHash) {
		return fmt.Errorf("%w: canonical_hash must be a 64-char hex string", campaign.ErrInvalidInput)
	}
	return nil
}
