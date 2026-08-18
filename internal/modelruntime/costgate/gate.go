// Package costgate wires internal/modelpricing, internal/costledger, and
// internal/agentbudget together behind modelruntime.CostBudgetGate, so
// DispatchService itself never imports any of those three packages
// directly.
package costgate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	"github.com/Mireuz13/explorarte-organization/internal/costledger"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/Mireuz13/explorarte-organization/internal/programbudget"
)

type Gate struct {
	pricing               *modelpricing.Service
	ledger                costledger.Ledger
	budgets               agentbudget.Ledger
	subscriptionProviders map[string]bool
	programResolver       interface {
		Resolve(context.Context, int64, string, string) (programbudget.Scope, error)
	}
}

func (g *Gate) WithProgramBudgetResolver(r interface {
	Resolve(context.Context, int64, string, string) (programbudget.Scope, error)
}) *Gate {
	g.programResolver = r
	return g
}

// New wires the PAYG cost/budget gate. subscriptionProviders names
// provider IDs billed via a fixed subscription/token-plan rather than
// pay-as-you-go (e.g. "mimo") — Reserve skips PriceTier resolution and
// wallet reservation entirely for these, since no real per-call USD price
// exists to reserve against and none is fabricated. Variadic and optional:
// omitting it (or passing none) preserves the exact prior all-PAYG
// behavior for every existing caller.
func New(pricing *modelpricing.Service, ledger costledger.Ledger, budgets agentbudget.Ledger, subscriptionProviders ...string) (*Gate, error) {
	if pricing == nil || ledger == nil || budgets == nil {
		return nil, errors.New("costgate requires pricing, ledger, and budgets")
	}
	subscribed := make(map[string]bool, len(subscriptionProviders))
	for _, id := range subscriptionProviders {
		subscribed[id] = true
	}
	return &Gate{pricing: pricing, ledger: ledger, budgets: budgets, subscriptionProviders: subscribed}, nil
}

var _ modelruntime.CostBudgetGate = (*Gate)(nil)
var _ modelruntime.SubscriptionSettler = (*Gate)(nil)

func (g *Gate) Reserve(ctx context.Context, request modelruntime.CostReservationRequest, now time.Time) (modelruntime.CostReservation, error) {
	// Budget tracking is independently optional per task (not every task is
	// attached to a budget tree — e.g. CEO planning/review/closure tasks
	// today). Wallet tracking is NOT optional for any real call: real money
	// gets spent regardless of whether anyone remembered to wire that task
	// into a budget tree, so a missing budget must never silently skip the
	// wallet reservation too.
	budget, err := g.budgets.ResolveBudgetForTask(ctx, request.TaskID)
	budgetTracked := true
	if errors.Is(err, agentbudget.ErrBudgetNotFound) {
		budgetTracked = false
	} else if err != nil {
		return modelruntime.CostReservation{}, fmt.Errorf("resolve budget for task: %w", err)
	}
	var programScope programbudget.Scope
	if g.programResolver != nil {
		programScope, err = g.programResolver.Resolve(ctx, request.TaskID, request.ProviderID, request.ProviderModelID)
		if err != nil {
			return modelruntime.CostReservation{}, err
		}
	}

	if g.subscriptionProviders[request.ProviderID] {
		// Subscription/token-plan billing (e.g. mimo): there is no
		// PriceTier to resolve and no real per-call USD amount to reserve
		// against the wallet — see modelruntime.CostReservation.
		// Subscription's doc comment. Budget/token tracking (a real,
		// observable fact — tokens requested, one model call) still
		// applies when a budget tree is attached; only the USD dimension
		// of it is zero, honestly, because it truly is zero.
		reservation := modelruntime.CostReservation{
			ProviderID: request.ProviderID, ProviderModelID: request.ProviderModelID,
			InvocationID: request.InvocationID, WalletApplied: false, Subscription: true,
		}
		if !budgetTracked {
			return reservation, nil
		}
		delta := agentbudget.Usage{
			UsedUSD:        0,
			UsedTokens:     request.EstimatedInputTokens + request.MaxOutputTokens,
			UsedModelCalls: 1,
		}
		if err := g.budgets.ConsumeModelCall(ctx, budget.ID, request.InvocationID, delta, now); err != nil {
			return modelruntime.CostReservation{}, err
		}
		reservation.BudgetID = budget.ID
		reservation.BudgetApplied = true
		return reservation, nil
	}

	tier, err := g.pricing.Resolve(ctx, request.ProviderID, request.ProviderModelID, request.EstimatedInputTokens, modelpricing.BillingOnline, now)
	if err != nil {
		return modelruntime.CostReservation{}, fmt.Errorf("resolve price tier: %w", err)
	}
	estimatedUSD, err := tier.EstimateCost(request.EstimatedInputTokens, 0, 0, request.MaxOutputTokens)
	if err != nil {
		return modelruntime.CostReservation{}, fmt.Errorf("estimate call cost: %w", err)
	}

	if g.programResolver != nil {
		if programScope.Family.Key != "" {
			scoped, ok := g.ledger.(costledger.ProgramScopedReserver)
			if !ok {
				return modelruntime.CostReservation{}, fmt.Errorf("program scoped reservation unavailable")
			}
			if err := scoped.ReserveWithinProgramCeiling(ctx, costledger.ProgramReservation{ProviderID: request.ProviderID, ProviderModelID: request.ProviderModelID, InvocationID: request.InvocationID, CorrelationID: programScope.CorrelationID, MaxUSD: programScope.Family.MaxUSD, EstimatedUSD: estimatedUSD}, now); err != nil {
				return modelruntime.CostReservation{}, err
			}
		} else if err := g.ledger.Reserve(ctx, request.ProviderID, request.InvocationID, estimatedUSD, now); err != nil {
			return modelruntime.CostReservation{}, err
		}
	} else if err := g.ledger.Reserve(ctx, request.ProviderID, request.InvocationID, estimatedUSD, now); err != nil {
		return modelruntime.CostReservation{}, err
	}
	reservation := modelruntime.CostReservation{
		ProviderID: request.ProviderID, ProviderModelID: request.ProviderModelID,
		InvocationID: request.InvocationID, WalletApplied: true,
	}

	if !budgetTracked {
		return reservation, nil
	}

	delta := agentbudget.Usage{
		UsedUSD:        estimatedUSD,
		UsedTokens:     request.EstimatedInputTokens + request.MaxOutputTokens,
		UsedModelCalls: 1,
	}
	if err := g.budgets.ConsumeModelCall(ctx, budget.ID, request.InvocationID, delta, now); err != nil {
		if releaseErr := g.ledger.Release(ctx, request.ProviderID, request.InvocationID, now); releaseErr != nil {
			return modelruntime.CostReservation{}, errors.Join(err, releaseErr)
		}
		return modelruntime.CostReservation{}, err
	}
	reservation.BudgetID = budget.ID
	reservation.BudgetApplied = true
	return reservation, nil
}

