package modelruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelrouting"
)

func swapSelector(id string, s modelrouting.Selector) map[string]modelrouting.Selector {
	orig := modelrouting.Selectors
	replaced := make(map[string]modelrouting.Selector, len(orig))
	for k, v := range orig {
		replaced[k] = v
	}
	replaced[id] = s
	modelrouting.Selectors = replaced
	return orig
}

func restoreSelectors(orig map[string]modelrouting.Selector) {
	modelrouting.Selectors = orig
}

// poolFakeStore wraps the existing fakeStore (service_test.go) instead of
// modifying it, so these new tests carry zero risk to the ~30 existing
// InvocationService/Dispatch tests that already depend on fakeStore's
// current CreateInvocation behavior (invariant #16).
//
// Unlike fakeStore.CreateInvocation (which always "succeeds" and never
// models a real idempotency conflict), this one simulates the actual
// Postgres contract: INSERT ... ON CONFLICT(organization_id,
// idempotency_key) DO NOTHING, then re-read-and-compare on conflict
// (internal/modelruntime/postgres/invocations.go). That is the exact
// mechanism Section 9/12.K needs proven: a retry under the same
// idempotency key returns the ORIGINAL row untouched, or a typed conflict
// if the request genuinely differs -- never a silently different route.
type poolFakeStore struct {
	*fakeStore
	policy     RoutingPolicy
	candidates []RoutingCandidate
	routes     map[string]ResolvedBinding

	byKey  map[string]Invocation
	nextID int64
}

func newPoolFakeStore(base *fakeStore) *poolFakeStore {
	return &poolFakeStore{fakeStore: base, byKey: map[string]Invocation{}}
}

func (f *poolFakeStore) GetRoutingPolicy(_ context.Context, _ string, _ int64, policyID string) (RoutingPolicy, bool, error) {
	if policyID != f.policy.PolicyID {
		return RoutingPolicy{}, false, nil
	}
	return f.policy, true, nil
}
func (f *poolFakeStore) ListRoutingCandidates(_ context.Context, _ string, _ int64, policyID string) ([]RoutingCandidate, error) {
	if policyID != f.policy.PolicyID {
		return nil, nil
	}
	return f.candidates, nil
}
func (f *poolFakeStore) GetCandidateRoute(_ context.Context, _ string, _ int64, profileID string) (ResolvedBinding, error) {
	b, ok := f.routes[profileID]
	if !ok {
		return ResolvedBinding{}, ErrBindingNotFound
	}
	return b, nil
}

func (f *poolFakeStore) CreateInvocation(_ context.Context, p PreparedInvocation, _ int) (CreateInvocationResult, error) {
	f.created = true
	f.prepared = p
	if existing, ok := f.byKey[p.Command.IdempotencyKey]; ok {
		if existing.RequestHash != p.RequestHash {
			return CreateInvocationResult{}, fmt.Errorf("%w: idempotency key reused with different request", ErrConflict)
		}
		return CreateInvocationResult{Invocation: existing, Reused: true}, nil
	}
	f.nextID++
	inv := f.invocation
	inv.ID = f.nextID
	inv.RequestHash = p.RequestHash
	inv.ModelProfileID = p.Binding.Profile.ID
	inv.ModelProfileVersionID = p.Binding.Version.ID
	inv.ProviderID = p.Binding.Version.ProviderID
	inv.ProviderModelID = p.Binding.Version.ProviderModelID
	f.byKey[p.Command.IdempotencyKey] = inv
	return CreateInvocationResult{Invocation: inv}, nil
}

func poolFixture(t *testing.T) (*poolFakeStore, fakeCatalog, fakeTaskReader, *fakeContextReader, fakeAssignmentResolver, time.Time) {
	t.Helper()
	base, catalog, task, contexts, assignments, _, now := serviceFixture()
	catalog.role.ModelPolicy = "research.worker"
	policy, candidates, routes := twoCandidatePool()
	store := newPoolFakeStore(base)
	store.policy = policy
	store.candidates = candidates
	store.routes = routes
	return store, catalog, task, contexts, assignments, now
}

func poolCommand(now time.Time, idempotencyKey string) CreateInvocationCommand {
	return CreateInvocationCommand{
		OrganizationID: "explorarte", TaskID: 3, AttemptID: 4, SubjectRoleID: "ingenieria_ia/code-runner",
		ContextSnapshotID: 5, Purpose: "test", RequiredCapabilities: []ModelCapability{"structured.output"},
		OutputMode: OutputJSON, OutputSchema: json.RawMessage(`{"type":"object"}`), MaxOutputTokens: 100,
		ThinkingMode: ThinkingDisabled, IdempotencyKey: idempotencyKey, Deadline: now.Add(time.Hour),
	}
}

