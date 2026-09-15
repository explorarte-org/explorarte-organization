package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
)

var hashRegex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Store provides PostgreSQL-backed persistence for campaign proposals and financial reviews.
type Store struct {
	pool *pgxpool.Pool
}

// New creates a new Store instance.
func New(store *platformpostgres.Store) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("campaign store requires an initialized PostgreSQL store")
	}
	return &Store{pool: store.Pool()}, nil
}

// CreateProposal creates a new draft campaign proposal or returns the existing
// one if an identical proposal was already submitted under the same idempotency key.
func (s *Store) CreateProposal(ctx context.Context, cmd campaign.CreateProposalCommand) (campaign.CampaignProposal, bool, error) {
	if err := validateCreateProposalCommand(cmd); err != nil {
		return campaign.CampaignProposal{}, false, err
	}

	criteriaJSON, err := json.Marshal(cmd.AcceptanceCriteria)
	if err != nil {
		return campaign.CampaignProposal{}, false, fmt.Errorf("marshal acceptance criteria: %w", err)
	}

	reqsJSON, err := json.Marshal(cmd.Requirements)
	if err != nil {
		return campaign.CampaignProposal{}, false, fmt.Errorf("marshal requirements: %w", err)
	}

	budgetJSON, err := json.Marshal(cmd.Budget)
	if err != nil {
		return campaign.CampaignProposal{}, false, fmt.Errorf("marshal budget: %w", err)
	}

	assumptionsJSON, err := json.Marshal(cmd.Assumptions)
	if err != nil {
		return campaign.CampaignProposal{}, false, fmt.Errorf("marshal assumptions: %w", err)
	}

	risksJSON, err := json.Marshal(cmd.Risks)
	if err != nil {
		return campaign.CampaignProposal{}, false, fmt.Errorf("marshal risks: %w", err)
	}

	questionsJSON, err := json.Marshal(cmd.OpenQuestions)
	if err != nil {
		return campaign.CampaignProposal{}, false, fmt.Errorf("marshal open questions: %w", err)
	}

	row := s.pool.QueryRow(ctx, `
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
			NULL, 1, NULL
		)
		ON CONFLICT (organization_id, idempotency_key) DO NOTHING
		RETURNING id, organization_id, conversation_id, created_by_role_id, created_from_message_id,
		          task_id, attempt_id, tool_call_id, status, title, goal,
		          acceptance_criteria, requirements, budget, assumptions, risks, open_questions,
		          financial_review_required, execution_started,
		       parent_proposal_id, revision_number, root_proposal_id,
		       idempotency_key, canonical_hash,
		          created_at, updated_at`,
		cmd.OrganizationID, cmd.ConversationID, cmd.CreatedByRoleID, cmd.CreatedFromMessageID,
		cmd.TaskID, cmd.AttemptID, cmd.ToolCallID, cmd.Title, cmd.Goal,
		criteriaJSON, reqsJSON, budgetJSON, assumptionsJSON, risksJSON, questionsJSON,
		cmd.IdempotencyKey, cmd.CanonicalHash,
	)

	proposal, err := scanProposal(row)
	if err == nil {
		return proposal, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return campaign.CampaignProposal{}, false, fmt.Errorf("insert campaign proposal: %w", err)
	}

	// Read conflicting row
	existing, err := s.getProposalByIdempotencyKey(ctx, cmd.OrganizationID, cmd.IdempotencyKey)
	if err != nil {
		return campaign.CampaignProposal{}, false, fmt.Errorf("read conflicting proposal: %w", err)
	}

	if existing.CanonicalHash == cmd.CanonicalHash {
		return existing, true, nil
	}

	return campaign.CampaignProposal{}, false, fmt.Errorf("%w: proposal already exists with different canonical hash", campaign.ErrIdempotencyConflict)
}

func (s *Store) getProposalByIdempotencyKey(ctx context.Context, organizationID, key string) (campaign.CampaignProposal, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, conversation_id, created_by_role_id, created_from_message_id,
		       task_id, attempt_id, tool_call_id, status, title, goal,
		       acceptance_criteria, requirements, budget, assumptions, risks, open_questions,
		       financial_review_required, execution_started,
		       parent_proposal_id, revision_number, root_proposal_id,
		       idempotency_key, canonical_hash,
		       created_at, updated_at
		FROM campaign_proposals
		WHERE organization_id = $1 AND idempotency_key = $2`, organizationID, key)

	p, err := scanProposal(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return campaign.CampaignProposal{}, campaign.ErrProposalNotFound
	}
	return p, err
}

// GetProposal retrieves a campaign proposal by organization ID and proposal ID.
func (s *Store) GetProposal(ctx context.Context, organizationID string, id int64) (campaign.CampaignProposal, error) {
	if strings.TrimSpace(organizationID) == "" {
		return campaign.CampaignProposal{}, fmt.Errorf("%w: organization ID is required", campaign.ErrInvalidInput)
	}
	if id <= 0 {
		return campaign.CampaignProposal{}, fmt.Errorf("%w: proposal ID must be positive", campaign.ErrInvalidInput)
	}

	row := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, conversation_id, created_by_role_id, created_from_message_id,
		       task_id, attempt_id, tool_call_id, status, title, goal,
		       acceptance_criteria, requirements, budget, assumptions, risks, open_questions,
		       financial_review_required, execution_started,
		       parent_proposal_id, revision_number, root_proposal_id,
		       idempotency_key, canonical_hash,
		       created_at, updated_at
		FROM campaign_proposals
		WHERE organization_id = $1 AND id = $2`, organizationID, id)

	p, err := scanProposal(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return campaign.CampaignProposal{}, campaign.ErrProposalNotFound
	}
	return p, err
}

