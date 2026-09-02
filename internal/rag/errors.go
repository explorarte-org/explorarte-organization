package rag

import "errors"

var (
	ErrInvalidDocument     = errors.New("invalid knowledge document")
	ErrInvalidVersion      = errors.New("invalid knowledge version")
	ErrInvalidTransition   = errors.New("invalid knowledge lifecycle transition")
	ErrInvalidAdmission    = errors.New("invalid knowledge admission")
	ErrForbiddenDataClass  = errors.New("forbidden knowledge data class")
	ErrInvalidReview       = errors.New("invalid knowledge review")
	ErrRevisionConflict    = errors.New("knowledge registry revision conflict")
	ErrNotFound            = errors.New("knowledge registry record not found")
	ErrConflict            = errors.New("knowledge registry conflict")
	ErrInvalidRequest      = errors.New("invalid knowledge request")
	ErrInvalidNamespace    = errors.New("invalid knowledge namespace")
	ErrVersionNotApproved  = errors.New("knowledge version is not approved")
	ErrInvalidChunk        = errors.New("invalid knowledge chunk")
	ErrInvalidGeneration   = errors.New("invalid knowledge index generation")
	ErrSourceDrift         = errors.New("knowledge source drift")
	ErrIdempotencyConflict = errors.New("knowledge idempotency conflict")
	// ErrSelfReview means the reviewing role is the same role that proposed
	// this version (ActorRoleID == ProposedBy). Independent of, and additive
	// to, capability-matrix.yaml restricting rag.publish_approved to
	// owner-only (G4-001): a future canonical grant change must not
	// silently reintroduce self-review with zero code change.
	ErrSelfReview          = errors.New("knowledge review rejected: reviewer proposed this version")
)
