package modelruntime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelrouting"
)

var (
	// ErrRoutingPolicyMalformed means the materialized routing_policies/
	// routing_candidates state for a policy is internally inconsistent
	// (unknown selector, zero candidates, wrong mode) -- a materialization
	// bug, never a caller problem. Fails closed before CreateInvocation.
	ErrRoutingPolicyMalformed = errors.New("modelruntime: routing policy malformed")
	// ErrRouteNotApproved means a Selector answered with something that is
	// not, byte-for-byte, one of the candidates it was handed from the
	// canonical materialized set. This is the DENY boundary for an
	// invented/non-canonical candidate (Section 12.D) -- it fires before
	// any capability/egress/pricing check ever runs, and before
	// CreateInvocation is called.
	ErrRouteNotApproved = errors.New("modelruntime: selected route is not part of the approved canonical candidate set")
)

// RouteResolutionRequest carries only host/kernel-authorized data: the
// organization/revision this Create() call is already operating under, the
// SubjectRoleID the assignment/binding checks already validated, and
// PolicyID -- the subject role's own ModelPolicy, as already read from
// OrganizationCatalog.GetRole by the caller. Nothing here is free-form
// caller input; there is no field a request body, an LLM response, or a
// scheduler could populate with an arbitrary provider/model.
type RouteResolutionRequest struct {
	OrganizationID         string
	OrganizationRevisionID int64
	SubjectRoleID          string
	PolicyID               string
}

// ResolvedRoute is what RouteResolver hands back to InvocationService.Create:
// the SAME ResolvedBinding shape GetBinding has always returned (so every
// downstream capability/egress/hash/persistence step is unchanged code),
// plus the provenance InvocationService persists for audit (Section 10).
type ResolvedRoute struct {
	Binding ResolvedBinding
	// PolicyID is the docs/canonical/model-routing.yaml policy key this
	// route was resolved against (empty for a route resolved with no
	// PolicyID request, which never happens through InvocationService.Create
	// -- kept as a field, not inferred from Binding, since a static route
	// has no routing_policies row to read it back from).
	PolicyID         string
	RoutingMode      string
	SelectorID       string
	CandidateSetHash string
	// CandidateHash is the SPECIFIC selected candidate's hash
	// (RoutingCandidate.CandidateHash) -- distinct from CandidateSetHash,
	// which digests the whole pool at decision time.
	CandidateHash  string
	DecisionReason string
}

// RouteResolver answers ONLY "where does this already-authorized
// invocation dispatch". It never answers "who may dispatch it" -- that
// remains modeldispatch.AssignmentResolver, called earlier in Create() and
// completely unaware this type exists.
type RouteResolver interface {
	Resolve(ctx context.Context, req RouteResolutionRequest) (ResolvedRoute, error)
}

// CapacityStateReader is the host-owned, mutable capacity picture a pool
// Selector ranks against. A narrow port rather than a full
// modelrouting.CandidateState struct so a deployment with no dynamic
// capacity tracking of its own can supply a trivial always-eligible reader
// and still get correct, deterministic pool selection by class/priority.
type CapacityStateReader interface {
	CapacityState(ctx context.Context, organizationID, providerID, providerModelID string) (modelrouting.CandidateState, error)
}

// AlwaysAvailableCapacityState is the default CapacityStateReader: every
// candidate is eligible, none disabled/cooling down/exhausted. A
// deployment that wires real capacity tracking (dispatch failures,
// quota/rate-limit signals) replaces this with its own reader; nothing
// else about RouteResolver changes.
type AlwaysAvailableCapacityState struct{}

func (AlwaysAvailableCapacityState) CapacityState(context.Context, string, string, string) (modelrouting.CandidateState, error) {
	return modelrouting.CandidateState{}, nil
}

// DefaultRouteResolver is the canonical RouteResolver. Static policies
// resolve through Store.GetBinding -- byte-for-byte the path every static
// role has always taken (invariant: unchanged static semantics). Pool
// policies resolve through Store.GetRoutingPolicy/ListRoutingCandidates
// and a pure internal/modelrouting.Selector; the two paths converge on the
// same ResolvedBinding shape before capability/egress/hash/persistence.
type DefaultRouteResolver struct {
	Store    RegistryStore
	Capacity CapacityStateReader
	Clock    func() time.Time
	// SelectorLookup resolves a selector_id to its implementation.
	// Defaults to modelrouting.LookupSelector (the closed, immutable
	// production registry). Overridable per-instance -- never via a
	// package-level global -- so a test can exercise "Resolve() itself
	// distrusts a misbehaving Selector" (TestRouteResolverDeniesNonCanonicalSelectorAnswer)
	// without mutating any shared state another test or goroutine could
	// observe.
	SelectorLookup func(id string) (modelrouting.Selector, bool)
}