// ListProposals lists campaign proposals for an organization ordered newest first.
func (s *Store) ListProposals(ctx context.Context, organizationID string, limit, offset int) ([]campaign.CampaignProposal, error) {
	if strings.TrimSpace(organizationID) == "" {
		return nil, fmt.Errorf("%w: organization ID is required", campaign.ErrInvalidInput)
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, organization_id, conversation_id, created_by_role_id, created_from_message_id,
		       task_id, attempt_id, tool_call_id, status, title, goal,
		       acceptance_criteria, requirements, budget, assumptions, risks, open_questions,
		       financial_review_required, execution_started,
		       parent_proposal_id, revision_number, root_proposal_id,
		       idempotency_key, canonical_hash,
		       created_at, updated_at
		FROM campaign_proposals
		WHERE organization_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2 OFFSET $3`, organizationID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list campaign proposals: %w", err)
	}
	defer rows.Close()

	var proposals []campaign.CampaignProposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, fmt.Errorf("scan campaign proposal: %w", err)
		}
		proposals = append(proposals, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate campaign proposals: %w", err)
	}
	return proposals, nil
}

// CreateReviewRequest creates a new review request for a proposal or returns an
// existing one if an identical request was already recorded under the same idempotency key.
func (s *Store) CreateReviewRequest(ctx context.Context, cmd campaign.CreateReviewRequestCommand) (campaign.CampaignFinancialReviewRequest, bool, error) {
	if err := validateCreateReviewRequestCommand(cmd); err != nil {
		return campaign.CampaignFinancialReviewRequest{}, false, err
	}

	// Verify proposal exists and canonical hash matches
	proposal, err := s.GetProposal(ctx, cmd.OrganizationID, cmd.ProposalID)
	if err != nil {
		return campaign.CampaignFinancialReviewRequest{}, false, err
	}
	if proposal.CanonicalHash != cmd.ProposalCanonicalHash {
		return campaign.CampaignFinancialReviewRequest{}, false, fmt.Errorf("%w: proposal canonical hash %q != request hash %q",
			campaign.ErrProposalHashMismatch, proposal.CanonicalHash, cmd.ProposalCanonicalHash)
	}

	var convID, msgID, taskID any
	if cmd.RequestedFromConversationID > 0 {
		convID = cmd.RequestedFromConversationID
	}
	if cmd.RequestedFromMessageID > 0 {
		msgID = cmd.RequestedFromMessageID
	}
	if cmd.RequestedFromTaskID > 0 {
		taskID = cmd.RequestedFromTaskID
	}

	row := s.pool.QueryRow(ctx, `
		INSERT INTO campaign_financial_review_requests (
			organization_id, proposal_id, proposal_canonical_hash,
			requested_by_role_id, requested_from_conversation_id,
			requested_from_message_id, requested_from_task_id,
			reviewer_role_id, review_task_id, status, idempotency_key
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, 'pending', $10
		)
		ON CONFLICT (organization_id, idempotency_key) DO NOTHING
		RETURNING id, organization_id, proposal_id, proposal_canonical_hash,
		          requested_by_role_id, requested_from_conversation_id,
		          requested_from_message_id, requested_from_task_id,
		          reviewer_role_id, review_task_id, status, idempotency_key,
		          created_at, updated_at`,
		cmd.OrganizationID, cmd.ProposalID, cmd.ProposalCanonicalHash,
		cmd.RequestedByRoleID, convID, msgID, taskID,
		cmd.ReviewerRoleID, cmd.ReviewTaskID, cmd.IdempotencyKey,
	)

	req, err := scanReviewRequest(row)
	if err == nil {
		return req, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return campaign.CampaignFinancialReviewRequest{}, false, fmt.Errorf("insert review request: %w", err)
	}

	// Read conflicting row
	existing, err := s.getReviewRequestByIdempotencyKey(ctx, cmd.OrganizationID, cmd.IdempotencyKey)
	if err != nil {
		return campaign.CampaignFinancialReviewRequest{}, false, fmt.Errorf("read conflicting review request: %w", err)
	}

	if existing.ProposalCanonicalHash == cmd.ProposalCanonicalHash && existing.ProposalID == cmd.ProposalID {
		return existing, true, nil
	}

	return campaign.CampaignFinancialReviewRequest{}, false, fmt.Errorf("%w: review request exists with different proposal or canonical hash", campaign.ErrIdempotencyConflict)
}

func (s *Store) getReviewRequestByIdempotencyKey(ctx context.Context, organizationID, key string) (campaign.CampaignFinancialReviewRequest, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, proposal_id, proposal_canonical_hash,
		       requested_by_role_id, requested_from_conversation_id,
		       requested_from_message_id, requested_from_task_id,
		       reviewer_role_id, review_task_id, status, idempotency_key,
		       created_at, updated_at
		FROM campaign_financial_review_requests
		WHERE organization_id = $1 AND idempotency_key = $2`, organizationID, key)

	req, err := scanReviewRequest(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return campaign.CampaignFinancialReviewRequest{}, campaign.ErrReviewRequestNotFound
	}
	return req, err
}

