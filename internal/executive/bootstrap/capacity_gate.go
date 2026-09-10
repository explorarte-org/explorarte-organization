package bootstrap

import (
	"context"
	"errors"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelrouting"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// newCapacityGate builds the tasks.CapacityValidator the Task Engine
// consults, at claim time and at every readiness-reconcile wake, before a
// task whose role routes through a pool policy is ever claimed
// (CAPACITY_EXHAUSTION_SCHEDULING_V1).
//
// It deliberately reuses modelrouting.LookupSelector's own Select -- the
// SAME function DefaultRouteResolver.Resolve calls to actually pick a
// candidate -- rather than restating pool eligibility rules here. This is
// a pure peek: nothing here creates an Invocation, calls a provider,
// spends a model-call budget, or mutates any routing/capacity state. If
// Select finds an eligible candidate, or the answer cannot be determined
// as a clean, timed, transient gap, this reports Available:true and defers
// to the existing claim/RouteResolver path unchanged -- capacity blocking
// only ever fires for the one case it was built for: every candidate is
// really and currently unavailable, with a real, known available-again
// time to wake it by. That fail-open-to-the-old-path default is what keeps
// a structural problem (malformed policy, unknown selector, no candidates)
// from ever being mistaken for a transient capacity wait: those errors
// still surface, unchanged, through the existing fail-closed Resolve path.
// capacityStore is the narrow slice of *modelpostgres.Store this gate
// needs: canonical pool-policy/candidate reads plus the same mutable
// capacity picture RouteResolver itself reads from, nothing else.
type capacityStore interface {
	modelruntime.RegistryStore
	modelruntime.CapacityStateReader
}

func newCapacityGate(registryRepository *registry.PostgresRepository, taskCatalog tasks.Catalog, store capacityStore, organizationID string) tasks.CapacityValidator {
	return func(ctx context.Context, task tasks.Task) (tasks.CapacityCheck, error) {
		role, err := registryRepository.GetRole(ctx, organizationID, task.AssignedRoleID)
		if err != nil || role.ModelPolicy == nil || *role.ModelPolicy == "" {
			// No role, or no model policy at all: not a pool-routed task,
			// nothing for this gate to say. Defer.
			return tasks.CapacityCheck{Available: true}, nil
		}
		revision, err := taskCatalog.CurrentRevision(ctx, organizationID)
		if err != nil {
			return tasks.CapacityCheck{Available: true}, nil
		}
		policy, ok, err := store.GetRoutingPolicy(ctx, organizationID, revision.ID, *role.ModelPolicy)
		if err != nil || !ok || policy.RoutingMode != modelruntime.RoutingModePool {
			// Static policy, or the policy row does not (yet) exist: the
			// existing static-binding / fail-closed path already handles
			// this correctly with no capacity concept involved.
			return tasks.CapacityCheck{Available: true}, nil
		}
		selector, ok := modelrouting.LookupSelector(policy.SelectorID)
		if !ok {
			// Unknown selector is a materialization bug -- STRUCTURAL_NO_
			// ROUTE, not a capacity gap. Defer to Resolve()'s own
			// ErrRoutingPolicyMalformed handling, unchanged.
			return tasks.CapacityCheck{Available: true}, nil
		}
		stored, err := store.ListRoutingCandidates(ctx, organizationID, revision.ID, *role.ModelPolicy)
		if err != nil || len(stored) == 0 {
			return tasks.CapacityCheck{Available: true}, nil
		}
		candidates := make([]modelrouting.Candidate, 0, len(stored))
		state := make(map[string]modelrouting.CandidateState, len(stored))
		for _, c := range stored {
			mc := modelrouting.Candidate{
				ProviderID: c.ProviderID, ProviderModelID: c.ProviderModelID,
				Transport: string(c.Transport), CapacityClass: c.CapacityClass, Priority: c.Priority,
				ProfileID: c.ProfileID, ModelProfileVersionID: c.ModelProfileVersionID,
			}
			candidates = append(candidates, mc)
			cs, err := store.CapacityState(ctx, organizationID, c.ProviderID, c.ProviderModelID)
			if err != nil {
				return tasks.CapacityCheck{Available: true}, nil
			}
			state[c.ProviderID+"|"+c.ProviderModelID] = cs
		}

		now := time.Now()
		if _, err := selector.Select(candidates, state, modelrouting.Requirements{AllowPaid: policy.AllowPaid}, now); err == nil {
			// Some candidate is genuinely selectable right now -- proceed
			// through the normal claim/RouteResolver path exactly as
			// before; this gate has nothing more to say.
			return tasks.CapacityCheck{Available: true}, nil
		} else if !errors.Is(err, modelrouting.ErrNoCapacity) {
			// A Select failure that is not ErrNoCapacity is not a
			// capacity signal at all (the closed Selector registry never
			// returns anything else today, but this stays fail-open to
			// the existing path rather than assume).
			return tasks.CapacityCheck{Available: true}, nil
		}

		// TRANSIENT_NO_CAPACITY: every candidate is unavailable right now.
		// RetryAt is the earliest known available-again time across them,
		// derived only from real, durable capacity state -- never invented.
		var retryAt time.Time
		for _, cs := range state {
			if cs.CooldownUntil == nil {
				continue
			}
			if retryAt.IsZero() || cs.CooldownUntil.Before(retryAt) {
				retryAt = *cs.CooldownUntil
			}
		}
		if retryAt.IsZero() || !retryAt.After(now) {
			// No candidate carries a determinable, future available-again
			// time (e.g. every candidate is Disabled, or QuotaExhausted
			// with no CooldownUntil) -- FASE B's explicit instruction: do
			// not fabricate a RetryAt. Defer to the existing, already
			// fail-closed claim/RouteResolver path instead of inventing a
			// capacity wait with no real wake time.
			return tasks.CapacityCheck{Available: true}, nil
		}
		return tasks.CapacityCheck{Available: false, RetryAt: retryAt, Reason: "transient_no_capacity: every pool candidate is temporarily unavailable"}, nil
	}
}
