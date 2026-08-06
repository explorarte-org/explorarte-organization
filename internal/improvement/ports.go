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

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }
