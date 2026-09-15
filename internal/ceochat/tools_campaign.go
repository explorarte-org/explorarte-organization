package ceochat

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
)

// CapabilityAuthorizer evaluates canonical capability permissions.
type CapabilityAuthorizer interface {
	Authorize(ctx context.Context, organizationID string, revisionID int64, roleID, capability string) error
}

// CampaignToolsConfig holds optional collaborators for campaign tools.
type CampaignToolsConfig struct {
	FinanceService  *campaign.FinanceService
	ApprovalService *campaign.ApprovalService
}

// CampaignToolsOption configures CampaignToolsConfig.
type CampaignToolsOption func(*CampaignToolsConfig)

// WithFinanceService configures a FinanceService for campaign financial review tools.
func WithFinanceService(svc *campaign.FinanceService) CampaignToolsOption {
	return func(c *CampaignToolsConfig) {
		c.FinanceService = svc
	}
}

// WithApprovalService configures an ApprovalService for campaign owner execution approval tools.
func WithApprovalService(svc *campaign.ApprovalService) CampaignToolsOption {
	return func(c *CampaignToolsConfig) {
		c.ApprovalService = svc
	}
}

// ProposeResultProjection is the bounded, non-sensitive projection returned
// to the model upon proposal creation.
type ProposeResultProjection struct {
	ProposalID              int64  `json:"proposal_id"`
	Status                  string `json:"status"`
	Title                   string `json:"title"`
	FinancialReviewRequired bool   `json:"financial_review_required"`
	ExecutionStarted        bool   `json:"execution_started"`
}

// RequestFinancialReviewResultProjection is the projection returned upon review request.
type RequestFinancialReviewResultProjection struct {
	ReviewRequestID       int64  `json:"review_request_id"`
	ProposalID            int64  `json:"proposal_id"`
	ProposalCanonicalHash string `json:"proposal_canonical_hash"`
	ReviewerRoleID        string `json:"reviewer_role_id"`
	ReviewTaskID          int64  `json:"review_task_id"`
	Status                string `json:"status"`
}

// FinancialReviewResultProjection is the projection returned by campaign.get_financial_review.
type FinancialReviewResultProjection struct {
	ReviewID              int64                          `json:"review_id"`
	ReviewRequestID       int64                          `json:"review_request_id"`
	ProposalID            int64                          `json:"proposal_id"`
	ProposalCanonicalHash string                         `json:"proposal_canonical_hash"`
	ReviewerRoleID        string                         `json:"reviewer_role_id"`
	Verdict               string                         `json:"verdict"`
	Summary               string                         `json:"summary"`
	RecommendedBudget     *campaign.BudgetRecommendation `json:"recommended_budget,omitempty"`
	EstimatedCost         *campaign.EstimatedCost        `json:"estimated_cost,omitempty"`
	Assumptions           []string                       `json:"assumptions"`
	Risks                 []string                       `json:"risks"`
	RequiredCorrections   []string                       `json:"required_corrections"`
	MissingInformation    []string                       `json:"missing_information"`
	CanonicalHash         string                         `json:"canonical_hash"`
	CreatedAt             string                         `json:"created_at"`
}

// ReviseProposalResultProjection is the bounded projection returned to the model upon proposal revision.
type ReviseProposalResultProjection struct {
	ProposalID       int64  `json:"proposal_id"`
	ParentProposalID int64  `json:"parent_proposal_id"`
	RevisionNumber   int    `json:"revision_number"`
	RootProposalID   int64  `json:"root_proposal_id"`
	Status           string `json:"status"`
	Title            string `json:"title"`
	CanonicalHash    string `json:"canonical_hash"`
}

// OwnerApprovalResultProjection is the bounded projection returned upon owner execution approval.
type OwnerApprovalResultProjection struct {
	ApprovalID                   int64                         `json:"approval_id"`
	ProposalID                   int64                         `json:"proposal_id"`
	ProposalCanonicalHash        string                        `json:"proposal_canonical_hash"`
	FinancialReviewID            int64                         `json:"financial_review_id"`
	FinancialReviewCanonicalHash string                        `json:"financial_review_canonical_hash"`
	ApprovedByRoleID             string                        `json:"approved_by_role_id"`
	Status                       string                        `json:"status"`
	ExecutionBudget              campaign.BudgetRecommendation `json:"execution_budget"`
	CanonicalHash                string                        `json:"canonical_hash"`
	CreatedAt                    string                        `json:"created_at"`
}

type proposeArgs struct {
	Title              string                         `json:"title"`
	Goal               string                         `json:"goal"`
	AcceptanceCriteria []string                       `json:"acceptance_criteria"`
	Requirements       []campaign.ProposalRequirement `json:"requirements"`
	Budget             *campaign.ProposalBudget       `json:"budget"`
	Assumptions        []string                       `json:"assumptions"`
	Risks              []string                       `json:"risks"`
	OpenQuestions      []string                       `json:"open_questions"`
}

type getProposalArgs struct {
	ProposalID int64 `json:"proposal_id"`
}

type listProposalsArgs struct {
	Limit int `json:"limit"`
}

type requestFinancialReviewArgs struct {
	ProposalID int64 `json:"proposal_id"`
}

type getFinancialReviewArgs struct {
	ReviewID        int64 `json:"review_id,omitempty"`
	ProposalID      int64 `json:"proposal_id,omitempty"`
	ReviewRequestID int64 `json:"review_request_id,omitempty"`
}

