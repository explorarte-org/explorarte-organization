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

// Store provides PostgreSQL-backed persistence for campaign proposals.
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
		return campaign.CampaignProposal{}, false, fmt.Errorf("%w: marshal acceptance criteria: %v", campaign.ErrInvalidInput, err)
	}
	reqs := cmd.Requirements
	if reqs == nil {
		reqs = []campaign.ProposalRequirement{}
	}
	reqsJSON, err := json.Marshal(reqs)
	if err != nil {
		return campaign.CampaignProposal{}, false, fmt.Errorf("%w: marshal requirements: %v", campaign.ErrInvalidInput, err)
	}
	var budgetJSON []byte
	if cmd.Budget != nil {
		budgetJSON, err = json.Marshal(cmd.Budget)
		if err != nil {
			return campaign.CampaignProposal{}, false, fmt.Errorf("%w: marshal budget: %v", campaign.ErrInvalidInput, err)
		}
	}
	assumptions := cmd.Assumptions
	if assumptions == nil {
		assumptions = []string{}
	}
	assumptionsJSON, err := json.Marshal(assumptions)
	if err != nil {
		return campaign.CampaignProposal{}, false, fmt.Errorf("%w: marshal assumptions: %v", campaign.ErrInvalidInput, err)
	}
	risks := cmd.Risks
	if risks == nil {
		risks = []string{}
	}
	risksJSON, err := json.Marshal(risks)
	if err != nil {
		return campaign.CampaignProposal{}, false, fmt.Errorf("%w: marshal risks: %v", campaign.ErrInvalidInput, err)
	}
	questions := cmd.OpenQuestions
	if questions == nil {
		questions = []string{}
	}
	questionsJSON, err := json.Marshal(questions)
	if err != nil {
		return campaign.CampaignProposal{}, false, fmt.Errorf("%w: marshal open questions: %v", campaign.ErrInvalidInput, err)
	}

	row := s.pool.QueryRow(ctx, `
		INSERT INTO campaign_proposals (
			organization_id, conversation_id, created_by_role_id, created_from_message_id,
			task_id, attempt_id, tool_call_id, status, title, goal,
			acceptance_criteria, requirements, budget, assumptions, risks, open_questions,
			financial_review_required, execution_started, idempotency_key, canonical_hash
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, 'draft', $8, $9,
			$10, $11, $12, $13, $14, $15,
			TRUE, FALSE, $16, $17
		)
		ON CONFLICT (organization_id, idempotency_key) DO NOTHING
		RETURNING id, organization_id, conversation_id, created_by_role_id, created_from_message_id,
		          task_id, attempt_id, tool_call_id, status, title, goal,
		          acceptance_criteria, requirements, budget, assumptions, risks, open_questions,
		          financial_review_required, execution_started, idempotency_key, canonical_hash,
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

	if existing.CanonicalHash != cmd.CanonicalHash {
		return campaign.CampaignProposal{}, false, fmt.Errorf("%w: idempotency key %q reused with different canonical hash",
			campaign.ErrIdempotencyConflict, cmd.IdempotencyKey)
	}

	return existing, true, nil
}

// GetProposal retrieves a campaign proposal by ID.
func (s *Store) GetProposal(ctx context.Context, organizationID string, id int64) (campaign.CampaignProposal, error) {
	if strings.TrimSpace(organizationID) == "" || id <= 0 {
		return campaign.CampaignProposal{}, fmt.Errorf("%w: invalid proposal id or organization", campaign.ErrInvalidInput)
	}
	row := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, conversation_id, created_by_role_id, created_from_message_id,
		       task_id, attempt_id, tool_call_id, status, title, goal,
		       acceptance_criteria, requirements, budget, assumptions, risks, open_questions,
		       financial_review_required, execution_started, idempotency_key, canonical_hash,
		       created_at, updated_at
		FROM campaign_proposals
		WHERE organization_id = $1 AND id = $2`, organizationID, id)
	proposal, err := scanProposal(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return campaign.CampaignProposal{}, fmt.Errorf("%w: proposal %d in organization %s", campaign.ErrProposalNotFound, id, organizationID)
	}
	return proposal, err
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
		       financial_review_required, execution_started, idempotency_key, canonical_hash,
		       created_at, updated_at
		FROM campaign_proposals
		WHERE organization_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2 OFFSET $3`, organizationID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var proposals []campaign.CampaignProposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		proposals = append(proposals, p)
	}
	if proposals == nil {
		proposals = []campaign.CampaignProposal{}
	}
	return proposals, rows.Err()
}

func (s *Store) getProposalByIdempotencyKey(ctx context.Context, organizationID, key string) (campaign.CampaignProposal, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, conversation_id, created_by_role_id, created_from_message_id,
		       task_id, attempt_id, tool_call_id, status, title, goal,
		       acceptance_criteria, requirements, budget, assumptions, risks, open_questions,
		       financial_review_required, execution_started, idempotency_key, canonical_hash,
		       created_at, updated_at
		FROM campaign_proposals
		WHERE organization_id = $1 AND idempotency_key = $2`, organizationID, key)
	proposal, err := scanProposal(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return campaign.CampaignProposal{}, fmt.Errorf("%w: proposal with key %q", campaign.ErrProposalNotFound, key)
	}
	return proposal, err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanProposal(scanner rowScanner) (campaign.CampaignProposal, error) {
	var (
		p               campaign.CampaignProposal
		statusStr       string
		criteriaBytes   []byte
		reqsBytes       []byte
		budgetBytes     []byte
		assumptionsBytes []byte
		risksBytes      []byte
		questionsBytes  []byte
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
			return campaign.CampaignProposal{}, fmt.Errorf("unmarshal acceptance criteria: %w", err)
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
		var b campaign.ProposalBudget
		if err := json.Unmarshal(budgetBytes, &b); err != nil {
			return campaign.CampaignProposal{}, fmt.Errorf("unmarshal budget: %w", err)
		}
		p.Budget = &b
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
			return campaign.CampaignProposal{}, fmt.Errorf("unmarshal open questions: %w", err)
		}
	} else {
		p.OpenQuestions = []string{}
	}

	return p, nil
}

