package memory

import "errors"

var (
	ErrInvalidRequest     = errors.New("invalid memory request")
	ErrInvalidEntry       = errors.New("invalid memory entry")
	ErrInvalidEvidenceRef = errors.New("invalid memory evidence reference")
	ErrInvalidAdmission   = errors.New("invalid memory admission attestation")
	ErrForbiddenDataClass = errors.New("forbidden memory data class")
	ErrInvalidTransition  = errors.New("invalid memory state transition")
	ErrInvalidReview      = errors.New("invalid memory review")
	ErrEntryNotFound      = errors.New("memory entry not found")
	ErrRevisionConflict   = errors.New("memory revision conflict")
	ErrDuplicateCandidate = errors.New("duplicate memory candidate")
	ErrConflict           = errors.New("memory conflict")
	// ErrSelfReview means the reviewing role is the same role that proposed
	// the entry (Review.ReviewerID == Entry.ProposedBy). This is an
	// independent guard, additive to -- never a replacement for --
	// capability-matrix.yaml restricting memory.approve to owner-only
	// (G4-001): a future canonical grant change must not silently
	// reintroduce self-review with zero code change.
	ErrSelfReview         = errors.New("memory review rejected: reviewer proposed this entry")
)
