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
	ParentProposalID        *int64                `json:"parent_proposal_id,omitempty"`
	RevisionNumber          int                   `json:"revision_number"`
	RootProposalID          *int64                `json:"root_proposal_id,omitempty"`
	IdempotencyKey          string                `json:"idempotency_key"`
	CanonicalHash           string                `json:"canonical_hash"`
	CreatedAt               time.Time             `json:"created_at"`
	UpdatedAt               time.Time             `json:"updated_at"`
}

// ReviewRequestStatus represents the status of a financial review request.
type ReviewRequestStatus string

const (
	ReviewRequestStatusPending   ReviewRequestStatus = "pending"
	ReviewRequestStatusCompleted ReviewRequestStatus = "completed"
	ReviewRequestStatusFailed    ReviewRequestStatus = "failed"
)

// FinancialReviewVerdict represents the closed vocabulary of financial recommendations.
type FinancialReviewVerdict string

const (
	VerdictRecommended      FinancialReviewVerdict = "recommended"
	VerdictChangesRequested FinancialReviewVerdict = "changes_requested"
	VerdictNotRecommended   FinancialReviewVerdict = "not_recommended"
	VerdictInsufficientData FinancialReviewVerdict = "insufficient_data"
)

// ValidVerdict checks if a verdict string is one of the closed vocabulary values.
func ValidVerdict(v string) bool {
	switch FinancialReviewVerdict(v) {
	case VerdictRecommended, VerdictChangesRequested, VerdictNotRecommended, VerdictInsufficientData:
		return true
	default:
		return false
	}
}

// BudgetRecommendation provides execution budget ceilings compatible with agentbudget.Limits.
type BudgetRecommendation struct {
	MaxUSD        float64 `json:"max_usd"`
	MaxTokens     int64   `json:"max_tokens"`
	MaxModelCalls int     `json:"max_model_calls"`
	MaxWallTimeMS int64   `json:"max_wall_time_ms"`
	MaxDepth      int     `json:"max_depth"`
	MaxRetries    int     `json:"max_retries"`
	MaxSubagents  int     `json:"max_subagents"`
}

// EstimatedCost captures estimated financial impact.
type EstimatedCost struct {
	Amount     float64 `json:"amount"`
	Currency   string  `json:"currency"`
	Confidence string  `json:"confidence,omitempty"` // HIGH, MEDIUM, LOW
}

// CampaignFinancialReviewRequest is the durable record of a request for finance review.
type CampaignFinancialReviewRequest struct {
	ID                          int64               `json:"id"`
	OrganizationID              string              `json:"organization_id"`
	ProposalID                  int64               `json:"proposal_id"`
	ProposalCanonicalHash       string              `json:"proposal_canonical_hash"`
	RequestedByRoleID           string              `json:"requested_by_role_id"`
	RequestedFromConversationID int64               `json:"requested_from_conversation_id,omitempty"`
	RequestedFromMessageID      int64               `json:"requested_from_message_id,omitempty"`
	RequestedFromTaskID         int64               `json:"requested_from_task_id,omitempty"`
	ReviewerRoleID              string              `json:"reviewer_role_id"`
	ReviewTaskID                int64               `json:"review_task_id"`
	Status                      ReviewRequestStatus `json:"status"`
	IdempotencyKey              string              `json:"idempotency_key"`
	CreatedAt                   time.Time           `json:"created_at"`
	UpdatedAt                   time.Time           `json:"updated_at"`
}