func NewDefaultRouteResolver(store RegistryStore, capacity CapacityStateReader, clock func() time.Time) (*DefaultRouteResolver, error) {
	if store == nil {
		return nil, fmt.Errorf("route resolver requires a registry store")
	}
	if capacity == nil {
		capacity = AlwaysAvailableCapacityState{}
	}
	if clock == nil {
		clock = time.Now
	}
	return &DefaultRouteResolver{Store: store, Capacity: capacity, Clock: clock, SelectorLookup: modelrouting.LookupSelector}, nil
}

func (r *DefaultRouteResolver) Resolve(ctx context.Context, req RouteResolutionRequest) (ResolvedRoute, error) {
	policy, ok, err := r.Store.GetRoutingPolicy(ctx, req.OrganizationID, req.OrganizationRevisionID, req.PolicyID)
	if err != nil {
		return ResolvedRoute{}, err
	}
	if !ok {
		// No routing_policies row: this is a static policy, resolved
		// exactly as every static policy has always been resolved.
		binding, err := r.Store.GetBinding(ctx, req.OrganizationID, req.OrganizationRevisionID, req.SubjectRoleID)
		if err != nil {
			return ResolvedRoute{}, err
		}
		return ResolvedRoute{Binding: binding, PolicyID: req.PolicyID, RoutingMode: RoutingModeStatic, DecisionReason: "static binding"}, nil
	}
	if policy.RoutingMode != RoutingModePool {
		return ResolvedRoute{}, fmt.Errorf("%w: policy %q has routing_mode %q", ErrRoutingPolicyMalformed, req.PolicyID, policy.RoutingMode)
	}
	lookup := r.SelectorLookup
	if lookup == nil {
		lookup = modelrouting.LookupSelector
	}
	selector, ok := lookup(policy.SelectorID)
	if !ok {
		return ResolvedRoute{}, fmt.Errorf("%w: policy %q names unknown selector %q", ErrRoutingPolicyMalformed, req.PolicyID, policy.SelectorID)
	}
	stored, err := r.Store.ListRoutingCandidates(ctx, req.OrganizationID, req.OrganizationRevisionID, req.PolicyID)
	if err != nil {
		return ResolvedRoute{}, err
	}
	if len(stored) == 0 {
		return ResolvedRoute{}, fmt.Errorf("%w: pool policy %q has no materialized candidates", ErrRoutingPolicyMalformed, req.PolicyID)
	}

	candidates := make([]modelrouting.Candidate, 0, len(stored))
	approved := make(map[string]RoutingCandidate, len(stored))
	state := make(map[string]modelrouting.CandidateState, len(stored))
	for _, c := range stored {
		mc := modelrouting.Candidate{
			ProviderID: c.ProviderID, ProviderModelID: c.ProviderModelID,
			Transport: string(c.Transport), CapacityClass: c.CapacityClass, Priority: c.Priority,
			ProfileID: c.ProfileID, ModelProfileVersionID: c.ModelProfileVersionID,
		}
		candidates = append(candidates, mc)
		key := c.ProviderID + "|" + c.ProviderModelID
		approved[key] = c
		cs, err := r.Capacity.CapacityState(ctx, req.OrganizationID, c.ProviderID, c.ProviderModelID)
		if err != nil {
			return ResolvedRoute{}, err
		}
		state[key] = cs
	}

	decision, err := selector.Select(candidates, state, modelrouting.Requirements{AllowPaid: policy.AllowPaid}, r.Clock())
	if err != nil {
		return ResolvedRoute{}, err
	}

	// Prove the selector's answer is one of the candidates it was actually
	// given -- never trust a Selector's return value blindly, however
	// trusted its implementation. This is what makes an invented/
	// non-canonical candidate impossible to reach CreateInvocation with,
	// independent of whether the Selector implementation is buggy,
	// compromised, or simply new and unreviewed.
	key := decision.Candidate.ProviderID + "|" + decision.Candidate.ProviderModelID
	approvedCandidate, ok := approved[key]
	if !ok || approvedCandidate.ProfileID != decision.Candidate.ProfileID || approvedCandidate.ModelProfileVersionID != decision.Candidate.ModelProfileVersionID {
		return ResolvedRoute{}, fmt.Errorf("%w: selector returned provider=%s model=%s", ErrRouteNotApproved, decision.Candidate.ProviderID, decision.Candidate.ProviderModelID)
	}

	binding, err := r.Store.GetCandidateRoute(ctx, req.OrganizationID, req.OrganizationRevisionID, approvedCandidate.ProfileID)
	if err != nil {
		return ResolvedRoute{}, err
	}
	// Defense in depth: the freshly-read, FK-checked binding must itself
	// agree with the approved candidate -- a second, independent read
	// against the same canonical source, not a re-trust of the same value.
	if binding.Version.ProviderID != approvedCandidate.ProviderID || binding.Version.ProviderModelID != approvedCandidate.ProviderModelID {
		return ResolvedRoute{}, fmt.Errorf("%w: materialized route drifted from approved candidate", ErrRouteNotApproved)
	}

	return ResolvedRoute{
		Binding:          binding,
		PolicyID:         req.PolicyID,
		RoutingMode:      RoutingModePool,
		SelectorID:       policy.SelectorID,
		CandidateSetHash: policy.CanonicalHash,
		CandidateHash:    approvedCandidate.CandidateHash,
		DecisionReason:   decision.Reason,
	}, nil
}