// GetReviewRequest retrieves a review request by organization ID and ID.
func (s *Store) GetReviewRequest(ctx context.Context, organizationID string, id int64) (campaign.CampaignFinancialReviewRequest, error) {
	if strings.TrimSpace(organizationID) == "" {
		return campaign.CampaignFinancialReviewRequest{}, fmt.Errorf("%w: organization ID is required", campaign.ErrInvalidInput)
	}
	if id <= 0 {
		return campaign.CampaignFinancialReviewRequest{}, fmt.Errorf("%w: request ID must be positive", campaign.ErrInvalidInput)
	}

	row := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, proposal_id, proposal_canonical_hash,
		       requested_by_role_id, requested_from_conversation_id,
		       requested_from_message_id, requested_from_task_id,
		       reviewer_role_id, review_task_id, status, idempotency_key,
		       created_at, updated_at
		FROM campaign_financial_review_requests
		WHERE organization_id = $1 AND id = $2`, organizationID, id)

	req, err := scanReviewRequest(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return campaign.CampaignFinancialReviewRequest{}, campaign.ErrReviewRequestNotFound
	}
	return req, err
}

// GetLatestReviewRequestForProposal returns the most recent review request for a proposal.
func (s *Store) GetLatestReviewRequestForProposal(ctx context.Context, organizationID string, proposalID int64) (campaign.CampaignFinancialReviewRequest, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, proposal_id, proposal_canonical_hash,
		       requested_by_role_id, requested_from_conversation_id,
		       requested_from_message_id, requested_from_task_id,
		       reviewer_role_id, review_task_id, status, idempotency_key,
		       created_at, updated_at
		FROM campaign_financial_review_requests
		WHERE organization_id = $1 AND proposal_id = $2
		ORDER BY created_at DESC, id DESC
		LIMIT 1`, organizationID, proposalID)

	req, err := scanReviewRequest(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return campaign.CampaignFinancialReviewRequest{}, campaign.ErrReviewRequestNotFound
	}
	return req, err
}

