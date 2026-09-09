package modelruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelrouting"
)

// fakeRoutingStore is a minimal RegistryStore double purpose-built for
// RouteResolver tests -- distinct from service_test.go's fakeStore so
// these tests carry zero risk of perturbing the ~30 existing
// InvocationService/Dispatch tests that already depend on fakeStore's
// exact current behavior (invariant #16).
type fakeRoutingStore struct {
	staticBinding ResolvedBinding
	staticErr     error

	policies   map[string]RoutingPolicy
	candidates map[string][]RoutingCandidate
	routes     map[string]ResolvedBinding
}

func (f *fakeRoutingStore) RecordRegistryValidated(context.Context, string, string) error { return nil }
func (f *fakeRoutingStore) RegistryStatus(context.Context, string, int64, string) (RegistryStatus, error) {
	return RegistryStatus{}, nil
}
func (f *fakeRoutingStore) ApplyRegistry(context.Context, RegistryPlan, int) (RegistrySyncResult, error) {
	return RegistrySyncResult{}, nil
}
func (f *fakeRoutingStore) GetBinding(context.Context, string, int64, string) (ResolvedBinding, error) {
	if f.staticErr != nil {
		return ResolvedBinding{}, f.staticErr
	}
	return f.staticBinding, nil
}
func (f *fakeRoutingStore) GetRoutingPolicy(_ context.Context, _ string, _ int64, policyID string) (RoutingPolicy, bool, error) {
	p, ok := f.policies[policyID]
	return p, ok, nil
}
func (f *fakeRoutingStore) ListRoutingCandidates(_ context.Context, _ string, _ int64, policyID string) ([]RoutingCandidate, error) {
	return f.candidates[policyID], nil
}
func (f *fakeRoutingStore) GetCandidateRoute(_ context.Context, _ string, _ int64, profileID string) (ResolvedBinding, error) {
	b, ok := f.routes[profileID]
	if !ok {
		return ResolvedBinding{}, ErrBindingNotFound
	}
	return b, nil
}

func poolBinding(provider, model, profileID string, versionID int64) ResolvedBinding {
	return ResolvedBinding{
		Profile:      Profile{ID: profileID, PolicyID: profileID},
		Version:      ProfileVersion{ID: versionID, ProfileID: profileID, ProviderID: provider, ProviderModelID: model, Transport: TransportHTTP, AdapterStatus: AdapterAvailable, DispatchEnabled: true},
		Capabilities: CapabilitySnapshot{Capabilities: []ModelCapability{"structured.output"}},
		Provider:     Provider{ID: provider, Transport: TransportHTTP, AdapterStatus: AdapterAvailable, DispatchEnabled: true},
	}
}

func twoCandidatePool() (RoutingPolicy, []RoutingCandidate, map[string]ResolvedBinding) {
	policy := RoutingPolicy{PolicyID: "research.worker", RoutingMode: RoutingModePool, SelectorID: "free_capacity_v1", AllowPaid: false, CanonicalHash: "poolhash"}
	candidates := []RoutingCandidate{
		{PolicyID: "research.worker", ProviderID: "cloudflare_workers_ai", ProviderModelID: "@cf/zai-org/glm-4.7-flash", Transport: TransportHTTP, CapacityClass: "free_daily", Priority: 10, ProfileID: "research.worker~pool~0", ModelProfileVersionID: 100, CandidateHash: "cand-cloudflare"},
		{PolicyID: "research.worker", ProviderID: "mistral", ProviderModelID: "ministral-8b-2512", Transport: TransportHTTP, CapacityClass: "credit_monthly", Priority: 20, ProfileID: "research.worker~pool~1", ModelProfileVersionID: 101, CandidateHash: "cand-mistral"},
	}
	routes := map[string]ResolvedBinding{
		"research.worker~pool~0": poolBinding("cloudflare_workers_ai", "@cf/zai-org/glm-4.7-flash", "research.worker~pool~0", 100),
		"research.worker~pool~1": poolBinding("mistral", "ministral-8b-2512", "research.worker~pool~1", 101),
	}
	return policy, candidates, routes
}

