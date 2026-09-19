package modelruntime

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// PossibleRoutes must agree with what Resolve can actually select: the static
// policy has exactly its one binding, and a pool has EVERY candidate -- the
// winner depends on capacity state a host preflight must not guess.
func TestPossibleRoutesStaticIsTheOneCanonicalBinding(t *testing.T) {
	store := &fakeRoutingStore{staticBinding: poolBinding("gemini", "gemini-3.5-flash-lite", "worker-default", 9)}
	got, err := PossibleRoutes(context.Background(), store, RouteResolutionRequest{OrganizationID: "explorarte", OrganizationRevisionID: 5, SubjectRoleID: "negocio/x", PolicyID: "department.worker"})
	if err != nil {
		t.Fatal(err)
	}
	want := []RoutableModel{{ProviderID: "gemini", ProviderModelID: "gemini-3.5-flash-lite"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("static routes = %+v, want %+v", got, want)
	}
}

func TestPossibleRoutesPoolListsEveryCandidateSortedAndDeduplicated(t *testing.T) {
	policy, candidates, routes := twoCandidatePool()
	candidates = append(candidates, candidates[0]) // a duplicate row must not double-count
	store := &fakeRoutingStore{policies: map[string]RoutingPolicy{"research.worker": policy}, candidates: map[string][]RoutingCandidate{"research.worker": candidates}, routes: routes}
	got, err := PossibleRoutes(context.Background(), store, RouteResolutionRequest{OrganizationID: "explorarte", OrganizationRevisionID: 5, SubjectRoleID: "investigacion/r", PolicyID: "research.worker"})
	if err != nil {
		t.Fatal(err)
	}
	want := []RoutableModel{
		{ProviderID: "cloudflare_workers_ai", ProviderModelID: "@cf/zai-org/glm-4.7-flash"},
		{ProviderID: "mistral", ProviderModelID: "ministral-8b-2512"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pool routes = %+v, want %+v", got, want)
	}
}

func TestPossibleRoutesFailsClosedOnMalformedPolicies(t *testing.T) {
	ctx := context.Background()
	req := RouteResolutionRequest{OrganizationID: "explorarte", OrganizationRevisionID: 5, SubjectRoleID: "r", PolicyID: "p"}
	empty := &fakeRoutingStore{policies: map[string]RoutingPolicy{"p": {PolicyID: "p", RoutingMode: RoutingModePool}}}
	if _, err := PossibleRoutes(ctx, empty, req); !errors.Is(err, ErrRoutingPolicyMalformed) {
		t.Fatalf("pool with no candidates: err = %v, want ErrRoutingPolicyMalformed", err)
	}
	wrongMode := &fakeRoutingStore{policies: map[string]RoutingPolicy{"p": {PolicyID: "p", RoutingMode: "weird"}}}
	if _, err := PossibleRoutes(ctx, wrongMode, req); !errors.Is(err, ErrRoutingPolicyMalformed) {
		t.Fatalf("unknown routing mode: err = %v, want ErrRoutingPolicyMalformed", err)
	}
	if _, err := PossibleRoutes(ctx, nil, req); err == nil {
		t.Fatal("nil store must fail closed")
	}
}
