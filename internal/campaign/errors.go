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