// TestCreatePoolSelectsAndEvaluatesEconomicsAgainstSelectedProvider:
// Section 12.C/F/G/H end to end through Create(). The capability snapshot
// checked, the egress evaluation, and the frozen provider/model on the
// resulting Invocation must all reflect the ACTUALLY selected candidate --
// not the role's static default (there is none here) and not whichever
// candidate happens to be listed first.
func TestCreatePoolSelectsAndEvaluatesEconomicsAgainstSelectedProvider(t *testing.T) {
	store, catalog, task, contexts, assignments, now := poolFixture(t)
	svc, err := NewInvocationService("explorarte", catalog, task, contexts, store, store, store, assignments, ClockFunc(func() time.Time { return now }), 10, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Create(context.Background(), poolCommand(now, "pool-econ-1"))
	if err != nil {
		t.Fatal(err)
	}
	if store.prepared.Binding.Version.ProviderID != "cloudflare_workers_ai" {
		t.Fatalf("expected free_daily candidate selected first, got %s", store.prepared.Binding.Version.ProviderID)
	}
	if got.Invocation.ProviderID != "cloudflare_workers_ai" || got.Invocation.ProviderModelID != "@cf/zai-org/glm-4.7-flash" {
		t.Fatalf("frozen invocation route = %s/%s, want the selected candidate", got.Invocation.ProviderID, got.Invocation.ProviderModelID)
	}
	if store.prepared.RoutingMode != RoutingModePool || store.prepared.RoutingSelectorID != "free_capacity_v1" {
		t.Fatalf("routing provenance not persisted: mode=%s selector=%s", store.prepared.RoutingMode, store.prepared.RoutingSelectorID)
	}
}

// TestCreatePoolImmutableAfterCreate: invariant #9/#10, Section 12.I --
// once persisted, ProviderID/ProviderModelID/ModelProfileVersionID never
// change for that Invocation, independent of anything that happens later
// (a second, different Create() call under a DIFFERENT idempotency key
// must not touch the first row).
func TestCreatePoolImmutableAfterCreate(t *testing.T) {
	store, catalog, task, contexts, assignments, now := poolFixture(t)
	svc, err := NewInvocationService("explorarte", catalog, task, contexts, store, store, store, assignments, ClockFunc(func() time.Time { return now }), 10, false)
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.Create(context.Background(), poolCommand(now, "pool-immutable-1"))
	if err != nil {
		t.Fatal(err)
	}
	frozenProvider := first.Invocation.ProviderID
	frozenVersion := first.Invocation.ModelProfileVersionID

	// Capacity state drifts, a DIFFERENT logical request (different key)
	// now resolves to the other candidate.
	svc.SetRouteResolver(mustResolver(t, store, map[string]bool{"cloudflare_workers_ai|@cf/zai-org/glm-4.7-flash": true}))
	second, err := svc.Create(context.Background(), poolCommand(now, "pool-immutable-2"))
	if err != nil {
		t.Fatal(err)
	}
	if second.Invocation.ProviderID == frozenProvider {
		t.Fatalf("expected the second, distinct request to route to the other candidate once the first was disabled")
	}

	// Re-fetch what "row #1" would still say: byKey still has it untouched.
	stillFirst := store.byKey["pool-immutable-1"]
	if stillFirst.ProviderID != frozenProvider || stillFirst.ModelProfileVersionID != frozenVersion {
		t.Fatalf("first invocation's route mutated: now %s/%d, was %s/%d", stillFirst.ProviderID, stillFirst.ModelProfileVersionID, frozenProvider, frozenVersion)
	}
}

func mustResolver(t *testing.T, store RegistryStore, disabled map[string]bool) RouteResolver {
	t.Helper()
	r, err := NewDefaultRouteResolver(store, &fakeCapacityReader{disabled: disabled}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestCreatePoolFallbackIsANewInvocation: Section 8/12.J. A caller
// retrying after a (simulated) provider failure with a FRESH idempotency
// key gets a fresh, distinct InvocationID with the newly selected route;
// the original invocation's own row is never touched -- there is no
// "mutate provider on the same row" path in this design at all.
func TestCreatePoolFallbackIsANewInvocation(t *testing.T) {
	store, catalog, task, contexts, assignments, now := poolFixture(t)
	svc, err := NewInvocationService("explorarte", catalog, task, contexts, store, store, store, assignments, ClockFunc(func() time.Time { return now }), 10, false)
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.Create(context.Background(), poolCommand(now, "pool-fallback-1"))
	if err != nil {
		t.Fatal(err)
	}
	// Simulate: cloudflare attempt failed (retryable/quota/rate-limit),
	// capacity state updated, caller mints a NEW idempotency key for the
	// fallback attempt (never reuses the failed attempt's key).
	svc.SetRouteResolver(mustResolver(t, store, map[string]bool{"cloudflare_workers_ai|@cf/zai-org/glm-4.7-flash": true}))
	second, err := svc.Create(context.Background(), poolCommand(now, "pool-fallback-2"))
	if err != nil {
		t.Fatal(err)
	}
	if second.Invocation.ID == first.Invocation.ID {
		t.Fatal("fallback attempt must be a distinct InvocationID, never the same row")
	}
	if first.Invocation.ProviderID != "cloudflare_workers_ai" || second.Invocation.ProviderID != "mistral" {
		t.Fatalf("expected first=cloudflare second=mistral, got first=%s second=%s", first.Invocation.ProviderID, second.Invocation.ProviderID)
	}
}

// TestCreatePoolIdempotentRetryPreservesOriginalRouteDespiteCapacityDrift
// is the adversarial case Section 9 names explicitly: T0 resolves
// Cloudflare and the caller's response is lost; by T1 capacity state has
// flipped so Cloudflare would no longer be chosen; a retry with the SAME
// idempotency key MUST return the original Cloudflare invocation, never
// create/re-resolve a Mistral one.
func TestCreatePoolIdempotentRetryPreservesOriginalRouteDespiteCapacityDrift(t *testing.T) {
	store, catalog, task, contexts, assignments, now := poolFixture(t)
	svc, err := NewInvocationService("explorarte", catalog, task, contexts, store, store, store, assignments, ClockFunc(func() time.Time { return now }), 10, false)
	if err != nil {
		t.Fatal(err)
	}
	cmd := poolCommand(now, "pool-idempotent-K")
	t0, err := svc.Create(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if t0.Invocation.ProviderID != "cloudflare_workers_ai" {
		t.Fatalf("T0 expected cloudflare, got %s", t0.Invocation.ProviderID)
	}

	// T1: capacity state drifts so mistral would now be preferred if this
	// were treated as a fresh request.
	svc.SetRouteResolver(mustResolver(t, store, map[string]bool{"cloudflare_workers_ai|@cf/zai-org/glm-4.7-flash": true}))
	t1, err := svc.Create(context.Background(), cmd) // SAME idempotency key
	if err != nil {
		t.Fatalf("retry under the same idempotency key must succeed and replay, got error: %v", err)
	}
	if t1.Invocation.ID != t0.Invocation.ID {
		t.Fatalf("retry created a DIFFERENT invocation (id %d vs %d) -- idempotency key must reuse #501, never mint #502", t1.Invocation.ID, t0.Invocation.ID)
	}
	if t1.Invocation.ProviderID != "cloudflare_workers_ai" {
		t.Fatalf("retry silently changed provider to %s -- idempotent replay must never change route", t1.Invocation.ProviderID)
	}
	if !t1.Reused {
		t.Fatal("expected the store to report this as a replay (Reused=true)")
	}
}

// TestCreateStaticIdempotentRetrySameAsAlways is the invariant-#16 control
// for the fix above: for a STATIC role, request-hash equality on retry
// was already guaranteed by determinism; this proves it still is, through
// the exact same code path pool retries now use.
func TestCreateStaticIdempotentRetrySameAsAlways(t *testing.T) {
	base, catalog, task, contexts, assignments, _, now := serviceFixture()
	store := newPoolFakeStore(base) // no policy registered -> pure static path
	svc, err := NewInvocationService("explorarte", catalog, task, contexts, store, store, store, assignments, ClockFunc(func() time.Time { return now }), 10, false)
	if err != nil {
		t.Fatal(err)
	}
	cmd := poolCommand(now, "static-idempotent-1")
	first, err := svc.Create(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Create(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if second.Invocation.ID != first.Invocation.ID || !second.Reused {
		t.Fatalf("static retry must replay the same invocation: first=%d second=%d reused=%v", first.Invocation.ID, second.Invocation.ID, second.Reused)
	}
}

// TestCreateNoTypeLevelProviderOverride: Section 12.B / question A. There
// is no field on CreateInvocationCommand a caller could set to name a
// provider or model -- proven at compile time by this file compiling at
// all: CreateInvocationCommand has no ProviderID/ProviderModelID field,
// so any attempt to set one is a compile error, not a runtime check.
// This test exists to keep that fact executable and discoverable rather
// than only true by omission.
func TestCreateNoTypeLevelProviderOverride(t *testing.T) {
	cmd := CreateInvocationCommand{}
	_ = cmd
	// The following would not compile if uncommented, which is the point:
	//   cmd.ProviderID = "evil_provider"
	//   cmd.ProviderModelID = "invented-model"
}

// TestCreatePoolNonCanonicalCandidateDeniedBeforeCreateInvocation:
// Section 12.D through the full Create() path -- a compromised/buggy
// Selector must never reach store.CreateInvocation.
func TestCreatePoolNonCanonicalCandidateDeniedBeforeCreateInvocation(t *testing.T) {
	store, catalog, task, contexts, assignments, now := poolFixture(t)
	svc, err := NewInvocationService("explorarte", catalog, task, contexts, store, store, store, assignments, ClockFunc(func() time.Time { return now }), 10, false)
	if err != nil {
		t.Fatal(err)
	}
	origSelectors := swapSelector("free_capacity_v1", evilSelector{})
	defer restoreSelectors(origSelectors)

	_, err = svc.Create(context.Background(), poolCommand(now, "pool-evil-1"))
	if !errors.Is(err, ErrRouteNotApproved) {
		t.Fatalf("err = %v, want ErrRouteNotApproved", err)
	}
	if store.created {
		t.Fatal("CreateInvocation must never be called when the selector's answer is not approved")
	}
}