// RecordFinancialReview idempotently stores an immutable financial review.
func (s *Store) RecordFinancialReview(ctx context.Context, cmd campaign.RecordFinancialReviewCommand) (campaign.CampaignFinancialReview, bool, error) {
	if err := validateRecordFinancialReviewCommand(cmd); err != nil {
		return campaign.CampaignFinancialReview{}, false, err
	}

	// Verify review request exists
	req, err := s.GetReviewRequest(ctx, cmd.OrganizationID, cmd.ReviewRequestID)
	if err != nil {
		return campaign.CampaignFinancialReview{}, false, err
	}

	// Verify proposal canonical hash matches
	if req.ProposalCanonicalHash != cmd.ProposalCanonicalHash {
		return campaign.CampaignFinancialReview{}, false, fmt.Errorf("%w: review canonical hash %q != request hash %q",
			campaign.ErrProposalHashMismatch, cmd.ProposalCanonicalHash, req.ProposalCanonicalHash)
	}

	// Separation of Duties checks:
	// 1. Reviewer role must not be the request creator or proposal creator
	if cmd.ReviewerRoleID == req.RequestedByRoleID || cmd.ReviewerRoleID == "empresa/ceo" || cmd.ReviewerRoleID == "empresa/human" {
		return campaign.CampaignFinancialReview{}, false, fmt.Errorf("%w: reviewer role %q cannot be CEO, owner, or requester",
			campaign.ErrSeparationOfDutiesViolation, cmd.ReviewerRoleID)
	}
	// 2. Reviewer role must match the request assigned reviewer
	if cmd.ReviewerRoleID != req.ReviewerRoleID {
		return campaign.CampaignFinancialReview{}, false, fmt.Errorf("%w: reviewer role %q does not match assigned reviewer %q",
			campaign.ErrSeparationOfDutiesViolation, cmd.ReviewerRoleID, req.ReviewerRoleID)
	}

	var budgetJSON, costJSON []byte
	if cmd.RecommendedBudget != nil {
		budgetJSON, err = json.Marshal(cmd.RecommendedBudget)
		if err != nil {
			return campaign.CampaignFinancialReview{}, false, fmt.Errorf("marshal recommended budget: %w", err)
		}
	}
	if cmd.EstimatedCost != nil {
		costJSON, err = json.Marshal(cmd.EstimatedCost)
		if err != nil {
			return campaign.CampaignFinancialReview{}, false, fmt.Errorf("marshal estimated cost: %w", err)
		}
	}

	assumptionsJSON, err := json.Marshal(cmd.Assumptions)
	if err != nil {
		return campaign.CampaignFinancialReview{}, false, fmt.Errorf("marshal assumptions: %w", err)
	}
	risksJSON, err := json.Marshal(cmd.Risks)
	if err != nil {
		return campaign.CampaignFinancialReview{}, false, fmt.Errorf("marshal risks: %w", err)
	}
	correctionsJSON, err := json.Marshal(cmd.RequiredCorrections)
	if err != nil {
		return campaign.CampaignFinancialReview{}, false, fmt.Errorf("marshal corrections: %w", err)
	}
	missingJSON, err := json.Marshal(cmd.MissingInformation)
	if err != nil {
		return campaign.CampaignFinancialReview{}, false, fmt.Errorf("marshal missing information: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return campaign.CampaignFinancialReview{}, false, fmt.Errorf("begin review tx: %w", err)
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx, `
		INSERT INTO campaign_financial_reviews (
			organization_id, review_request_id, proposal_id, proposal_canonical_hash,
			reviewer_role_id, review_task_id, review_attempt_id, verdict,
			recommended_budget, estimated_cost, assumptions, risks,
			required_corrections, missing_information, summary, canonical_hash
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8,
			$9, $10, $11, $12,
			$13, $14, $15, $16
		)
		ON CONFLICT (organization_id, review_request_id) DO NOTHING
		RETURNING id, organization_id, review_request_id, proposal_id, proposal_canonical_hash,
		          reviewer_role_id, review_task_id, review_attempt_id, verdict,
		          recommended_budget, estimated_cost, assumptions, risks,
		          required_corrections, missing_information, summary, canonical_hash,
		          created_at`,
		cmd.OrganizationID, cmd.ReviewRequestID, cmd.ProposalID, cmd.ProposalCanonicalHash,
		cmd.ReviewerRoleID, cmd.ReviewTaskID, cmd.ReviewAttemptID, string(cmd.Verdict),
		budgetJSON, costJSON, assumptionsJSON, risksJSON,
		correctionsJSON, missingJSON, cmd.Summary, cmd.CanonicalHash,
	)

	review, err := scanFinancialReview(row)
	if err == nil {
		// Update review request status to completed
		_, err = tx.Exec(ctx, `
			UPDATE campaign_financial_review_requests
			SET status = 'completed', updated_at = NOW()
			WHERE id = $1 AND organization_id = $2`, cmd.ReviewRequestID, cmd.OrganizationID)
		if err != nil {
			return campaign.CampaignFinancialReview{}, false, fmt.Errorf("update request status to completed: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return campaign.CampaignFinancialReview{}, false, fmt.Errorf("commit review tx: %w", err)
		}
		return review, false, nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return campaign.CampaignFinancialReview{}, false, fmt.Errorf("insert financial review: %w", err)
	}

	// Read existing review on conflict
	existing, err := s.GetFinancialReviewByRequestID(ctx, cmd.OrganizationID, cmd.ReviewRequestID)
	if err != nil {
		return campaign.CampaignFinancialReview{}, false, fmt.Errorf("read conflicting financial review: %w", err)
	}

	if existing.CanonicalHash == cmd.CanonicalHash {
		return existing, true, nil
	}

	return campaign.CampaignFinancialReview{}, false, fmt.Errorf("%w: review already exists with different canonical hash", campaign.ErrIdempotencyConflict)
}

// GetFinancialReview retrieves a financial review by organization ID and ID.
func (s *Store) GetFinancialReview(ctx context.Context, organizationID string, id int64) (campaign.CampaignFinancialReview, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, review_request_id, proposal_id, proposal_canonical_hash,
		       reviewer_role_id, review_task_id, review_attempt_id, verdict,
		       recommended_budget, estimated_cost, assumptions, risks,
		       required_corrections, missing_information, summary, canonical_hash,
		       created_at
		FROM campaign_financial_reviews
		WHERE organization_id = $1 AND id = $2`, organizationID, id)

	review, err := scanFinancialReview(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return campaign.CampaignFinancialReview{}, campaign.ErrFinancialReviewNotFound
	}
	return review, err
}

// GetFinancialReviewByRequestID retrieves a financial review by its review request ID.
func (s *Store) GetFinancialReviewByRequestID(ctx context.Context, organizationID string, requestID int64) (campaign.CampaignFinancialReview, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, review_request_id, proposal_id, proposal_canonical_hash,
		       reviewer_role_id, review_task_id, review_attempt_id, verdict,
		       recommended_budget, estimated_cost, assumptions, risks,
		       required_corrections, missing_information, summary, canonical_hash,
		       created_at
		FROM campaign_financial_reviews
		WHERE organization_id = $1 AND review_request_id = $2`, organizationID, requestID)

	review, err := scanFinancialReview(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return campaign.CampaignFinancialReview{}, campaign.ErrFinancialReviewNotFound
	}
	return review, err
}

// GetLatestFinancialReviewForProposal returns the most recent financial review for a proposal.
func (s *Store) GetLatestFinancialReviewForProposal(ctx context.Context, organizationID string, proposalID int64) (campaign.CampaignFinancialReview, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, review_request_id, proposal_id, proposal_canonical_hash,
		       reviewer_role_id, review_task_id, review_attempt_id, verdict,
		       recommended_budget, estimated_cost, assumptions, risks,
		       required_corrections, missing_information, summary, canonical_hash,
		       created_at
		FROM campaign_financial_reviews
		WHERE organization_id = $1 AND proposal_id = $2
		ORDER BY created_at DESC, id DESC
		LIMIT 1`, organizationID, proposalID)

	review, err := scanFinancialReview(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return campaign.CampaignFinancialReview{}, campaign.ErrFinancialReviewNotFound
	}
	return review, err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanProposal(scanner rowScanner) (campaign.CampaignProposal, error) {
	var (
		p                campaign.CampaignProposal
		statusStr        string
		criteriaBytes    []byte
		reqsBytes        []byte
		budgetBytes      []byte
		assumptionsBytes []byte
		risksBytes       []byte
		questionsBytes   []byte
	)

	err := scanner.Scan(
		&p.ID,
		&p.OrganizationID,
		&p.ConversationID,
		&p.CreatedByRoleID,
		&p.CreatedFromMessageID,
		&p.TaskID,
		&p.AttemptID,
		&p.ToolCallID,
		&statusStr,
		&p.Title,
		&p.Goal,
		&criteriaBytes,
		&reqsBytes,
		&budgetBytes,
		&assumptionsBytes,
		&risksBytes,
		&questionsBytes,
		&p.FinancialReviewRequired,
		&p.ExecutionStarted,
		&p.ParentProposalID,
		&p.RevisionNumber,
		&p.RootProposalID,
		&p.IdempotencyKey,
		&p.CanonicalHash,
		&p.CreatedAt,
		&p.UpdatedAt,
	)
	if err != nil {
		return campaign.CampaignProposal{}, err
	}

	p.Status = campaign.ProposalStatus(statusStr)

	if len(criteriaBytes) > 0 {
		if err := json.Unmarshal(criteriaBytes, &p.AcceptanceCriteria); err != nil {
			return campaign.CampaignProposal{}, fmt.Errorf("unmarshal criteria: %w", err)
		}
	} else {
		p.AcceptanceCriteria = []string{}
	}

	if len(reqsBytes) > 0 {
		if err := json.Unmarshal(reqsBytes, &p.Requirements); err != nil {
			return campaign.CampaignProposal{}, fmt.Errorf("unmarshal requirements: %w", err)
		}
	} else {
		p.Requirements = []campaign.ProposalRequirement{}
	}

	if len(budgetBytes) > 0 && string(budgetBytes) != "null" {
		p.Budget = &campaign.ProposalBudget{}
		if err := json.Unmarshal(budgetBytes, p.Budget); err != nil {
			return campaign.CampaignProposal{}, fmt.Errorf("unmarshal budget: %w", err)
		}
	}

	if len(assumptionsBytes) > 0 {
		if err := json.Unmarshal(assumptionsBytes, &p.Assumptions); err != nil {
			return campaign.CampaignProposal{}, fmt.Errorf("unmarshal assumptions: %w", err)
		}
	} else {
		p.Assumptions = []string{}
	}

	if len(risksBytes) > 0 {
		if err := json.Unmarshal(risksBytes, &p.Risks); err != nil {
			return campaign.CampaignProposal{}, fmt.Errorf("unmarshal risks: %w", err)
		}
	} else {
		p.Risks = []string{}
	}

	if len(questionsBytes) > 0 {
		if err := json.Unmarshal(questionsBytes, &p.OpenQuestions); err != nil {
			return campaign.CampaignProposal{}, fmt.Errorf("unmarshal questions: %w", err)
		}
	} else {
		p.OpenQuestions = []string{}
	}

	return p, nil
}

func scanReviewRequest(scanner rowScanner) (campaign.CampaignFinancialReviewRequest, error) {
	var (
		req    campaign.CampaignFinancialReviewRequest
		status string
		convID *int64
		msgID  *int64
		taskID *int64
	)

	err := scanner.Scan(
		&req.ID,
		&req.OrganizationID,
		&req.ProposalID,
		&req.ProposalCanonicalHash,
		&req.RequestedByRoleID,
		&convID,
		&msgID,
		&taskID,
		&req.ReviewerRoleID,
		&req.ReviewTaskID,
		&status,
		&req.IdempotencyKey,
		&req.CreatedAt,
		&req.UpdatedAt,
	)
	if err != nil {
		return campaign.CampaignFinancialReviewRequest{}, err
	}

	req.Status = campaign.ReviewRequestStatus(status)
	if convID != nil {
		req.RequestedFromConversationID = *convID
	}
	if msgID != nil {
		req.RequestedFromMessageID = *msgID
	}
	if taskID != nil {
		req.RequestedFromTaskID = *taskID
	}
	return req, nil
}

func scanFinancialReview(scanner rowScanner) (campaign.CampaignFinancialReview, error) {
	var (
		rev              campaign.CampaignFinancialReview
		verdict          string
		budgetBytes      []byte
		costBytes        []byte
		assumptionsBytes []byte
		risksBytes       []byte
		correctionsBytes []byte
		missingBytes     []byte
	)

	err := scanner.Scan(
		&rev.ID,
		&rev.OrganizationID,
		&rev.ReviewRequestID,
		&rev.ProposalID,
		&rev.ProposalCanonicalHash,
		&rev.ReviewerRoleID,
		&rev.ReviewTaskID,
		&rev.ReviewAttemptID,
		&verdict,
		&budgetBytes,
		&costBytes,
		&assumptionsBytes,
		&risksBytes,
		&correctionsBytes,
		&missingBytes,
		&rev.Summary,
		&rev.CanonicalHash,
		&rev.CreatedAt,
	)
	if err != nil {
		return campaign.CampaignFinancialReview{}, err
	}

	rev.Verdict = campaign.FinancialReviewVerdict(verdict)

	if len(budgetBytes) > 0 && string(budgetBytes) != "null" {
		rev.RecommendedBudget = &campaign.BudgetRecommendation{}
		if err := json.Unmarshal(budgetBytes, rev.RecommendedBudget); err != nil {
			return campaign.CampaignFinancialReview{}, fmt.Errorf("unmarshal recommended budget: %w", err)
		}
	}
	if len(costBytes) > 0 && string(costBytes) != "null" {
		rev.EstimatedCost = &campaign.EstimatedCost{}
		if err := json.Unmarshal(costBytes, rev.EstimatedCost); err != nil {
			return campaign.CampaignFinancialReview{}, fmt.Errorf("unmarshal estimated cost: %w", err)
		}
	}

	if len(assumptionsBytes) > 0 {
		if err := json.Unmarshal(assumptionsBytes, &rev.Assumptions); err != nil {
			return campaign.CampaignFinancialReview{}, fmt.Errorf("unmarshal assumptions: %w", err)
		}
	} else {
		rev.Assumptions = []string{}
	}

	if len(risksBytes) > 0 {
		if err := json.Unmarshal(risksBytes, &rev.Risks); err != nil {
			return campaign.CampaignFinancialReview{}, fmt.Errorf("unmarshal risks: %w", err)
		}
	} else {
		rev.Risks = []string{}
	}

	if len(correctionsBytes) > 0 {
		if err := json.Unmarshal(correctionsBytes, &rev.RequiredCorrections); err != nil {
			return campaign.CampaignFinancialReview{}, fmt.Errorf("unmarshal corrections: %w", err)
		}
	} else {
		rev.RequiredCorrections = []string{}
	}

	if len(missingBytes) > 0 {
		if err := json.Unmarshal(missingBytes, &rev.MissingInformation); err != nil {
			return campaign.CampaignFinancialReview{}, fmt.Errorf("unmarshal missing info: %w", err)
		}
	} else {
		rev.MissingInformation = []string{}
	}

	return rev, nil
}

func validateCreateProposalCommand(cmd campaign.CreateProposalCommand) error {
	if strings.TrimSpace(cmd.OrganizationID) == "" {
		return fmt.Errorf("%w: organization ID is required", campaign.ErrInvalidInput)
	}
	if cmd.ConversationID <= 0 {
		return fmt.Errorf("%w: conversation ID must be positive", campaign.ErrInvalidInput)
	}
	if cmd.CreatedFromMessageID <= 0 {
		return fmt.Errorf("%w: created_from_message_id must be positive", campaign.ErrInvalidInput)
	}
	if cmd.TaskID <= 0 {
		return fmt.Errorf("%w: task_id must be positive", campaign.ErrInvalidInput)
	}
	if cmd.AttemptID <= 0 {
		return fmt.Errorf("%w: attempt_id must be positive", campaign.ErrInvalidInput)
	}
	if strings.TrimSpace(cmd.ToolCallID) == "" {
		return fmt.Errorf("%w: tool_call_id is required", campaign.ErrInvalidInput)
	}
	if strings.TrimSpace(cmd.CreatedByRoleID) == "" {
		return fmt.Errorf("%w: created_by_role_id is required", campaign.ErrInvalidInput)
	}
	if len(cmd.CreatedByRoleID) > campaign.MaxRoleIDLength {
		return fmt.Errorf("%w: created_by_role_id exceeds maximum length", campaign.ErrInvalidInput)
	}
	if strings.TrimSpace(cmd.IdempotencyKey) == "" {
		return fmt.Errorf("%w: idempotency key is required", campaign.ErrInvalidInput)
	}
	if len(cmd.IdempotencyKey) > campaign.MaxIdempotencyKeyLength {
		return fmt.Errorf("%w: idempotency key exceeds maximum length", campaign.ErrInvalidInput)
	}
	if !hashRegex.MatchString(cmd.CanonicalHash) {
		return fmt.Errorf("%w: canonical hash must be 64 lowercase hex characters", campaign.ErrInvalidInput)
	}
	if strings.TrimSpace(cmd.Title) == "" {
		return fmt.Errorf("%w: title is required", campaign.ErrInvalidInput)
	}
	if len(cmd.Title) > campaign.MaxTitleLength {
		return fmt.Errorf("%w: title exceeds maximum length", campaign.ErrInvalidInput)
	}
	if strings.TrimSpace(cmd.Goal) == "" {
		return fmt.Errorf("%w: goal is required", campaign.ErrInvalidInput)
	}
	if len(cmd.Goal) > campaign.MaxGoalLength {
		return fmt.Errorf("%w: goal exceeds maximum length", campaign.ErrInvalidInput)
	}
	if len(cmd.AcceptanceCriteria) == 0 {
		return fmt.Errorf("%w: at least one acceptance criterion is required", campaign.ErrInvalidInput)
	}
	if len(cmd.AcceptanceCriteria) > campaign.MaxAcceptanceCriteriaCount {
		return fmt.Errorf("%w: acceptance criteria count exceeds maximum", campaign.ErrInvalidInput)
	}
	for i, c := range cmd.AcceptanceCriteria {
		if strings.TrimSpace(c) == "" {
			return fmt.Errorf("%w: acceptance criterion %d cannot be empty", campaign.ErrInvalidInput, i)
		}
		if len(c) > campaign.MaxAcceptanceCriteriaItemBytes {
			return fmt.Errorf("%w: acceptance criterion %d exceeds maximum length", campaign.ErrInvalidInput, i)
		}
	}
	if len(cmd.Requirements) > campaign.MaxRequirementsCount {
		return fmt.Errorf("%w: requirements count exceeds maximum", campaign.ErrInvalidInput)
	}
	for i, r := range cmd.Requirements {
		if strings.TrimSpace(r.Key) == "" {
			return fmt.Errorf("%w: requirement %d key cannot be empty", campaign.ErrInvalidInput, i)
		}
		if len(r.Key) > campaign.MaxRequirementKeyBytes {
			return fmt.Errorf("%w: requirement %d key exceeds maximum length", campaign.ErrInvalidInput, i)
		}
		if strings.TrimSpace(r.Description) == "" {
			return fmt.Errorf("%w: requirement %d description cannot be empty", campaign.ErrInvalidInput, i)
		}
		if len(r.Description) > campaign.MaxRequirementDescBytes {
			return fmt.Errorf("%w: requirement %d description exceeds maximum length", campaign.ErrInvalidInput, i)
		}
	}
	if cmd.Budget != nil {
		if strings.TrimSpace(cmd.Budget.Currency) == "" || len(cmd.Budget.Currency) > 10 {
			return fmt.Errorf("%w: invalid budget currency", campaign.ErrInvalidInput)
		}
		if cmd.Budget.MaxAmount < 0 {
			return fmt.Errorf("%w: budget max_amount cannot be negative", campaign.ErrInvalidInput)
		}
	}
	if len(cmd.Assumptions) > campaign.MaxAssumptionsCount {
		return fmt.Errorf("%w: assumptions count exceeds maximum", campaign.ErrInvalidInput)
	}
	for i, a := range cmd.Assumptions {
		if len(a) > campaign.MaxAssumptionItemBytes {
			return fmt.Errorf("%w: assumption %d exceeds maximum length", campaign.ErrInvalidInput, i)
		}
	}
	if len(cmd.Risks) > campaign.MaxRisksCount {
		return fmt.Errorf("%w: risks count exceeds maximum", campaign.ErrInvalidInput)
	}
	for i, r := range cmd.Risks {
		if len(r) > campaign.MaxRiskItemBytes {
			return fmt.Errorf("%w: risk %d exceeds maximum length", campaign.ErrInvalidInput, i)
		}
	}
	if len(cmd.OpenQuestions) > campaign.MaxOpenQuestionsCount {
		return fmt.Errorf("%w: open questions count exceeds maximum", campaign.ErrInvalidInput)
	}
	for i, q := range cmd.OpenQuestions {
		if len(q) > campaign.MaxOpenQuestionItemBytes {
			return fmt.Errorf("%w: open question %d exceeds maximum length", campaign.ErrInvalidInput, i)
		}
	}
	return nil
}

func validateCreateReviewRequestCommand(cmd campaign.CreateReviewRequestCommand) error {
	if strings.TrimSpace(cmd.OrganizationID) == "" {
		return fmt.Errorf("%w: organization ID is required", campaign.ErrInvalidInput)
	}
	if cmd.ProposalID <= 0 {
		return fmt.Errorf("%w: proposal ID must be positive", campaign.ErrInvalidInput)
	}
	if !hashRegex.MatchString(cmd.ProposalCanonicalHash) {
		return fmt.Errorf("%w: proposal canonical hash must be 64 lowercase hex characters", campaign.ErrInvalidInput)
	}
	if strings.TrimSpace(cmd.RequestedByRoleID) == "" {
		return fmt.Errorf("%w: requested_by_role_id is required", campaign.ErrInvalidInput)
	}
	if strings.TrimSpace(cmd.ReviewerRoleID) == "" {
		return fmt.Errorf("%w: reviewer_role_id is required", campaign.ErrInvalidInput)
	}
	if cmd.ReviewTaskID <= 0 {
		return fmt.Errorf("%w: review_task_id must be positive", campaign.ErrInvalidInput)
	}
	if strings.TrimSpace(cmd.IdempotencyKey) == "" {
		return fmt.Errorf("%w: idempotency key is required", campaign.ErrInvalidInput)
	}
	if len(cmd.IdempotencyKey) > campaign.MaxIdempotencyKeyLength {
		return fmt.Errorf("%w: idempotency key exceeds maximum length", campaign.ErrInvalidInput)
	}
	return nil
}

func validateRecordFinancialReviewCommand(cmd campaign.RecordFinancialReviewCommand) error {
	if strings.TrimSpace(cmd.OrganizationID) == "" {
		return fmt.Errorf("%w: organization ID is required", campaign.ErrInvalidInput)
	}
	if cmd.ReviewRequestID <= 0 {
		return fmt.Errorf("%w: review_request_id must be positive", campaign.ErrInvalidInput)
	}
	if cmd.ProposalID <= 0 {
		return fmt.Errorf("%w: proposal_id must be positive", campaign.ErrInvalidInput)
	}
	if !hashRegex.MatchString(cmd.ProposalCanonicalHash) {
		return fmt.Errorf("%w: proposal canonical hash must be 64 lowercase hex characters", campaign.ErrInvalidInput)
	}
	if strings.TrimSpace(cmd.ReviewerRoleID) == "" {
		return fmt.Errorf("%w: reviewer_role_id is required", campaign.ErrInvalidInput)
	}
	if cmd.ReviewTaskID <= 0 {
		return fmt.Errorf("%w: review_task_id must be positive", campaign.ErrInvalidInput)
	}
	if cmd.ReviewAttemptID <= 0 {
		return fmt.Errorf("%w: review_attempt_id must be positive", campaign.ErrInvalidInput)
	}
	if !campaign.ValidVerdict(string(cmd.Verdict)) {
		return fmt.Errorf("%w: %q is not a valid verdict", campaign.ErrInvalidVerdict, cmd.Verdict)
	}
	if strings.TrimSpace(cmd.Summary) == "" {
		return fmt.Errorf("%w: summary is required", campaign.ErrInvalidInput)
	}
	if len(cmd.Summary) > campaign.MaxSummaryBytes {
		return fmt.Errorf("%w: summary exceeds maximum bytes", campaign.ErrInvalidInput)
	}
	if !hashRegex.MatchString(cmd.CanonicalHash) {
		return fmt.Errorf("%w: canonical hash must be 64 lowercase hex characters", campaign.ErrInvalidInput)
	}
	if len(cmd.Assumptions) > campaign.MaxAssumptionsCount {
		return fmt.Errorf("%w: assumptions count exceeds maximum", campaign.ErrInvalidInput)
	}
	if len(cmd.Risks) > campaign.MaxRisksCount {
		return fmt.Errorf("%w: risks count exceeds maximum", campaign.ErrInvalidInput)
	}
	if len(cmd.RequiredCorrections) > campaign.MaxCorrectionsCount {
		return fmt.Errorf("%w: required corrections count exceeds maximum", campaign.ErrInvalidInput)
	}
	if len(cmd.MissingInformation) > campaign.MaxMissingInfoCount {
		return fmt.Errorf("%w: missing information count exceeds maximum", campaign.ErrInvalidInput)
	}
	return nil
}
