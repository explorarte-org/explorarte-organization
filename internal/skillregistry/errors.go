package skillregistry

import "errors"

var (
	ErrInvalidSkill           = errors.New("invalid skill")
	ErrInvalidVersion         = errors.New("invalid skill version")
	ErrInvalidAssignment      = errors.New("invalid skill assignment")
	ErrInvalidTransition      = errors.New("invalid skill lifecycle transition")
	ErrMissingActivationProof = errors.New("missing skill activation evidence")
	ErrCapabilityReviewFailed = errors.New("skill capability review failed")
	ErrVersionNotActive       = errors.New("skill version is not active")
	ErrAssignmentNotActive    = errors.New("skill assignment is not active")
	ErrAssignmentConflict     = errors.New("skill assignment conflict")
	ErrRevisionConflict       = errors.New("skill registry revision conflict")
	ErrNotFound               = errors.New("skill registry record not found")
	ErrSourceDrift            = errors.New("skill source drift")
	ErrIdempotencyConflict    = errors.New("skill registry idempotency conflict")
)