type reviseProposalArgs struct {
	ProposalID         int64                          `json:"proposal_id"`
	Title              string                         `json:"title"`
	Goal               string                         `json:"goal"`
	AcceptanceCriteria []string                       `json:"acceptance_criteria"`
	Requirements       []campaign.ProposalRequirement `json:"requirements"`
	Budget             *campaign.ProposalBudget       `json:"budget"`
	Assumptions        []string                       `json:"assumptions"`
	Risks              []string                       `json:"risks"`
	OpenQuestions      []string                       `json:"open_questions"`
}

type approveForExecutionArgs struct {
	ProposalID        int64 `json:"proposal_id"`
	FinancialReviewID int64 `json:"financial_review_id"`
}

type getOwnerApprovalArgs struct {
	ApprovalID int64 `json:"approval_id,omitempty"`
	ProposalID int64 `json:"proposal_id,omitempty"`
}

var (
	campaignProposeInputSchema = json.RawMessage(`{
		"type": "object",
		"properties": {
			"title": {"type": "string", "maxLength": 4000, "description": "Short, clear title of the campaign proposal."},
			"goal": {"type": "string", "maxLength": 16000, "description": "High-level goal and objective of the campaign."},
			"acceptance_criteria": {
				"type": "array",
				"items": {"type": "string", "maxLength": 2000},
				"description": "Measurable criteria defining successful completion (1 to 20 items)."
			},
			"requirements": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"key": {"type": "string", "maxLength": 200},
						"description": {"type": "string", "maxLength": 2000},
						"required": {"type": "boolean"}
					},
					"required": ["key", "description"]
				},
				"description": "Operational requirements for the campaign (up to 20 items)."
			},
			"budget": {
				"type": "object",
				"properties": {
					"currency": {"type": "string", "maxLength": 10, "description": "Currency code (e.g. USD)"},
					"max_amount": {"type": "number", "minimum": 0, "description": "Proposed maximum spending limit or estimate."},
					"source": {"type": "string", "enum": ["OWNER_LIMIT", "CEO_ESTIMATE", "UNKNOWN"]}
				},
				"required": ["currency", "max_amount"],
				"description": "Non-executing estimated or proposed spending constraint."
			},
			"assumptions": {
				"type": "array",
				"items": {"type": "string", "maxLength": 2000},
				"description": "Key assumptions behind this proposal (up to 20 items)."
			},
			"risks": {
				"type": "array",
				"items": {"type": "string", "maxLength": 2000},
				"description": "Identified risks and potential mitigations (up to 20 items)."
			},
			"open_questions": {
				"type": "array",
				"items": {"type": "string", "maxLength": 2000},
				"description": "Open questions requiring owner or team clarification (up to 20 items)."
			}
		},
		"required": ["title", "goal", "acceptance_criteria"],
		"additionalProperties": false
	}`)

	campaignGetProposalInputSchema = json.RawMessage(`{
		"type": "object",
		"properties": {
			"proposal_id": {"type": "integer", "minimum": 1, "description": "Durable ID of the campaign proposal to retrieve."}
		},
		"required": ["proposal_id"],
		"additionalProperties": false
	}`)

	campaignListProposalsInputSchema = json.RawMessage(`{
		"type": "object",
		"properties": {
			"limit": {"type": "integer", "minimum": 1, "maximum": 20, "description": "Maximum number of proposals to list (max 20)."}
		},
		"additionalProperties": false
	}`)

	campaignRequestFinancialReviewInputSchema = json.RawMessage(`{
		"type": "object",
		"properties": {
			"proposal_id": {"type": "integer", "minimum": 1, "description": "Durable ID of the draft campaign proposal to submit for financial review."}
		},
		"required": ["proposal_id"],
		"additionalProperties": false
	}`)

	campaignGetFinancialReviewInputSchema = json.RawMessage(`{
		"type": "object",
		"properties": {
			"review_id": {"type": "integer", "minimum": 1, "description": "Durable ID of the financial review."},
			"proposal_id": {"type": "integer", "minimum": 1, "description": "Durable ID of the proposal whose financial review to retrieve."},
			"review_request_id": {"type": "integer", "minimum": 1, "description": "Durable ID of the review request."}
		},
		"additionalProperties": false
	}`)

	campaignReviseProposalInputSchema = json.RawMessage(`{
		"type": "object",
		"properties": {
			"proposal_id": {"type": "integer", "minimum": 1, "description": "Durable ID of the campaign proposal to revise."},
			"title": {"type": "string", "maxLength": 4000, "description": "Short, clear title of the revised campaign proposal."},
			"goal": {"type": "string", "maxLength": 16000, "description": "High-level goal and objective of the revised campaign."},
			"acceptance_criteria": {
				"type": "array",
				"items": {"type": "string", "maxLength": 2000},
				"description": "Measurable criteria defining successful completion (1 to 20 items)."
			},
			"requirements": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"key": {"type": "string", "maxLength": 200},
						"description": {"type": "string", "maxLength": 2000},
						"required": {"type": "boolean"}
					},
					"required": ["key", "description"]
				},
				"description": "Operational requirements for the revised campaign (up to 20 items)."
			},
			"budget": {
				"type": "object",
				"properties": {
					"currency": {"type": "string", "maxLength": 10, "description": "Currency code (e.g. USD)"},
					"max_amount": {"type": "number", "minimum": 0, "description": "Proposed maximum spending limit or estimate."},
					"source": {"type": "string", "enum": ["OWNER_LIMIT", "CEO_ESTIMATE", "UNKNOWN"]}
				},
				"required": ["currency", "max_amount"],
				"description": "Non-executing estimated or proposed spending constraint."
			},
			"assumptions": {
				"type": "array",
				"items": {"type": "string", "maxLength": 2000},
				"description": "Key assumptions behind this proposal revision (up to 20 items)."
			},
			"risks": {
				"type": "array",
				"items": {"type": "string", "maxLength": 2000},
				"description": "Identified risks and potential mitigations (up to 20 items)."
			},
			"open_questions": {
				"type": "array",
				"items": {"type": "string", "maxLength": 2000},
				"description": "Open questions requiring owner or team clarification (up to 20 items)."
			}
		},
		"required": ["proposal_id", "title", "goal", "acceptance_criteria"],
		"additionalProperties": false
	}`)

	campaignApproveForExecutionInputSchema = json.RawMessage(`{
		"type": "object",
		"properties": {
			"proposal_id": {"type": "integer", "minimum": 1, "description": "Durable ID of the campaign proposal to approve for execution."},
			"financial_review_id": {"type": "integer", "minimum": 1, "description": "Durable ID of the recommended financial review."}
		},
		"required": ["proposal_id", "financial_review_id"],
		"additionalProperties": false
	}`)

	campaignGetOwnerApprovalInputSchema = json.RawMessage(`{
		"type": "object",
		"properties": {
			"approval_id": {"type": "integer", "minimum": 1, "description": "Durable ID of the owner execution approval."},
			"proposal_id": {"type": "integer", "minimum": 1, "description": "Durable ID of the proposal whose approval to retrieve."}
		},
		"additionalProperties": false
	}`)
)