// ValidateResolvedRouteAgainstCanonical independently re-derives and
// checks a ResolvedRoute directly against the canonical registry store --
// regardless of which RouteResolver produced it. InvocationService.Create
// calls this unconditionally after every Resolve(), so a RouteResolver
// installed via SetRouteResolver -- misconfigured, buggy, or actively
// malicious -- can never make CreateInvocation see a binding this function
// did not independently verify against routing_policies/routing_candidates/
// role_model_bindings itself. This is the same trust posture Resolve()
// already applies to a pool Selector's answer (never trusted without
// checking membership first), extended to cover the RouteResolver
// boundary itself.
func ValidateResolvedRouteAgainstCanonical(ctx context.Context, store RegistryStore, req RouteResolutionRequest, route ResolvedRoute) error {
	policy, ok, err := store.GetRoutingPolicy(ctx, req.OrganizationID, req.OrganizationRevisionID, req.PolicyID)
	if err != nil {
		return err
	}
	if !ok {
		if route.RoutingMode != RoutingModeStatic {
			return fmt.Errorf("%w: policy %q is static but resolved route claims routing_mode %q", ErrRouteNotApproved, req.PolicyID, route.RoutingMode)
		}
		canonical, err := store.GetBinding(ctx, req.OrganizationID, req.OrganizationRevisionID, req.SubjectRoleID)
		if err != nil {
			return err
		}
		if route.Binding.Profile.ID != canonical.Profile.ID ||
			route.Binding.Version.ID != canonical.Version.ID ||
			route.Binding.Version.ProviderID != canonical.Version.ProviderID ||
			route.Binding.Version.ProviderModelID != canonical.Version.ProviderModelID {
			return fmt.Errorf("%w: resolved route does not match the canonical static binding for role %s", ErrRouteNotApproved, req.SubjectRoleID)
		}
		return nil
	}
	if route.RoutingMode != RoutingModePool {
		return fmt.Errorf("%w: policy %q is a pool but resolved route claims routing_mode %q", ErrRouteNotApproved, req.PolicyID, route.RoutingMode)
	}
	if policy.RoutingMode != RoutingModePool {
		return fmt.Errorf("%w: policy %q has routing_mode %q", ErrRoutingPolicyMalformed, req.PolicyID, policy.RoutingMode)
	}
	candidates, err := store.ListRoutingCandidates(ctx, req.OrganizationID, req.OrganizationRevisionID, req.PolicyID)
	if err != nil {
		return err
	}
	for _, c := range candidates {
		if c.ProfileID == route.Binding.Profile.ID &&
			c.ModelProfileVersionID == route.Binding.Version.ID &&
			c.ProviderID == route.Binding.Version.ProviderID &&
			c.ProviderModelID == route.Binding.Version.ProviderModelID {
			return nil
		}
	}
	return fmt.Errorf("%w: resolved route provider=%s model=%s is not a materialized candidate of policy %q", ErrRouteNotApproved, route.Binding.Version.ProviderID, route.Binding.Version.ProviderModelID, req.PolicyID)
}
