package modelruntime

import (
	"context"
	"time"
)

// CostReservation is an opaque handle returned by CostBudgetGate.Reserve,
// passed back to Reconcile or Release once the call's outcome is known.
// Applied is false when the gate deliberately did nothing — e.g. the task
// was never attached to a budget tree — so Reconcile/Release can no-op
// instead of erroring on a reservation that was never made.
type CostReservation struct {
	ProviderID      string
	ProviderModelID string
	InvocationID    int64
	BudgetID        int64
	Applied         bool
}

// CostReservationRequest carries everything CostBudgetGate needs to
// estimate and reserve a call's worst-case cost before the provider is
// contacted.
type CostReservationRequest struct {
	OrganizationID       string
	TaskID               int64
	InvocationID         int64
	ProviderID           string
	ProviderModelID      string
	EstimatedInputTokens int64
	MaxOutputTokens      int64
}

// CostBudgetGate reserves and reconciles the real-money and multidimensional
// cost of a dispatch, without DispatchService depending on
// internal/modelpricing, internal/costledger, or internal/agentbudget
// directly. A nil gate on DispatchService means no cost/budget tracking is
// performed — existing behavior for every caller that doesn't wire one in
// is unaffected.
//
// The wallet (real USD) is reconciled to the actual cost computed from
// reported token usage; the multidimensional budget's model-call/token
// reservation deliberately is not — it stays at its worst-case estimate
// from Reserve. Adding a symmetric downward adjustment there would need
// its own reservation-vs-actual event type and is not needed for the
// budget to do its job: it is a ceiling on runaway agents, not a precise
// accounting ledger the way the wallet is.
type CostBudgetGate interface {
	// Reserve estimates and reserves this call's worst-case cost against
	// both the provider's wallet and the task's budget. An error here
	// means the call must never reach the provider.
	Reserve(ctx context.Context, request CostReservationRequest, now time.Time) (CostReservation, error)
	// Reconcile adjusts the wallet reservation down to the real cost
	// computed from reported token usage.
	Reconcile(ctx context.Context, reservation CostReservation, inputTokens, cachedInputTokens, outputTokens int64, now time.Time) error
	// Release frees the wallet reservation without any billable usage —
	// the call failed before producing a response the provider will
	// charge for.
	Release(ctx context.Context, reservation CostReservation, now time.Time) error
}