// RegisterCampaignTools registers campaign.propose (mutating), campaign.get_proposal (read_only),
// campaign.list_proposals (read_only), campaign.request_financial_review (mutating),
// and campaign.get_financial_review (read_only) into the provided ToolRegistry.
func RegisterCampaignTools(registry *ToolRegistry, organizationID string, store campaign.Store, authorizer CapabilityAuthorizer, opts ...CampaignToolsOption) error {
	cfg := CampaignToolsConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}

	// 1. campaign.propose (MUTATING)
	proposeDesc := ToolDescriptor{
		ID:           "campaign.propose",
		Version:      "v1",
		Description:  "Drafts a durable campaign proposal for owner review. Does NOT execute, commit resources, or authorize spend.",
		InputSchema:  campaignProposeInputSchema,
		Access:       AccessMutating,
		Effect:       ToolEffectWrite,
		RequiredRole: CEORoleID,
		Limits: ToolLimits{
			MaxRows:        1,
			MaxResultBytes: 16384,
			Timeout:        defaultToolTimeout,
		},
		DataClass: DataClassInternal,
	}

	proposeValidator := func(raw json.RawMessage) error {
		var args proposeArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return fmt.Errorf("%w: decode campaign.propose args: %v", ErrInvalidInput, err)
		}
		if strings.TrimSpace(args.Title) == "" || len(args.Title) > campaign.MaxTitleLength {
			return fmt.Errorf("%w: title must be non-empty and <= %d chars", ErrInvalidInput, campaign.MaxTitleLength)
		}
		if strings.TrimSpace(args.Goal) == "" || len(args.Goal) > campaign.MaxGoalLength {
			return fmt.Errorf("%w: goal must be non-empty and <= %d chars", ErrInvalidInput, campaign.MaxGoalLength)
		}
		if len(args.AcceptanceCriteria) == 0 || len(args.AcceptanceCriteria) > campaign.MaxAcceptanceCriteriaCount {
			return fmt.Errorf("%w: acceptance_criteria count must be between 1 and %d", ErrInvalidInput, campaign.MaxAcceptanceCriteriaCount)
		}
		for i, ac := range args.AcceptanceCriteria {
			if strings.TrimSpace(ac) == "" || len(ac) > campaign.MaxAcceptanceCriteriaItemBytes {
				return fmt.Errorf("%w: acceptance_criteria[%d] must be non-empty and <= %d bytes", ErrInvalidInput, i, campaign.MaxAcceptanceCriteriaItemBytes)
			}
		}
		if len(args.Requirements) > campaign.MaxRequirementsCount {
			return fmt.Errorf("%w: requirements count exceeds %d", ErrInvalidInput, campaign.MaxRequirementsCount)
		}
		for i, req := range args.Requirements {
			if strings.TrimSpace(req.Key) == "" || len(req.Key) > campaign.MaxRequirementKeyBytes {
				return fmt.Errorf("%w: requirements[%d].key invalid", ErrInvalidInput, i)
			}
			if strings.TrimSpace(req.Description) == "" || len(req.Description) > campaign.MaxRequirementDescBytes {
				return fmt.Errorf("%w: requirements[%d].description invalid", ErrInvalidInput, i)
			}
		}
		if len(args.Assumptions) > campaign.MaxAssumptionsCount {
			return fmt.Errorf("%w: assumptions count exceeds %d", ErrInvalidInput, campaign.MaxAssumptionsCount)
		}
		if len(args.Risks) > campaign.MaxRisksCount {
			return fmt.Errorf("%w: risks count exceeds %d", ErrInvalidInput, campaign.MaxRisksCount)
		}
		if len(args.OpenQuestions) > campaign.MaxOpenQuestionsCount {
			return fmt.Errorf("%w: open_questions count exceeds %d", ErrInvalidInput, campaign.MaxOpenQuestionsCount)
		}
		if args.Budget != nil {
			if strings.TrimSpace(args.Budget.Currency) == "" || len(args.Budget.Currency) > 10 {
				return fmt.Errorf("%w: invalid budget currency", ErrInvalidInput)
			}
			if args.Budget.MaxAmount < 0 {
				return fmt.Errorf("%w: budget max_amount cannot be negative", ErrInvalidInput)
			}
		}
		return nil
	}

	proposeHandler := func(ctx context.Context, actorRoleID string, raw json.RawMessage) (json.RawMessage, error) {
		turnCtx, ok := TurnContextFrom(ctx)
		if !ok || strings.TrimSpace(turnCtx.ActorRoleID) == "" {
			return nil, fmt.Errorf("%w: missing authorized turn context", ErrUnauthorizedActor)
		}
		toolCallCtx, ok := ToolCallContextFrom(ctx)
		if !ok || strings.TrimSpace(toolCallCtx.ToolCallID) == "" {
			return nil, fmt.Errorf("%w: missing tool call context", ErrUnauthorizedActor)
		}

		if authorizer != nil {
			if err := authorizer.Authorize(ctx, turnCtx.OrganizationID, turnCtx.OrganizationRevisionID, turnCtx.ActorRoleID, "campaign.proposal.create"); err != nil {
				return nil, fmt.Errorf("%w: actor %q lacks campaign.proposal.create capability: %v", ErrUnauthorizedActor, turnCtx.ActorRoleID, err)
			}
		}

		var args proposeArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}

		if args.Budget != nil && args.Budget.Source == "" {
			args.Budget.Source = campaign.BudgetSourceCEOEstimate
		}

		canonicalHash, err := campaign.ComputeCanonicalHash(campaign.CanonicalPayload{
			Title:              args.Title,
			Goal:               args.Goal,
			AcceptanceCriteria: args.AcceptanceCriteria,
			Requirements:       args.Requirements,
			Budget:             args.Budget,
			Assumptions:        args.Assumptions,
			Risks:              args.Risks,
			OpenQuestions:      args.OpenQuestions,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: compute canonical hash: %v", ErrInvalidInput, err)
		}

		idempotencyKey := fmt.Sprintf("cprop:%d:%d:%s", turnCtx.ConversationID, turnCtx.TaskID, toolCallCtx.ToolCallID)
		if len(idempotencyKey) > campaign.MaxIdempotencyKeyLength {
			h := sha256.Sum256([]byte(toolCallCtx.ToolCallID))
			idempotencyKey = fmt.Sprintf("cprop:%d:%d:%x", turnCtx.ConversationID, turnCtx.TaskID, h[:])
		}

		proposal, _, err := store.CreateProposal(ctx, campaign.CreateProposalCommand{
			OrganizationID:       turnCtx.OrganizationID,
			ConversationID:       turnCtx.ConversationID,
			CreatedByRoleID:      turnCtx.ActorRoleID,
			CreatedFromMessageID: turnCtx.OwnerMessageID,
			TaskID:               turnCtx.TaskID,
			AttemptID:            turnCtx.AttemptID,
			ToolCallID:           toolCallCtx.ToolCallID,
			IdempotencyKey:       idempotencyKey,
			CanonicalHash:        canonicalHash,
			Title:                args.Title,
			Goal:                 args.Goal,
			AcceptanceCriteria:   args.AcceptanceCriteria,
			Requirements:         args.Requirements,
			Budget:               args.Budget,
			Assumptions:          args.Assumptions,
			Risks:                args.Risks,
			OpenQuestions:        args.OpenQuestions,
		})
		if err != nil {
			return nil, err
		}

		projection := ProposeResultProjection{
			ProposalID:              proposal.ID,
			Status:                  string(proposal.Status),
			Title:                   proposal.Title,
			FinancialReviewRequired: proposal.FinancialReviewRequired,
			ExecutionStarted:        proposal.ExecutionStarted,
		}
		return json.Marshal(projection)
	}

	if err := registry.Register(proposeDesc, proposeValidator, proposeHandler); err != nil {
		return fmt.Errorf("register campaign.propose: %w", err)
	}

	// 2. campaign.get_proposal (READ ONLY)
	getDesc := ToolDescriptor{
		ID:           "campaign.get_proposal",
		Version:      "v1",
		Description:  "Retrieves a previously created campaign proposal by its durable proposal ID.",
		InputSchema:  campaignGetProposalInputSchema,
		Access:       AccessReadOnly,
		Effect:       ToolEffectRead,
		RequiredRole: CEORoleID,
		Limits: ToolLimits{
			MaxRows:        1,
			MaxResultBytes: 16384,
			Timeout:        defaultToolTimeout,
		},
		DataClass: DataClassInternal,
	}

	getValidator := func(raw json.RawMessage) error {
		var args getProposalArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return fmt.Errorf("%w: decode campaign.get_proposal args: %v", ErrInvalidInput, err)
		}
		if args.ProposalID <= 0 {
			return fmt.Errorf("%w: proposal_id must be positive", ErrInvalidInput)
		}
		return nil
	}

	getHandler := func(ctx context.Context, actorRoleID string, raw json.RawMessage) (json.RawMessage, error) {
		turnCtx, ok := TurnContextFrom(ctx)
		orgID := organizationID
		if ok && turnCtx.OrganizationID != "" {
			orgID = turnCtx.OrganizationID
		}
		if authorizer != nil && ok && turnCtx.ActorRoleID != "" {
			if err := authorizer.Authorize(ctx, orgID, turnCtx.OrganizationRevisionID, turnCtx.ActorRoleID, "campaign.proposal.read"); err != nil {
				return nil, fmt.Errorf("%w: actor %q lacks campaign.proposal.read capability: %v", ErrUnauthorizedActor, turnCtx.ActorRoleID, err)
			}
		}

		var args getProposalArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}

		proposal, err := store.GetProposal(ctx, orgID, args.ProposalID)
		if err != nil {
			return nil, err
		}
		return json.Marshal(proposal)
	}

	if err := registry.Register(getDesc, getValidator, getHandler); err != nil {
		return fmt.Errorf("register campaign.get_proposal: %w", err)
	}

	// 3. campaign.list_proposals (READ ONLY)
	listDesc := ToolDescriptor{
		ID:           "campaign.list_proposals",
		Version:      "v1",
		Description:  "Lists recently created campaign proposals for this organization, newest first.",
		InputSchema:  campaignListProposalsInputSchema,
		Access:       AccessReadOnly,
		Effect:       ToolEffectRead,
		RequiredRole: CEORoleID,
		Limits: ToolLimits{
			MaxRows:        20,
			MaxResultBytes: 16384,
			Timeout:        defaultToolTimeout,
		},
		DataClass: DataClassInternal,
	}

	listValidator := func(raw json.RawMessage) error {
		var args listProposalsArgs
		if len(raw) > 0 && string(raw) != "{}" {
			if err := json.Unmarshal(raw, &args); err != nil {
				return fmt.Errorf("%w: decode campaign.list_proposals args: %v", ErrInvalidInput, err)
			}
			if args.Limit < 0 || args.Limit > 20 {
				return fmt.Errorf("%w: limit must be between 1 and 20", ErrInvalidInput)
			}
		}
		return nil
	}

	listHandler := func(ctx context.Context, actorRoleID string, raw json.RawMessage) (json.RawMessage, error) {
		turnCtx, ok := TurnContextFrom(ctx)
		orgID := organizationID
		if ok && turnCtx.OrganizationID != "" {
			orgID = turnCtx.OrganizationID
		}
		if authorizer != nil && ok && turnCtx.ActorRoleID != "" {
			if err := authorizer.Authorize(ctx, orgID, turnCtx.OrganizationRevisionID, turnCtx.ActorRoleID, "campaign.proposal.read"); err != nil {
				return nil, fmt.Errorf("%w: actor %q lacks campaign.proposal.read capability: %v", ErrUnauthorizedActor, turnCtx.ActorRoleID, err)
			}
		}

		var args listProposalsArgs
		if len(raw) > 0 && string(raw) != "{}" {
			_ = json.Unmarshal(raw, &args)
		}
		limit := args.Limit
		if limit <= 0 {
			limit = 10
		}

		proposals, err := store.ListProposals(ctx, orgID, limit, 0)
		if err != nil {
			return nil, err
		}
		return json.Marshal(proposals)
	}

	if err := registry.Register(listDesc, listValidator, listHandler); err != nil {
		return fmt.Errorf("register campaign.list_proposals: %w", err)
	}

	// 4. campaign.request_financial_review (MUTATING / WRITE)
	reqFinDesc := ToolDescriptor{
		ID:           "campaign.request_financial_review",
		Version:      "v1",
		Description:  "Submits a draft campaign proposal to the canonical Finance role for formal financial review. Does NOT execute the campaign.",
		InputSchema:  campaignRequestFinancialReviewInputSchema,
		Access:       AccessMutating,
		Effect:       ToolEffectWrite,
		RequiredRole: CEORoleID,
		Limits: ToolLimits{
			MaxRows:        1,
			MaxResultBytes: 16384,
			Timeout:        defaultToolTimeout,
		},
		DataClass: DataClassInternal,
	}

	reqFinValidator := func(raw json.RawMessage) error {
		var args requestFinancialReviewArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return fmt.Errorf("%w: decode campaign.request_financial_review args: %v", ErrInvalidInput, err)
		}
		if args.ProposalID <= 0 {
			return fmt.Errorf("%w: proposal_id must be positive", ErrInvalidInput)
		}
		return nil
	}

	reqFinHandler := func(ctx context.Context, actorRoleID string, raw json.RawMessage) (json.RawMessage, error) {
		turnCtx, ok := TurnContextFrom(ctx)
		if !ok || strings.TrimSpace(turnCtx.ActorRoleID) == "" {
			return nil, fmt.Errorf("%w: missing authorized turn context", ErrUnauthorizedActor)
		}
		toolCallCtx, ok := ToolCallContextFrom(ctx)
		if !ok || strings.TrimSpace(toolCallCtx.ToolCallID) == "" {
			return nil, fmt.Errorf("%w: missing tool call context", ErrUnauthorizedActor)
		}

		if authorizer != nil {
			if err := authorizer.Authorize(ctx, turnCtx.OrganizationID, turnCtx.OrganizationRevisionID, turnCtx.ActorRoleID, campaign.CapabilityFinancialReviewRequest); err != nil {
				return nil, fmt.Errorf("%w: actor %q lacks %s capability: %v", ErrUnauthorizedActor, turnCtx.ActorRoleID, campaign.CapabilityFinancialReviewRequest, err)
			}
		}

		var args requestFinancialReviewArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}

		if cfg.FinanceService == nil {
			return nil, fmt.Errorf("%w: finance service not configured", ErrInvalidInput)
		}

		reviewReq, task, _, err := cfg.FinanceService.RequestReview(ctx, campaign.RequestReviewParams{
			OrganizationID:              turnCtx.OrganizationID,
			OrganizationRevisionID:      turnCtx.OrganizationRevisionID,
			ProposalID:                  args.ProposalID,
			RequestedByRoleID:           turnCtx.ActorRoleID,
			RequestedFromConversationID: turnCtx.ConversationID,
			RequestedFromMessageID:      turnCtx.OwnerMessageID,
			RequestedFromTaskID:         turnCtx.TaskID,
			ToolCallID:                  toolCallCtx.ToolCallID,
		})
		if err != nil {
			return nil, err
		}

		projection := RequestFinancialReviewResultProjection{
			ReviewRequestID:       reviewReq.ID,
			ProposalID:            reviewReq.ProposalID,
			ProposalCanonicalHash: reviewReq.ProposalCanonicalHash,
			ReviewerRoleID:        reviewReq.ReviewerRoleID,
			ReviewTaskID:          task.ID,
			Status:                string(reviewReq.Status),
		}
		return json.Marshal(projection)
	}

	if err := registry.Register(reqFinDesc, reqFinValidator, reqFinHandler); err != nil {
		return fmt.Errorf("register campaign.request_financial_review: %w", err)
	}

	// 5. campaign.get_financial_review (READ ONLY)
	getFinDesc := ToolDescriptor{
		ID:           "campaign.get_financial_review",
		Version:      "v1",
		Description:  "Retrieves the completed immutable financial review recommendation for a campaign proposal.",
		InputSchema:  campaignGetFinancialReviewInputSchema,
		Access:       AccessReadOnly,
		Effect:       ToolEffectRead,
		RequiredRole: CEORoleID,
		Limits: ToolLimits{
			MaxRows:        1,
			MaxResultBytes: 16384,
			Timeout:        defaultToolTimeout,
		},
		DataClass: DataClassInternal,
	}

	getFinValidator := func(raw json.RawMessage) error {
		var args getFinancialReviewArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return fmt.Errorf("%w: decode campaign.get_financial_review args: %v", ErrInvalidInput, err)
		}
		if args.ReviewID <= 0 && args.ProposalID <= 0 && args.ReviewRequestID <= 0 {
			return fmt.Errorf("%w: at least one of review_id, proposal_id, or review_request_id must be provided", ErrInvalidInput)
		}
		return nil
	}

	getFinHandler := func(ctx context.Context, actorRoleID string, raw json.RawMessage) (json.RawMessage, error) {
		turnCtx, ok := TurnContextFrom(ctx)
		orgID := organizationID
		if ok && turnCtx.OrganizationID != "" {
			orgID = turnCtx.OrganizationID
		}
		if authorizer != nil && ok && turnCtx.ActorRoleID != "" {
			if err := authorizer.Authorize(ctx, orgID, turnCtx.OrganizationRevisionID, turnCtx.ActorRoleID, campaign.CapabilityFinancialReviewRead); err != nil {
				return nil, fmt.Errorf("%w: actor %q lacks %s capability: %v", ErrUnauthorizedActor, turnCtx.ActorRoleID, campaign.CapabilityFinancialReviewRead, err)
			}
		}

		var args getFinancialReviewArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}

		var rev campaign.CampaignFinancialReview
		var err error
		if args.ReviewID > 0 {
			rev, err = store.GetFinancialReview(ctx, orgID, args.ReviewID)
		} else if args.ReviewRequestID > 0 {
			rev, err = store.GetFinancialReviewByRequestID(ctx, orgID, args.ReviewRequestID)
		} else if args.ProposalID > 0 {
			rev, err = store.GetLatestFinancialReviewForProposal(ctx, orgID, args.ProposalID)
		} else {
			return nil, fmt.Errorf("%w: query identifier required", ErrInvalidInput)
		}
		if err != nil {
			return nil, err
		}

		projection := FinancialReviewResultProjection{
			ReviewID:              rev.ID,
			ReviewRequestID:       rev.ReviewRequestID,
			ProposalID:            rev.ProposalID,
			ProposalCanonicalHash: rev.ProposalCanonicalHash,
			ReviewerRoleID:        rev.ReviewerRoleID,
			Verdict:               string(rev.Verdict),
			Summary:               rev.Summary,
			RecommendedBudget:     rev.RecommendedBudget,
			EstimatedCost:         rev.EstimatedCost,
			Assumptions:           rev.Assumptions,
			Risks:                 rev.Risks,
			RequiredCorrections:   rev.RequiredCorrections,
			MissingInformation:    rev.MissingInformation,
			CanonicalHash:         rev.CanonicalHash,
			CreatedAt:             rev.CreatedAt.Format(time.RFC3339),
		}
		return json.Marshal(projection)
	}

	if err := registry.Register(getFinDesc, getFinValidator, getFinHandler); err != nil {
		return fmt.Errorf("register campaign.get_financial_review: %w", err)
	}

	// 6. campaign.revise_proposal (MUTATING)
	reviseDesc := ToolDescriptor{
		ID:           "campaign.revise_proposal",
		Version:      "v1",
		Description:  "Creates an immutable new revision of an existing campaign proposal with lineage. Does NOT execute or authorize spend.",
		InputSchema:  campaignReviseProposalInputSchema,
		Access:       AccessMutating,
		Effect:       ToolEffectWrite,
		RequiredRole: CEORoleID,
		Limits: ToolLimits{
			MaxRows:        1,
			MaxResultBytes: 16384,
			Timeout:        defaultToolTimeout,
		},
		DataClass: DataClassInternal,
	}

	reviseValidator := func(raw json.RawMessage) error {
		var args reviseProposalArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		if args.ProposalID <= 0 {
			return fmt.Errorf("%w: proposal_id must be positive", ErrInvalidInput)
		}
		if strings.TrimSpace(args.Title) == "" {
			return fmt.Errorf("%w: title cannot be empty", ErrInvalidInput)
		}
		if strings.TrimSpace(args.Goal) == "" {
			return fmt.Errorf("%w: goal cannot be empty", ErrInvalidInput)
		}
		if len(args.AcceptanceCriteria) == 0 {
			return fmt.Errorf("%w: at least one acceptance criterion is required", ErrInvalidInput)
		}
		return nil
	}

	reviseHandler := func(ctx context.Context, actorRoleID string, raw json.RawMessage) (json.RawMessage, error) {
		turnCtx, ok := TurnContextFrom(ctx)
		if !ok {
			return nil, fmt.Errorf("%w: missing turn context", ErrInvalidInput)
		}
		toolCallCtx, _ := ToolCallContextFrom(ctx)

		if authorizer != nil {
			if err := authorizer.Authorize(ctx, turnCtx.OrganizationID, turnCtx.OrganizationRevisionID, turnCtx.ActorRoleID, campaign.CapabilityProposalRevise); err != nil {
				return nil, fmt.Errorf("%w: actor %q lacks %s capability: %v", ErrUnauthorizedActor, turnCtx.ActorRoleID, campaign.CapabilityProposalRevise, err)
			}
		}

		var args reviseProposalArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}

		if args.Budget != nil && args.Budget.Source == "" {
			args.Budget.Source = campaign.BudgetSourceCEOEstimate
		}

		canonicalHash, err := campaign.ComputeCanonicalHash(campaign.CanonicalPayload{
			Title:              args.Title,
			Goal:               args.Goal,
			AcceptanceCriteria: args.AcceptanceCriteria,
			Requirements:       args.Requirements,
			Budget:             args.Budget,
			Assumptions:        args.Assumptions,
			Risks:              args.Risks,
			OpenQuestions:      args.OpenQuestions,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: compute canonical hash: %v", ErrInvalidInput, err)
		}

		idempotencyKey := fmt.Sprintf("crev:%d:%d:%s", turnCtx.ConversationID, turnCtx.TaskID, toolCallCtx.ToolCallID)
		if len(idempotencyKey) > campaign.MaxIdempotencyKeyLength {
			h := sha256.Sum256([]byte(toolCallCtx.ToolCallID))
			idempotencyKey = fmt.Sprintf("crev:%d:%d:%x", turnCtx.ConversationID, turnCtx.TaskID, h[:])
		}

		rev, _, err := store.CreateRevision(ctx, campaign.CreateRevisionCommand{
			OrganizationID:       turnCtx.OrganizationID,
			ParentProposalID:     args.ProposalID,
			ConversationID:       turnCtx.ConversationID,
			CreatedByRoleID:      turnCtx.ActorRoleID,
			CreatedFromMessageID: turnCtx.OwnerMessageID,
			TaskID:               turnCtx.TaskID,
			AttemptID:            turnCtx.AttemptID,
			ToolCallID:           toolCallCtx.ToolCallID,
			IdempotencyKey:       idempotencyKey,
			CanonicalHash:        canonicalHash,
			Title:                args.Title,
			Goal:                 args.Goal,
			AcceptanceCriteria:   args.AcceptanceCriteria,
			Requirements:         args.Requirements,
			Budget:               args.Budget,
			Assumptions:          args.Assumptions,
			Risks:                args.Risks,
			OpenQuestions:        args.OpenQuestions,
		})
		if err != nil {
			return nil, err
		}

		parentID := int64(0)
		if rev.ParentProposalID != nil {
			parentID = *rev.ParentProposalID
		}
		rootID := int64(0)
		if rev.RootProposalID != nil {
			rootID = *rev.RootProposalID
		}

		projection := ReviseProposalResultProjection{
			ProposalID:       rev.ID,
			ParentProposalID: parentID,
			RevisionNumber:   rev.RevisionNumber,
			RootProposalID:   rootID,
			Status:           string(rev.Status),
			Title:            rev.Title,
			CanonicalHash:    rev.CanonicalHash,
		}
		return json.Marshal(projection)
	}

	if err := registry.Register(reviseDesc, reviseValidator, reviseHandler); err != nil {
		return fmt.Errorf("register campaign.revise_proposal: %w", err)
	}

	// 7. campaign.approve_for_execution (MUTATING)
	approveDesc := ToolDescriptor{
		ID:           "campaign.approve_for_execution",
		Version:      "v1",
		Description:  "Records durable owner execution approval for an exact campaign proposal and recommended financial review tuple. Does NOT execute or call Executive.Submit.",
		InputSchema:  campaignApproveForExecutionInputSchema,
		Access:       AccessMutating,
		Effect:       ToolEffectWrite,
		RequiredRole: CEORoleID,
		Limits: ToolLimits{
			MaxRows:        1,
			MaxResultBytes: 16384,
			Timeout:        defaultToolTimeout,
		},
		DataClass: DataClassInternal,
	}

	approveValidator := func(raw json.RawMessage) error {
		var args approveForExecutionArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		if args.ProposalID <= 0 {
			return fmt.Errorf("%w: proposal_id must be positive", ErrInvalidInput)
		}
		if args.FinancialReviewID <= 0 {
			return fmt.Errorf("%w: financial_review_id must be positive", ErrInvalidInput)
		}
		return nil
	}

	approveHandler := func(ctx context.Context, actorRoleID string, raw json.RawMessage) (json.RawMessage, error) {
		turnCtx, ok := TurnContextFrom(ctx)
		if !ok {
			return nil, fmt.Errorf("%w: missing turn context", ErrInvalidInput)
		}
		toolCallCtx, _ := ToolCallContextFrom(ctx)

		if cfg.ApprovalService == nil {
			return nil, fmt.Errorf("%w: approval service is not configured", ErrInvalidInput)
		}

		// Strictly enforce owner authority:
		// 1. OwnerRoleID must be present and non-empty.
		// 2. The owner role must possess campaign.owner_approval.create capability.
		// 3. The conversational model/CEO cannot self-approve.
		ownerRoleID := turnCtx.OwnerRoleID
		if strings.TrimSpace(ownerRoleID) == "" {
			return nil, fmt.Errorf("%w: turn has no verified owner identity", ErrUnauthorizedActor)
		}

		if authorizer != nil {
			if err := authorizer.Authorize(ctx, turnCtx.OrganizationID, turnCtx.OrganizationRevisionID, ownerRoleID, campaign.CapabilityOwnerApprovalCreate); err != nil {
				return nil, fmt.Errorf("%w: owner role %q lacks %s capability: %v", ErrUnauthorizedActor, ownerRoleID, campaign.CapabilityOwnerApprovalCreate, err)
			}
		}

		var args approveForExecutionArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}

		appr, _, err := cfg.ApprovalService.ApproveForExecution(ctx, campaign.ApproveParams{
			OrganizationID:    turnCtx.OrganizationID,
			RevisionID:        turnCtx.OrganizationRevisionID,
			ProposalID:        args.ProposalID,
			FinancialReviewID: args.FinancialReviewID,
			ApprovedByRoleID:  ownerRoleID,
			ConversationID:    turnCtx.ConversationID,
			MessageID:         turnCtx.OwnerMessageID,
			TurnTaskID:        turnCtx.TaskID,
			ToolCallID:        toolCallCtx.ToolCallID,
		})
		if err != nil {
			return nil, err
		}

		projection := OwnerApprovalResultProjection{
			ApprovalID:                   appr.ID,
			ProposalID:                   appr.ProposalID,
			ProposalCanonicalHash:        appr.ProposalCanonicalHash,
			FinancialReviewID:            appr.FinancialReviewID,
			FinancialReviewCanonicalHash: appr.FinancialReviewCanonicalHash,
			ApprovedByRoleID:             appr.ApprovedByRoleID,
			Status:                       string(appr.Status),
			ExecutionBudget:              appr.ExecutionBudget,
			CanonicalHash:                appr.CanonicalHash,
			CreatedAt:                    appr.CreatedAt.Format(time.RFC3339),
		}
		return json.Marshal(projection)
	}

	if err := registry.Register(approveDesc, approveValidator, approveHandler); err != nil {
		return fmt.Errorf("register campaign.approve_for_execution: %w", err)
	}

	// 8. campaign.get_owner_approval (READ ONLY)
	getApprDesc := ToolDescriptor{
		ID:           "campaign.get_owner_approval",
		Version:      "v1",
		Description:  "Retrieves an existing durable owner execution approval by approval ID or proposal ID.",
		InputSchema:  campaignGetOwnerApprovalInputSchema,
		Access:       AccessReadOnly,
		Effect:       ToolEffectRead,
		RequiredRole: CEORoleID,
		Limits: ToolLimits{
			MaxRows:        1,
			MaxResultBytes: 16384,
			Timeout:        defaultToolTimeout,
		},
		DataClass: DataClassInternal,
	}

	getApprValidator := func(raw json.RawMessage) error {
		var args getOwnerApprovalArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		if args.ApprovalID <= 0 && args.ProposalID <= 0 {
			return fmt.Errorf("%w: either approval_id or proposal_id must be positive", ErrInvalidInput)
		}
		return nil
	}

	getApprHandler := func(ctx context.Context, actorRoleID string, raw json.RawMessage) (json.RawMessage, error) {
		turnCtx, ok := TurnContextFrom(ctx)
		orgID := organizationID
		if ok && turnCtx.OrganizationID != "" {
			orgID = turnCtx.OrganizationID
		}
		if authorizer != nil && ok && turnCtx.ActorRoleID != "" {
			if err := authorizer.Authorize(ctx, orgID, turnCtx.OrganizationRevisionID, turnCtx.ActorRoleID, campaign.CapabilityOwnerApprovalRead); err != nil {
				return nil, fmt.Errorf("%w: actor %q lacks %s capability: %v", ErrUnauthorizedActor, turnCtx.ActorRoleID, campaign.CapabilityOwnerApprovalRead, err)
			}
		}

		var args getOwnerApprovalArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}

		var appr campaign.CampaignOwnerApproval
		var err error
		if args.ApprovalID > 0 {
			appr, err = store.GetOwnerApproval(ctx, orgID, args.ApprovalID)
		} else if args.ProposalID > 0 {
			appr, err = store.GetOwnerApprovalByProposal(ctx, orgID, args.ProposalID)
		} else {
			return nil, fmt.Errorf("%w: query identifier required", ErrInvalidInput)
		}
		if err != nil {
			return nil, err
		}

		projection := OwnerApprovalResultProjection{
			ApprovalID:                   appr.ID,
			ProposalID:                   appr.ProposalID,
			ProposalCanonicalHash:        appr.ProposalCanonicalHash,
			FinancialReviewID:            appr.FinancialReviewID,
			FinancialReviewCanonicalHash: appr.FinancialReviewCanonicalHash,
			ApprovedByRoleID:             appr.ApprovedByRoleID,
			Status:                       string(appr.Status),
			ExecutionBudget:              appr.ExecutionBudget,
			CanonicalHash:                appr.CanonicalHash,
			CreatedAt:                    appr.CreatedAt.Format(time.RFC3339),
		}
		return json.Marshal(projection)
	}

	if err := registry.Register(getApprDesc, getApprValidator, getApprHandler); err != nil {
		return fmt.Errorf("register campaign.get_owner_approval: %w", err)
	}

	return nil
}