// CampaignFinancialReview is the immutable, durable record of a financial review.
type CampaignFinancialReview struct {
	ID                    int64                  `json:"id"`
	OrganizationID        string                 `json:"organization_id"`
	ReviewRequestID       int64                  `json:"review_request_id"`
	ProposalID            int64                  `json:"proposal_id"`
	ProposalCanonicalHash string                 `json:"proposal_canonical_hash"`
	ReviewerRoleID        string                 `json:"reviewer_role_id"`
	ReviewTaskID          int64                  `json:"review_task_id"`
	ReviewAttemptID       int64                  `json:"review_attempt_id"`
	Verdict               FinancialReviewVerdict `json:"verdict"`
	RecommendedBudget     *BudgetRecommendation  `json:"recommended_budget,omitempty"`
	EstimatedCost         *EstimatedCost         `json:"estimated_cost,omitempty"`
	Assumptions           []string               `json:"assumptions"`
	Risks                 []string               `json:"risks"`
	RequiredCorrections   []string               `json:"required_corrections"`
	MissingInformation    []string               `json:"missing_information"`
	Summary               string                 `json:"summary"`
	CanonicalHash         string                 `json:"canonical_hash"`
	CreatedAt             time.Time              `json:"created_at"`
}

// Limits bounds for campaign proposal and financial review fields.
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
	MaxCorrectionsCount            = 20
	MaxCorrectionItemBytes         = 2000
	MaxMissingInfoCount            = 20
	MaxMissingInfoItemBytes        = 2000
	MaxSummaryBytes                = 8000
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

// CreateReviewRequestCommand specifies the inputs required to record a review request.
type CreateReviewRequestCommand struct {
	OrganizationID              string
	ProposalID                  int64
	ProposalCanonicalHash       string
	RequestedByRoleID           string
	RequestedFromConversationID int64
	RequestedFromMessageID      int64
	RequestedFromTaskID         int64
	ReviewerRoleID              string
	ReviewTaskID                int64
	IdempotencyKey              string
}

// RecordFinancialReviewCommand specifies the inputs required to persist an immutable review.
type RecordFinancialReviewCommand struct {
	OrganizationID        string
	ReviewRequestID       int64
	ProposalID            int64
	ProposalCanonicalHash string
	ReviewerRoleID        string
	ReviewTaskID          int64
	ReviewAttemptID       int64
	Verdict               FinancialReviewVerdict
	RecommendedBudget     *BudgetRecommendation
	EstimatedCost         *EstimatedCost
	Assumptions           []string
	Risks                 []string
	RequiredCorrections   []string
	MissingInformation    []string
	Summary               string
	CanonicalHash         string
}

// ApprovalStatus represents the lifecycle state of an owner execution approval.
type ApprovalStatus string

const (
	// StatusApprovedForExecution means the owner has authorized promotion to Executive.
	StatusApprovedForExecution ApprovalStatus = "approved_for_execution"
)

// CampaignOwnerApproval is the durable, append-only record of an owner approving
// a specific campaign proposal + financial review tuple for eventual Executive promotion.
type CampaignOwnerApproval struct {
	ID                           int64                `json:"id"`
	OrganizationID               string               `json:"organization_id"`
	ProposalID                   int64                `json:"proposal_id"`
	ProposalCanonicalHash        string               `json:"proposal_canonical_hash"`
	FinancialReviewID            int64                `json:"financial_review_id"`
	FinancialReviewCanonicalHash string               `json:"financial_review_canonical_hash"`
	ApprovedByRoleID             string               `json:"approved_by_role_id"`
	ConversationID               int64                `json:"conversation_id,omitempty"`
	MessageID                    int64                `json:"message_id,omitempty"`
	TurnTaskID                   int64                `json:"turn_task_id,omitempty"`
	ToolCallID                   string               `json:"tool_call_id"`
	Status                       ApprovalStatus       `json:"status"`
	ExecutionBudget              BudgetRecommendation `json:"execution_budget"`
	IdempotencyKey               string               `json:"idempotency_key"`
	CanonicalHash                string               `json:"canonical_hash"`
	CreatedAt                    time.Time            `json:"created_at"`
}

