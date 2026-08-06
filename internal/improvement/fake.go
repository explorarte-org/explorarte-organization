package improvement

import (
	"context"
	"sync"
	"time"
)

// FakeApprovalGate is an ApprovalGate for tests. Without a custom Decide
// function it authorizes every request. It is safe for concurrent use.
type FakeApprovalGate struct {
	mu     sync.Mutex
	Decide func(PromotionRequest) (PromotionDecision, error)
}

func NewFakeApprovalGate() *FakeApprovalGate { return &FakeApprovalGate{} }

func (f *FakeApprovalGate) SetDecide(fn func(PromotionRequest) (PromotionDecision, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Decide = fn
}

func (f *FakeApprovalGate) AuthorizePromotion(ctx context.Context, request PromotionRequest) (PromotionDecision, error) {
	if err := ctx.Err(); err != nil {
		return PromotionDecision{}, err
	}
	f.mu.Lock()
	fn := f.Decide
	f.mu.Unlock()
	if fn != nil {
		return fn(request)
	}
	return PromotionDecision{
		CandidateID: request.CandidateID,
		Kind:        request.Kind,
		Outcome:     PromotionAuthorized,
		DecidedAt:   time.Now().UTC(),
		DecidedBy:   "fake-approver",
	}, nil
}
