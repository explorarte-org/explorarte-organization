package campaign

import "errors"

var (
	// ErrProposalNotFound is returned when a requested campaign proposal does not exist.
	ErrProposalNotFound = errors.New("campaign proposal not found")

	// ErrIdempotencyConflict is returned when an idempotency key is reused with a different canonical payload.
	ErrIdempotencyConflict = errors.New("campaign proposal idempotency conflict")

	// ErrInvalidInput is returned when proposal input fails schema or host boundary validation.
	ErrInvalidInput = errors.New("invalid campaign proposal input")

	// ErrUnauthorized is returned when the caller lacks required write or read capability.
	ErrUnauthorized = errors.New("unauthorized campaign proposal operation")

	// ErrReviewRequestNotFound is returned when a requested financial review request does not exist.
	ErrReviewRequestNotFound = errors.New("campaign financial review request not found")

	// ErrFinancialReviewNotFound is returned when a requested financial review does not exist.
	ErrFinancialReviewNotFound = errors.New("campaign financial review not found")

	// ErrProposalHashMismatch is returned when the reviewed proposal canonical hash does not match the request.
	ErrProposalHashMismatch = errors.New("proposal canonical hash mismatch")

	// ErrInvalidVerdict is returned when the review verdict is not one of the supported values.
	ErrInvalidVerdict = errors.New("invalid financial review verdict")

	// ErrSeparationOfDutiesViolation is returned when proponent and reviewer violate separation of duties.
	ErrSeparationOfDutiesViolation = errors.New("separation of duties violation")

	// ErrInvalidTaskLineage is returned when the durable parent task named by
	// RequestedFromTaskID cannot serve as the originating task for a new
	// child task's provenance -- it is missing, belongs to a different
	// organization or organization revision, carries no correlation, or was
	// not requested by the same actor as the child request. A task created
	// on top of an invalid parent would produce unauthorizable provenance
	// (modeldispatch.AuthorizedAttemptProvisioner's resolveTrustedRoot has
	// nothing sound to walk), so this fails closed before any task is ever
	// created.
	ErrInvalidTaskLineage = errors.New("invalid parent task lineage")

	// ErrFinanceTaskPayloadTooLarge is returned when the deterministic
	// Finance task instruction envelope (the complete immutable proposal
	// payload RequestReview embeds so the real Context Engine can surface
	// it to a real Harness run) exceeds the Task Engine's own Instructions
	// size limit. This fails closed before CreateTask -- RequestReview
	// never truncates, drops fields, or silently summarizes the proposal
	// to fit.
	ErrFinanceTaskPayloadTooLarge = errors.New("finance task instruction envelope exceeds the task engine's instructions size limit")

	// ErrInvalidExecutionBudget is returned when a campaign budget
	// recommendation or an already-approved execution budget cannot be
	// translated into an executable agentbudget.Limits -- most commonly
	// because one or more of its seven dimensions is not strictly
	// positive (CAMPAIGN_EXECUTABLE_BUDGET_CONTRACT_HOTFIX_V1). Zero is
	// never reinterpreted as "unlimited" or "disabled" here: a campaign
	// submitted to Executive is an execution tree that may require at
	// least one downstream delegation, so an unexecutable Finance
	// recommendation is rejected, never silently normalized. See
	// ValidateExecutableBudget/ToAgentBudgetLimits, the single seam
	// Finance, Approval, and Promotion all delegate to instead of each
	// hand-rolling their own positivity checks.
	ErrInvalidExecutionBudget = errors.New("invalid campaign execution budget")
)

var (
	// ErrStaleParentRevision is returned when attempting to revise a proposal that is not the latest revision.
	ErrStaleParentRevision = errors.New("stale parent: only the latest revision may be revised")

	// ErrReviewNotRecommended is returned when attempting to approve a proposal whose review verdict is not recommended.
	ErrReviewNotRecommended = errors.New("financial review verdict is not recommended")

	// ErrReviewHashMismatch is returned when review canonical hash does not match the approval request.
	ErrReviewHashMismatch = errors.New("financial review canonical hash mismatch")

	// ErrApprovalNotFound is returned when a requested owner approval does not exist.
	ErrApprovalNotFound = errors.New("campaign owner approval not found")

	// ErrApprovalConflict is returned when an approval idempotency key is reused with different hashes.
	ErrApprovalConflict = errors.New("campaign owner approval conflict")

	// ErrNoFinancialReview is returned when no completed financial review exists for a proposal.
	ErrNoFinancialReview = errors.New("no completed financial review for this proposal")

	// ErrRevisionConflict is returned when a revision idempotency key is reused with a different payload.
	ErrRevisionConflict = errors.New("campaign proposal revision conflict")
)

var (
	// ErrStaleApproval is returned when attempting to promote an approval for a proposal revision that is no longer the latest.
	ErrStaleApproval = errors.New("stale approval: proposal has a newer revision")

	// ErrApprovalNotApproved is returned when approval status is not approved_for_execution.
	ErrApprovalNotApproved = errors.New("approval is not in approved_for_execution status")

	// ErrPromotionAlreadyExists is returned when a promotion already exists for this owner approval.
	ErrPromotionAlreadyExists = errors.New("campaign promotion already exists for this owner approval")

	// ErrPromotionNotFound is returned when a requested campaign promotion does not exist.
	ErrPromotionNotFound = errors.New("campaign promotion not found")

	// ErrSubmitterNotConfigured is returned when the executive submitter port has not been wired.
	ErrSubmitterNotConfigured = errors.New("executive submitter is not configured")

	// ErrBudgetMismatch is returned when the approval budget does not match the financial review recommended budget.
	ErrBudgetMismatch = errors.New("approval execution budget does not match financial review recommended budget")
)