// CreateRevisionCommand specifies the inputs required to create a new proposal revision.
type CreateRevisionCommand struct {
	OrganizationID       string
	ParentProposalID     int64
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

// CreateOwnerApprovalCommand specifies the inputs required to record an owner execution approval.
type CreateOwnerApprovalCommand struct {
	OrganizationID               string
	ProposalID                   int64
	ProposalCanonicalHash        string
	FinancialReviewID            int64
	FinancialReviewCanonicalHash string
	ApprovedByRoleID             string
	ConversationID               int64
	MessageID                    int64
	TurnTaskID                   int64
	ToolCallID                   string
	ExecutionBudget              BudgetRecommendation
	IdempotencyKey               string
	CanonicalHash                string
}

// PromotionStatus represents the lifecycle state of a campaign promotion.
type PromotionStatus string

const (
	// StatusSubmitted means the campaign has been submitted to Executive and its root is durable.
	StatusSubmitted PromotionStatus = "submitted"
)

// CampaignPromotion is the durable record linking an approved campaign proposal + financial review
// + owner approval tuple to an Executive root task execution.
type CampaignPromotion struct {
	ID                            int64                `json:"id"`
	OrganizationID                string               `json:"organization_id"`
	OwnerApprovalID               int64                `json:"owner_approval_id"`
	OwnerApprovalCanonicalHash    string               `json:"owner_approval_canonical_hash"`
	ProposalID                    int64                `json:"proposal_id"`
	ProposalCanonicalHash         string               `json:"proposal_canonical_hash"`
	FinancialReviewID             int64                `json:"financial_review_id"`
	FinancialReviewCanonicalHash  string               `json:"financial_review_canonical_hash"`
	ExecutionBudget               BudgetRecommendation `json:"execution_budget"`
	ExecutiveRootTaskID           int64                `json:"executive_root_task_id"`
	ExecutiveCorrelationID        string               `json:"executive_correlation_id"`
	ExecutiveSubmitIdempotencyKey string               `json:"executive_submit_idempotency_key"`
	Status                        PromotionStatus      `json:"status"`
	PromotedByRoleID              string               `json:"promoted_by_role_id"`
	ConversationID                int64                `json:"conversation_id,omitempty"`
	MessageID                     int64                `json:"message_id,omitempty"`
	TurnTaskID                    int64                `json:"turn_task_id,omitempty"`
	ToolCallID                    string               `json:"tool_call_id"`
	IdempotencyKey                string               `json:"idempotency_key"`
	CanonicalHash                 string               `json:"canonical_hash"`
	CreatedAt                     time.Time            `json:"created_at"`
}

// CreatePromotionCommand specifies the inputs required to record a campaign promotion.
type CreatePromotionCommand struct {
	OrganizationID                string
	OwnerApprovalID               int64
	OwnerApprovalCanonicalHash    string
	ProposalID                    int64
	ProposalCanonicalHash         string
	FinancialReviewID             int64
	FinancialReviewCanonicalHash  string
	ExecutionBudget               BudgetRecommendation
	ExecutiveRootTaskID           int64
	ExecutiveCorrelationID        string
	ExecutiveSubmitIdempotencyKey string
	Status                        PromotionStatus
	PromotedByRoleID              string
	ConversationID                int64
	MessageID                     int64
	TurnTaskID                    int64
	ToolCallID                    string
	IdempotencyKey                string
	CanonicalHash                 string
}

// PromoteToExecutiveParams carries the host-verified parameters to promote an approved campaign.
type PromoteToExecutiveParams struct {
	OrganizationID         string
	OrganizationRevisionID int64
	OwnerApprovalID        int64
	PromotedByRoleID       string
	ConversationID         int64
	MessageID              int64
	TurnTaskID             int64
	ToolCallID             string
	IdempotencyKey         string
}

// PromotionResult carries the result of a promotion to Executive.
type PromotionResult struct {
	Promotion              CampaignPromotion `json:"promotion"`
	ExecutiveRootTaskID    int64             `json:"executive_root_task_id"`
	ExecutiveCorrelationID string            `json:"executive_correlation_id"`
	Reused                 bool              `json:"reused"`
}

const (
	CapabilityPromotionExecute = "campaign.promotion.execute"
	CapabilityPromotionRead    = "campaign.promotion.read"
)
