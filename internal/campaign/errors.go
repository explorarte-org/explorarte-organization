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
)
