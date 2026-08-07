package memory

import "errors"

var (
	ErrInvalidEntry          = errors.New("invalid memory entry")
	ErrInvalidEvidenceRef    = errors.New("invalid memory evidence reference")
	ErrInvalidClassification = errors.New("invalid memory content classification")
	ErrForbiddenDataClass    = errors.New("forbidden memory data class")
	ErrInvalidTransition     = errors.New("invalid memory state transition")
	ErrInvalidReview         = errors.New("invalid memory review")
	ErrEntryNotFound         = errors.New("memory entry not found")
	ErrRevisionConflict      = errors.New("memory revision conflict")
	ErrDuplicateCandidate    = errors.New("duplicate memory candidate")
)