func validateCreateProposalCommand(cmd campaign.CreateProposalCommand) error {
	switch {
	case strings.TrimSpace(cmd.OrganizationID) == "":
		return fmt.Errorf("%w: organization ID is required", campaign.ErrInvalidInput)
	case cmd.ConversationID <= 0:
		return fmt.Errorf("%w: conversation ID must be positive", campaign.ErrInvalidInput)
	case strings.TrimSpace(cmd.CreatedByRoleID) == "" || len(cmd.CreatedByRoleID) > campaign.MaxRoleIDLength:
		return fmt.Errorf("%w: created_by_role_id must be 1..%d chars", campaign.ErrInvalidInput, campaign.MaxRoleIDLength)
	case cmd.CreatedFromMessageID <= 0:
		return fmt.Errorf("%w: created_from_message_id must be positive", campaign.ErrInvalidInput)
	case cmd.TaskID <= 0:
		return fmt.Errorf("%w: task ID must be positive", campaign.ErrInvalidInput)
	case cmd.AttemptID <= 0:
		return fmt.Errorf("%w: attempt ID must be positive", campaign.ErrInvalidInput)
	case strings.TrimSpace(cmd.ToolCallID) == "" || len(cmd.ToolCallID) > campaign.MaxToolCallIDLength:
		return fmt.Errorf("%w: tool_call_id must be 1..%d chars", campaign.ErrInvalidInput, campaign.MaxToolCallIDLength)
	case strings.TrimSpace(cmd.IdempotencyKey) == "" || len(cmd.IdempotencyKey) > campaign.MaxIdempotencyKeyLength:
		return fmt.Errorf("%w: idempotency_key must be 1..%d chars", campaign.ErrInvalidInput, campaign.MaxIdempotencyKeyLength)
	case !hashRegex.MatchString(cmd.CanonicalHash):
		return fmt.Errorf("%w: canonical_hash must be a 64-char lowercase hex string", campaign.ErrInvalidInput)
	case strings.TrimSpace(cmd.Title) == "" || len(cmd.Title) > campaign.MaxTitleLength:
		return fmt.Errorf("%w: title must be 1..%d bytes", campaign.ErrInvalidInput, campaign.MaxTitleLength)
	case strings.TrimSpace(cmd.Goal) == "" || len(cmd.Goal) > campaign.MaxGoalLength:
		return fmt.Errorf("%w: goal must be 1..%d bytes", campaign.ErrInvalidInput, campaign.MaxGoalLength)
	case len(cmd.AcceptanceCriteria) == 0 || len(cmd.AcceptanceCriteria) > campaign.MaxAcceptanceCriteriaCount:
		return fmt.Errorf("%w: acceptance criteria count must be 1..%d", campaign.ErrInvalidInput, campaign.MaxAcceptanceCriteriaCount)
	case len(cmd.Requirements) > campaign.MaxRequirementsCount:
		return fmt.Errorf("%w: requirements count must be at most %d", campaign.ErrInvalidInput, campaign.MaxRequirementsCount)
	case len(cmd.Assumptions) > campaign.MaxAssumptionsCount:
		return fmt.Errorf("%w: assumptions count must be at most %d", campaign.ErrInvalidInput, campaign.MaxAssumptionsCount)
	case len(cmd.Risks) > campaign.MaxRisksCount:
		return fmt.Errorf("%w: risks count must be at most %d", campaign.ErrInvalidInput, campaign.MaxRisksCount)
	case len(cmd.OpenQuestions) > campaign.MaxOpenQuestionsCount:
		return fmt.Errorf("%w: open questions count must be at most %d", campaign.ErrInvalidInput, campaign.MaxOpenQuestionsCount)
	}

	for i, c := range cmd.AcceptanceCriteria {
		if strings.TrimSpace(c) == "" || len(c) > campaign.MaxAcceptanceCriteriaItemBytes {
			return fmt.Errorf("%w: acceptance criteria [%d] must be 1..%d bytes", campaign.ErrInvalidInput, i, campaign.MaxAcceptanceCriteriaItemBytes)
		}
	}
	for i, r := range cmd.Requirements {
		if strings.TrimSpace(r.Key) == "" || len(r.Key) > campaign.MaxRequirementKeyBytes {
			return fmt.Errorf("%w: requirement [%d] key must be 1..%d bytes", campaign.ErrInvalidInput, i, campaign.MaxRequirementKeyBytes)
		}
		if len(r.Description) > campaign.MaxRequirementDescBytes {
			return fmt.Errorf("%w: requirement [%d] description exceeds %d bytes", campaign.ErrInvalidInput, i, campaign.MaxRequirementDescBytes)
		}
	}
	if cmd.Budget != nil {
		if strings.TrimSpace(cmd.Budget.Currency) == "" || len(cmd.Budget.Currency) > 10 {
			return fmt.Errorf("%w: budget currency must be 1..10 chars", campaign.ErrInvalidInput)
		}
		if cmd.Budget.MaxAmount < 0 {
			return fmt.Errorf("%w: budget max_amount cannot be negative", campaign.ErrInvalidInput)
		}
	}

	return nil
}
