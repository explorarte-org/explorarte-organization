package campaign

import "time"

// ProposalStatus represents the lifecycle state of a campaign proposal.
// In V1, only "draft" (and optionally "withdrawn") are permitted.
type ProposalStatus string

const (
	StatusDraft     ProposalStatus = "draft"
	StatusWithdrawn ProposalStatus = "withdrawn"
)

// BudgetSource indicates whether the budget figure is an explicit owner limit,
// a CEO estimate, or unknown.
type BudgetSource string

const (
	BudgetSourceOwnerLimit  BudgetSource = "OWNER_LIMIT"
	BudgetSourceCEOEstimate BudgetSource = "CEO_ESTIMATE"
	BudgetSourceUnknown     BudgetSource = "UNKNOWN"
)

// ProposalBudget captures non-executing financial expectations / estimates.
// In this V1, this does NOT allocate, reserve, or mutate wallet balances.
type ProposalBudget struct {
	Currency  string       `json:"currency"`
	MaxAmount float64      `json:"max_amount"`
	Source    BudgetSource `json:"source,omitempty"`
}

// ProposalRequirement describes a structured prerequisite or operational need.
type ProposalRequirement struct {
	Key         string `json:"key"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// CampaignProposal is the durable, immutable proposal record.
type CampaignProposal struct {
	ID                      int64                 `json:"id"`
	OrganizationID          string                `json:"organization_id"`
	ConversationID          int64                 `json:"conversation_id"`
	CreatedByRoleID         string                `json:"created_by_role_id"`
	CreatedFromMessageID    int64                 `json:"created_from_message_id"`
	TaskID                  int64                 `json:"task_id"`
	AttemptID               int64                 `json:"attempt_id"`
	ToolCallID              string                `json:"tool_call_id"`
	Status                  ProposalStatus        `json:"status"`
	Title                   string                `json:"title"`
	Goal                    string                `json:"goal"`
	AcceptanceCriteria      []string              `json:"acceptance_criteria"`
	Requirements            []ProposalRequirement `json:"requirements"`
	Budget                  *ProposalBudget       `json:"budget,omitempty"`
	Assumptions             []string              `json:"assumptions"`
	Risks                   []string              `json:"risks"`
	OpenQuestions           []string              `json:"open_questions"`
	FinancialReviewRequired bool                  `json:"financial_review_required"`
	ExecutionStarted        bool                  `json:"execution_started"`
	IdempotencyKey          string                `json:"idempotency_key"`
	CanonicalHash           string                `json:"canonical_hash"`
	CreatedAt               time.Time             `json:"created_at"`
	UpdatedAt               time.Time             `json:"updated_at"`
}

// Limits bounds for campaign proposal fields (host-owned invariants).
const (
	MaxTitleLength                 = 4000
	MaxGoalLength                  = 16000
	MaxAcceptanceCriteriaCount     = 20
	MaxAcceptanceCriteriaItemBytes = 2000
	MaxRequirementsCount           = 20
	MaxRequirementKeyBytes         = 200
	MaxRequirementDescBytes        = 2000
	MaxAssumptionsCount            = 20
	MaxAssumptionItemBytes         = 2000
	MaxRisksCount                  = 20
	MaxRiskItemBytes               = 2000
	MaxOpenQuestionsCount          = 20
	MaxOpenQuestionItemBytes       = 2000
	MaxIdempotencyKeyLength        = 240
	MaxToolCallIDLength            = 240
	MaxRoleIDLength                = 240
)

// CreateProposalCommand specifies the inputs required to record a proposal draft.
type CreateProposalCommand struct {
	OrganizationID       string
	ConversationID       int64
	CreatedByRoleID      string
	CreatedFromMessageID int64
	TaskID               int64
	AttemptID            int64
	ToolCallID           string
	IdempotencyKey       string
	CanonicalHash        string

	Title              string
	Goal               string
	AcceptanceCriteria []string
	Requirements       []ProposalRequirement
	Budget             *ProposalBudget
	Assumptions        []string
	Risks              []string
	OpenQuestions      []string
}