// TestRouteResolverStaticPassthrough: Section 12.A -- a policy with no
// routing_policies row resolves through GetBinding exactly as before this
// feature existed. Answers question A/D: no dynamic machinery is even
// consulted for a static role.
func TestRouteResolverStaticPassthrough(t *testing.T) {
	binding := poolBinding("test.fake", "v1", "worker-default", 9)
	store := &fakeRoutingStore{staticBinding: binding}
	resolver, err := NewDefaultRouteResolver(store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	route, err := resolver.Resolve(context.Background(), RouteResolutionRequest{OrganizationID: "explorarte", OrganizationRevisionID: 7, SubjectRoleID: "ingenieria_ia/arquitecto_software", PolicyID: "department.worker"})
	if err != nil {
		t.Fatal(err)
	}
	if route.RoutingMode != RoutingModeStatic {
		t.Fatalf("RoutingMode = %q, want static", route.RoutingMode)
	}
	if route.Binding.Version.ProviderID != "test.fake" {
		t.Fatalf("static route provider = %q, want test.fake", route.Binding.Version.ProviderID)
	}
}

// TestRouteResolverPoolSelectsBetweenTwoCandidates: Section 12.C -- the
// same SubjectRoleID/policy can resolve to either approved provider
// depending on capacity state. Answers question B: yes, only within the
// canonical policy's own candidate set.
func TestRouteResolverPoolSelectsBetweenTwoCandidates(t *testing.T) {
	policy, candidates, routes := twoCandidatePool()
	store := &fakeRoutingStore{policies: map[string]RoutingPolicy{"research.worker": policy}, candidates: map[string][]RoutingCandidate{"research.worker": candidates}, routes: routes}
	resolver, err := NewDefaultRouteResolver(store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := RouteResolutionRequest{OrganizationID: "explorarte", OrganizationRevisionID: 7, SubjectRoleID: "investigacion/research_worker_hourly", PolicyID: "research.worker"}

	route, err := resolver.Resolve(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if route.RoutingMode != RoutingModePool || route.Binding.Version.ProviderID != "cloudflare_workers_ai" {
		t.Fatalf("expected pool to pick cloudflare (free_daily beats credit_monthly), got mode=%s provider=%s", route.RoutingMode, route.Binding.Version.ProviderID)
	}

	// Cloudflare exhausted -> the SAME policy/role now resolves to mistral.
	capacity := &fakeCapacityReader{disabled: map[string]bool{"cloudflare_workers_ai|@cf/zai-org/glm-4.7-flash": true}}
	resolver2, err := NewDefaultRouteResolver(store, capacity, nil)
	if err != nil {
		t.Fatal(err)
	}
	route2, err := resolver2.Resolve(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if route2.Binding.Version.ProviderID != "mistral" {
		t.Fatalf("expected fallback to mistral once cloudflare is disabled, got %s", route2.Binding.Version.ProviderID)
	}
}

type fakeCapacityReader struct{ disabled map[string]bool }

func (f *fakeCapacityReader) CapacityState(_ context.Context, _, providerID, providerModelID string) (modelrouting.CandidateState, error) {
	return modelrouting.CandidateState{Disabled: f.disabled[providerID+"|"+providerModelID]}, nil
}

// TestRouteResolverDeniesNonCanonicalSelectorAnswer: Section 12.D. A
// selector that answers with something outside the materialized candidate
// set must be denied BEFORE any binding is resolved -- proving the deny
// boundary is independent of whether a given Selector implementation is
// trustworthy.
func TestRouteResolverDeniesNonCanonicalSelectorAnswer(t *testing.T) {
	policy, candidates, routes := twoCandidatePool()
	policy.SelectorID = "evil_selector"
	store := &fakeRoutingStore{policies: map[string]RoutingPolicy{"research.worker": policy}, candidates: map[string][]RoutingCandidate{"research.worker": candidates}, routes: routes}
	resolver, err := NewDefaultRouteResolver(store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// evilSelector is installed via SelectorLookup on this ONE resolver
	// instance -- internal/modelrouting's production registry is
	// unexported and immutable at runtime (Closure 3), so no test mutates
	// shared state.
	resolver.SelectorLookup = func(id string) (modelrouting.Selector, bool) {
		if id == "evil_selector" {
			return evilSelector{}, true
		}
		return modelrouting.LookupSelector(id)
	}

	_, err = resolver.Resolve(context.Background(), RouteResolutionRequest{OrganizationID: "explorarte", OrganizationRevisionID: 7, SubjectRoleID: "investigacion/research_worker_hourly", PolicyID: "research.worker"})
	if !errors.Is(err, ErrRouteNotApproved) {
		t.Fatalf("err = %v, want ErrRouteNotApproved", err)
	}
}

// evilSelector always answers with a candidate that was never part of the
// set it was handed -- simulating a buggy or compromised Selector.
type evilSelector struct{}

func (evilSelector) Select([]modelrouting.Candidate, map[string]modelrouting.CandidateState, modelrouting.Requirements, time.Time) (modelrouting.Decision, error) {
	return modelrouting.Decision{Candidate: modelrouting.Candidate{ProviderID: "evil_provider", ProviderModelID: "invented-model", ProfileID: "nowhere", ModelProfileVersionID: 999}}, nil
}

// TestRouteResolverUnknownSelectorFailsClosed: a routing_policies row
// naming a selector this binary does not compile is a materialization
// bug, and must fail closed rather than silently falling back to
// anything.
func TestRouteResolverUnknownSelectorFailsClosed(t *testing.T) {
	policy, candidates, routes := twoCandidatePool()
	policy.SelectorID = "unregistered_selector_v9"
	store := &fakeRoutingStore{policies: map[string]RoutingPolicy{"research.worker": policy}, candidates: map[string][]RoutingCandidate{"research.worker": candidates}, routes: routes}
	resolver, err := NewDefaultRouteResolver(store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.Resolve(context.Background(), RouteResolutionRequest{OrganizationID: "explorarte", OrganizationRevisionID: 7, SubjectRoleID: "investigacion/research_worker_hourly", PolicyID: "research.worker"})
	if !errors.Is(err, ErrRoutingPolicyMalformed) {
		t.Fatalf("err = %v, want ErrRoutingPolicyMalformed", err)
	}
}

// TestRouteResolverNoMaterializedCandidatesFailsClosed proves an empty
// candidate set (materialization bug: a routing_policies row with no
// routing_candidates rows) denies rather than resolving to nothing
// silently succeeding.
func TestRouteResolverNoMaterializedCandidatesFailsClosed(t *testing.T) {
	policy := RoutingPolicy{PolicyID: "research.worker", RoutingMode: RoutingModePool, SelectorID: "free_capacity_v1"}
	store := &fakeRoutingStore{policies: map[string]RoutingPolicy{"research.worker": policy}}
	resolver, err := NewDefaultRouteResolver(store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.Resolve(context.Background(), RouteResolutionRequest{OrganizationID: "explorarte", OrganizationRevisionID: 7, SubjectRoleID: "investigacion/research_worker_hourly", PolicyID: "research.worker"})
	if !errors.Is(err, ErrRoutingPolicyMalformed) {
		t.Fatalf("err = %v, want ErrRoutingPolicyMalformed", err)
	}
}

// TestRouteResolverAllowPaidFalseDeniesPaidOnlyPool: Section 12.L /
// invariant #15, at the resolver boundary specifically -- a pool whose
// only candidate is "paid" with AllowPaid=false never resolves to
// anything, so CreateInvocation is never reached and zero provider calls
// can happen.
func TestRouteResolverAllowPaidFalseDeniesPaidOnlyPool(t *testing.T) {
	policy := RoutingPolicy{PolicyID: "research.worker", RoutingMode: RoutingModePool, SelectorID: "free_capacity_v1", AllowPaid: false}
	candidates := []RoutingCandidate{{PolicyID: "research.worker", ProviderID: "openai_compatible", ProviderModelID: "gpt-5", Transport: TransportHTTP, CapacityClass: "paid", Priority: 1, ProfileID: "research.worker~pool~0", ModelProfileVersionID: 100}}
	routes := map[string]ResolvedBinding{"research.worker~pool~0": poolBinding("openai_compatible", "gpt-5", "research.worker~pool~0", 100)}
	store := &fakeRoutingStore{policies: map[string]RoutingPolicy{"research.worker": policy}, candidates: map[string][]RoutingCandidate{"research.worker": candidates}, routes: routes}
	resolver, err := NewDefaultRouteResolver(store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.Resolve(context.Background(), RouteResolutionRequest{OrganizationID: "explorarte", OrganizationRevisionID: 7, SubjectRoleID: "investigacion/research_worker_hourly", PolicyID: "research.worker"})
	if !errors.Is(err, modelrouting.ErrNoCapacity) {
		t.Fatalf("err = %v, want modelrouting.ErrNoCapacity", err)
	}
}

// Direct unit tests of ValidateResolvedRouteAgainstCanonical itself
// (Blocker 2: "testear directamente el validator de route membership"),
// independent of any RouteResolver implementation.

func TestValidateResolvedRouteAgainstCanonicalAcceptsMatchingStaticRoute(t *testing.T) {
	binding := poolBinding("test.fake", "v1", "worker-default", 9)
	store := &fakeRoutingStore{staticBinding: binding}
	req := RouteResolutionRequest{OrganizationID: "explorarte", OrganizationRevisionID: 7, SubjectRoleID: "ingenieria_ia/arquitecto_software", PolicyID: "department.worker"}
	route := ResolvedRoute{Binding: binding, RoutingMode: RoutingModeStatic}
	if err := ValidateResolvedRouteAgainstCanonical(context.Background(), store, req, route); err != nil {
		t.Fatalf("expected the canonical static binding to validate, got: %v", err)
	}
}

func TestValidateResolvedRouteAgainstCanonicalRejectsMismatchedStaticRoute(t *testing.T) {
	canonical := poolBinding("test.fake", "v1", "worker-default", 9)
	store := &fakeRoutingStore{staticBinding: canonical}
	req := RouteResolutionRequest{OrganizationID: "explorarte", OrganizationRevisionID: 7, SubjectRoleID: "ingenieria_ia/arquitecto_software", PolicyID: "department.worker"}
	forged := poolBinding("evil_provider", "invented-model", "worker-default", 9)
	route := ResolvedRoute{Binding: forged, RoutingMode: RoutingModeStatic}
	if err := ValidateResolvedRouteAgainstCanonical(context.Background(), store, req, route); !errors.Is(err, ErrRouteNotApproved) {
		t.Fatalf("err = %v, want ErrRouteNotApproved", err)
	}
}

func TestValidateResolvedRouteAgainstCanonicalAcceptsMatchingPoolCandidate(t *testing.T) {
	policy, candidates, routes := twoCandidatePool()
	store := &fakeRoutingStore{policies: map[string]RoutingPolicy{"research.worker": policy}, candidates: map[string][]RoutingCandidate{"research.worker": candidates}, routes: routes}
	req := RouteResolutionRequest{OrganizationID: "explorarte", OrganizationRevisionID: 7, SubjectRoleID: "investigacion/research_worker_hourly", PolicyID: "research.worker"}
	route := ResolvedRoute{Binding: routes["research.worker~pool~1"], RoutingMode: RoutingModePool}
	if err := ValidateResolvedRouteAgainstCanonical(context.Background(), store, req, route); err != nil {
		t.Fatalf("expected the real mistral candidate to validate, got: %v", err)
	}
}

func TestValidateResolvedRouteAgainstCanonicalRejectsNonMemberPoolRoute(t *testing.T) {
	policy, candidates, routes := twoCandidatePool()
	store := &fakeRoutingStore{policies: map[string]RoutingPolicy{"research.worker": policy}, candidates: map[string][]RoutingCandidate{"research.worker": candidates}, routes: routes}
	req := RouteResolutionRequest{OrganizationID: "explorarte", OrganizationRevisionID: 7, SubjectRoleID: "investigacion/research_worker_hourly", PolicyID: "research.worker"}
	forged := poolBinding("mistral", "ministral-8b-2512", "not-a-real-candidate-profile", 999)
	route := ResolvedRoute{Binding: forged, RoutingMode: RoutingModePool}
	if err := ValidateResolvedRouteAgainstCanonical(context.Background(), store, req, route); !errors.Is(err, ErrRouteNotApproved) {
		t.Fatalf("err = %v, want ErrRouteNotApproved", err)
	}
}
