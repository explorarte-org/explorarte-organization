package improvement

import "errors"

var (
	ErrInvalidArtifactRef       = errors.New("invalid artifact reference")
	ErrInvalidLineage           = errors.New("invalid candidate lineage")
	ErrInvalidCandidate         = errors.New("invalid candidate")
	ErrInvalidTransition        = errors.New("invalid candidate state transition")
	ErrInvalidRollbackTarget    = errors.New("invalid rollback target")
	ErrInvalidPromotionRequest  = errors.New("invalid promotion request")
	ErrInvalidPromotionDecision = errors.New("invalid promotion decision")
	ErrPromotionDenied          = errors.New("promotion denied")
)