// RecordSubscriptionConsumption implements modelruntime.SubscriptionSettler.
// A no-op for any non-subscription reservation. For a subscription
// reservation, it records (once, idempotently) that the call reached and
// was processed by the provider — real resource/quota consumption — without
// computing or persisting any USD amount, via the optional
// costledger.SubscriptionRecorder capability (same optional-capability
// pattern as PendingReconciliationMarker below: not every Ledger backend
// needs to implement it).
func (g *Gate) RecordSubscriptionConsumption(ctx context.Context, reservation modelruntime.CostReservation, now time.Time) error {
	if !reservation.Subscription {
		return nil
	}
	recorder, ok := g.ledger.(costledger.SubscriptionRecorder)
	if !ok {
		return nil
	}
	return recorder.RecordSubscriptionConsumption(ctx, reservation.ProviderID, reservation.InvocationID, now)
}

// Reconcile prices inputTokens entirely at the fresh (non-cached) rate: the
// system does not currently capture a cache-hit/cache-miss split in
// reported usage (modelruntime.Usage only has InputTokens/OutputTokens),
// so a caller always passes cachedInputTokens=0 today. This can only
// overestimate real cost in providers with cache pricing, never
// underestimate it.
func (g *Gate) Reconcile(ctx context.Context, reservation modelruntime.CostReservation, inputTokens, cachedInputTokens, outputTokens int64, now time.Time) error {
	if !reservation.WalletApplied {
		return nil
	}
	tier, err := g.pricing.Resolve(ctx, reservation.ProviderID, reservation.ProviderModelID, inputTokens, modelpricing.BillingOnline, now)
	if err != nil {
		return fmt.Errorf("resolve price tier for reconciliation: %w", err)
	}
	actualUSD, err := tier.EstimateCost(inputTokens, cachedInputTokens, 0, outputTokens)
	if err != nil {
		return fmt.Errorf("estimate actual call cost: %w", err)
	}
	return g.ledger.Reconcile(ctx, reservation.ProviderID, reservation.InvocationID, actualUSD, now)
}

func (g *Gate) Release(ctx context.Context, reservation modelruntime.CostReservation, now time.Time) error {
	if !reservation.WalletApplied {
		return nil
	}
	return g.ledger.Release(ctx, reservation.ProviderID, reservation.InvocationID, now)
}

// MarkPendingReconciliation is a best-effort annotation: not every
// costledger.Ledger implementation supports it (see
// costledger.PendingReconciliationMarker's doc comment on why it is kept
// separate from the required Ledger interface), so a backend that doesn't
// implement it is treated the same as WalletApplied=false — the reservation
// stays wherever Reserve left it rather than erroring the whole dispatch
// over a missing annotation capability.
func (g *Gate) MarkPendingReconciliation(ctx context.Context, reservation modelruntime.CostReservation, now time.Time) error {
	if !reservation.WalletApplied {
		return nil
	}
	marker, ok := g.ledger.(costledger.PendingReconciliationMarker)
	if !ok {
		return nil
	}
	return marker.MarkPendingReconciliation(ctx, reservation.ProviderID, reservation.InvocationID, now)
}
