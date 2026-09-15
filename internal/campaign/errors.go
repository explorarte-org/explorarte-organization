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
