package ceochat

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
)

// CapabilityAuthorizer evaluates canonical capability permissions.
type CapabilityAuthorizer interface {
	Authorize(ctx context.Context, organizationID string, revisionID int64, roleID, capability string) error
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
)

// RegisterCampaignTools registers campaign.propose (mutating), campaign.get_proposal (read_only),
// and campaign.list_proposals (read_only) into the provided ToolRegistry.
func RegisterCampaignTools(registry *ToolRegistry, organizationID string, store campaign.Store, authorizer CapabilityAuthorizer) error {
	if registry == nil {
		return fmt.Errorf("%w: registry is required", ErrInvalidInput)
	}
	if store == nil {
		return fmt.Errorf("%w: campaign store is required", ErrInvalidInput)
	}

	// 1. campaign.propose (MUTATING)
	proposeDesc := ToolDescriptor{
		ID:           "campaign.propose",
		Version:      "v1",
		Description:  "Creates a durable, immutable draft campaign proposal based on explicit owner intent. Does not execute the campaign, does not create an executive root task, and does not allocate operational budget.",
		InputSchema:  campaignProposeInputSchema,
		Access:       AccessMutating,
		Effect:       ToolEffectWrite,
		RequiredRole: CEORoleID,
		Limits: ToolLimits{
			MaxRows:        1,
			MaxResultBytes: 4096,
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
			return fmt.Errorf("%w: title must be 1..%d bytes", ErrInvalidInput, campaign.MaxTitleLength)
		}
		if strings.TrimSpace(args.Goal) == "" || len(args.Goal) > campaign.MaxGoalLength {
			return fmt.Errorf("%w: goal must be 1..%d bytes", ErrInvalidInput, campaign.MaxGoalLength)
		}
		if len(args.AcceptanceCriteria) == 0 || len(args.AcceptanceCriteria) > campaign.MaxAcceptanceCriteriaCount {
			return fmt.Errorf("%w: acceptance_criteria count must be 1..%d", ErrInvalidInput, campaign.MaxAcceptanceCriteriaCount)
		}
		if len(args.Requirements) > campaign.MaxRequirementsCount {
			return fmt.Errorf("%w: requirements count exceeds %d", ErrInvalidInput, campaign.MaxRequirementsCount)
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

		// Authorization guard: only a role holding campaign.proposal.create may cause this mutation.
		if authorizer != nil {
			if err := authorizer.Authorize(ctx, turnCtx.OrganizationID, turnCtx.OrganizationRevisionID, turnCtx.ActorRoleID, "campaign.proposal.create"); err != nil {
				return nil, fmt.Errorf("%w: actor %q lacks campaign.proposal.create capability: %v", ErrUnauthorizedActor, turnCtx.ActorRoleID, err)
			}
		}

		var args proposeArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}

		// Default budget source if unset
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

	return nil
}
