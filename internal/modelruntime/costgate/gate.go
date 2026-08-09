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
)

type Gate struct {
	pricing *modelpricing.Service
	ledger  costledger.Ledger
	budgets agentbudget.Ledger
}

func New(pricing *modelpricing.Service, ledger costledger.Ledger, budgets agentbudget.Ledger) (*Gate, error) {
	if pricing == nil || ledger == nil || budgets == nil {
		return nil, errors.New("costgate requires pricing, ledger, and budgets")
	}
	return &Gate{pricing: pricing, ledger: ledger, budgets: budgets}, nil
}

var _ modelruntime.CostBudgetGate = (*Gate)(nil)

func (g *Gate) Reserve(ctx context.Context, request modelruntime.CostReservationRequest, now time.Time) (modelruntime.CostReservation, error) {
	budget, err := g.budgets.ResolveBudgetForTask(ctx, request.TaskID)
	if errors.Is(err, agentbudget.ErrBudgetNotFound) {
		// This task was never attached to a budget tree — untracked,
		// deliberately not an error. Group A/B backlog work and other
		// tasks that predate this ledger must keep dispatching normally.
		return modelruntime.CostReservation{Applied: false}, nil
	}
	if err != nil {
		return modelruntime.CostReservation{}, fmt.Errorf("resolve budget for task: %w", err)
	}

	tier, err := g.pricing.Resolve(ctx, request.ProviderID, request.ProviderModelID, request.EstimatedInputTokens, now)
	if err != nil {
		return modelruntime.CostReservation{}, fmt.Errorf("resolve price tier: %w", err)
	}
	estimatedUSD, err := tier.EstimateCost(request.EstimatedInputTokens, 0, 0, request.MaxOutputTokens)
	if err != nil {
		return modelruntime.CostReservation{}, fmt.Errorf("estimate call cost: %w", err)
	}

	if err := g.ledger.Reserve(ctx, request.ProviderID, request.InvocationID, estimatedUSD, now); err != nil {
		return modelruntime.CostReservation{}, err
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

	return modelruntime.CostReservation{
		ProviderID: request.ProviderID, ProviderModelID: request.ProviderModelID,
		InvocationID: request.InvocationID, BudgetID: budget.ID, Applied: true,
	}, nil
}

// Reconcile prices inputTokens entirely at the fresh (non-cached) rate: the
// system does not currently capture a cache-hit/cache-miss split in
// reported usage (modelruntime.Usage only has InputTokens/OutputTokens),
// so a caller always passes cachedInputTokens=0 today. This can only
// overestimate real cost in providers with cache pricing, never
// underestimate it.
func (g *Gate) Reconcile(ctx context.Context, reservation modelruntime.CostReservation, inputTokens, cachedInputTokens, outputTokens int64, now time.Time) error {
	if !reservation.Applied {
		return nil
	}
	tier, err := g.pricing.Resolve(ctx, reservation.ProviderID, reservation.ProviderModelID, inputTokens, now)
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
	if !reservation.Applied {
		return nil
	}
	return g.ledger.Release(ctx, reservation.ProviderID, reservation.InvocationID, now)
}
