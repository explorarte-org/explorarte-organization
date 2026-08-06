package improvement

import (
	"context"
	"time"
)

// ApprovalGate authorizes (or denies) a candidate's promotion to the next
// state. It is the only way a candidate can ever advance to canary or
// active: automatic promotion without a gate decision is not possible
// through this package's Service.
type ApprovalGate interface {
	AuthorizePromotion(context.Context, PromotionRequest) (PromotionDecision, error)
}

// CandidateStore persists Candidate aggregates and promotion decisions with
// optimistic concurrency. Service does not depend on this port and stays
// stateless: composition-root code loads a Candidate and its revision,
// calls the appropriate Service method, and saves the result against the
// revision it loaded, so a concurrent writer's save fails with
// ErrRevisionConflict instead of silently overwriting.
type CandidateStore interface {
	// ProposeCandidate persists a new candidate in the proposed state and
	// returns its initial revision. createdBy is an opaque actor identity;
	// Candidate itself carries no such field, matching how PromotionRequest
	// and PromotionDecision already carry actor identity separately from
	// the aggregate they act on.
	ProposeCandidate(ctx context.Context, candidate Candidate, createdBy string) (revision int64, err error)
	// GetCandidate loads a candidate by its ID along with its current
	// revision.
	GetCandidate(ctx context.Context, id string) (Candidate, int64, error)
	// SaveCandidate persists candidate's new state, failing with
	// ErrRevisionConflict if expectedRevision no longer matches what is
	// stored (someone else already updated this candidate).
	SaveCandidate(ctx context.Context, candidate Candidate, expectedRevision int64) (newRevision int64, err error)
	// RecordPromotionDecision appends an immutable audit record combining a
	// PromotionRequest and the ApprovalGate's PromotionDecision for it.
	RecordPromotionDecision(ctx context.Context, request PromotionRequest, decision PromotionDecision) error
}

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }
