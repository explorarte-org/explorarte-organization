//go:build integration

package postgres_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	dispatchpostgres "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/postgres"
	egresspostgres "github.com/Mireuz13/explorarte-organization/internal/modelegress/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelidentity"
	identitypostgres "github.com/Mireuz13/explorarte-organization/internal/modelidentity/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	modelpostgres "github.com/Mireuz13/explorarte-organization/internal/modelruntime/postgres"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

// poolIntegrationRoutingDoc materializes two candidates under the SAME
// provider (test.fake) with different models -- fake_adapter transport,
// zero real network, per Closure 5's "no provider API calls". Two models
// under one provider is enough to prove real pool materialization and
// selection without needing a second real provider's egress rules wired
// up for this fixture.
const poolIntegrationRoutingDoc = `schema_version: 1
document_status: test
policies:
  research.worker.integration:
    routing_mode: pool
    selector: free_capacity_v1
    allow_paid: false
    capabilities:
      - structured.output
    candidates:
      - provider: test.fake
        model: pool-candidate-a
        transport: fake_adapter
        capacity_class: free_daily
        priority: 10
      - provider: test.fake
        model: pool-candidate-b
        transport: fake_adapter
        capacity_class: credit_monthly
        priority: 20
routing_invariants:
  - roles do not choose models
`

// poolIntegrationRegistryPlan hand-builds the same RegistryPlan
// BuildRegistryPlan would produce for poolIntegrationRoutingDoc above --
// see the comment at its call site for why this test constructs the plan
// directly rather than round-tripping through YAML (mirrors the existing
// fakeRegistryPlan/fakeEgressPlan pattern in integration_test.go).
func poolIntegrationRegistryPlan(revisionID int64, routingHash string) modelruntime.RegistryPlan {
	const policyID = "research.worker.integration"
	capabilities := []modelruntime.ModelCapability{"structured.output"}
	profileA, profileB := policyID+"~pool~0", policyID+"~pool~1"

	makeVersion := func(profileID, model string) modelruntime.ProfileVersion {
		return modelruntime.ProfileVersion{
			OrganizationID: modelIntegrationOrganization, ProfileID: profileID, OrganizationRevisionID: revisionID,
			CanonicalDocumentHash: routingHash, VersionHash: modelruntime.SHA256Bytes([]byte("version-" + profileID)),
			ProviderID: "test.fake", ProviderModelID: model, Transport: modelruntime.TransportFake,
			AdapterStatus: modelruntime.AdapterAvailable, DispatchEnabled: true,
		}
	}
	return modelruntime.RegistryPlan{
		OrganizationID: modelIntegrationOrganization, OrganizationRevisionID: revisionID, CanonicalHash: routingHash,
		Providers: []modelruntime.Provider{{
			OrganizationID: modelIntegrationOrganization, ID: "test.fake", Transport: modelruntime.TransportFake,
			AdapterStatus: modelruntime.AdapterAvailable, DispatchEnabled: true,
			CanonicalHash: modelruntime.SHA256Bytes([]byte("fake-provider-" + routingHash)), OrganizationRevisionID: revisionID,
		}},
		Profiles: []modelruntime.Profile{
			{OrganizationID: modelIntegrationOrganization, ID: profileA, PolicyID: profileA},
			{OrganizationID: modelIntegrationOrganization, ID: profileB, PolicyID: profileB},
		},
		Versions: []modelruntime.ProfileVersion{makeVersion(profileA, "pool-candidate-a"), makeVersion(profileB, "pool-candidate-b")},
		CapabilitySnapshots: []modelruntime.CapabilitySnapshot{
			{OrganizationID: modelIntegrationOrganization, ProfileID: profileA, Capabilities: capabilities, CapabilityHash: modelruntime.SHA256Bytes([]byte("caps-" + profileA))},
			{OrganizationID: modelIntegrationOrganization, ProfileID: profileB, Capabilities: capabilities, CapabilityHash: modelruntime.SHA256Bytes([]byte("caps-" + profileB))},
		},
		RoutingPolicies: []modelruntime.RoutingPolicy{{
			OrganizationID: modelIntegrationOrganization, OrganizationRevisionID: revisionID, PolicyID: policyID,
			RoutingMode: modelruntime.RoutingModePool, SelectorID: "free_capacity_v1", AllowPaid: false,
			CanonicalHash: modelruntime.SHA256Bytes([]byte("routing-policy-" + routingHash)),
		}},
		RoutingCandidates: []modelruntime.RoutingCandidate{
			{OrganizationID: modelIntegrationOrganization, OrganizationRevisionID: revisionID, PolicyID: policyID,
				ProviderID: "test.fake", ProviderModelID: "pool-candidate-a", Transport: modelruntime.TransportFake,
				CapacityClass: "free_daily", Priority: 10, ProfileID: profileA,
				CandidateHash: modelruntime.SHA256Bytes([]byte("candidate-" + profileA))},
			{OrganizationID: modelIntegrationOrganization, OrganizationRevisionID: revisionID, PolicyID: policyID,
				ProviderID: "test.fake", ProviderModelID: "pool-candidate-b", Transport: modelruntime.TransportFake,
				CapacityClass: "credit_monthly", Priority: 20, ProfileID: profileB,
				CandidateHash: modelruntime.SHA256Bytes([]byte("candidate-" + profileB))},
		},
	}
}

// TestDynamicCanonicalModelRoutingPostgreSQL is Closure 5: a real Postgres
// integration test for the pool half of Dynamic Canonical Model Routing --
// migrations through 000071, BuildRegistryPlan/ApplyRegistry materializing
// routing_policies/routing_candidates for real, GetRoutingPolicy/
// ListRoutingCandidates/GetCandidateRoute reading them back,
// InvocationService.Create resolving and persisting a real Invocation with
// full provenance, idempotent replay, concurrency over the real
// ON CONFLICT(organization_id, idempotency_key) constraint, and a
// distinct-key fallback producing a second Invocation. No provider API
// calls: fake_adapter/test.fake throughout.
func TestDynamicCanonicalModelRoutingPostgreSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	platform := openModelStore(t, ctx)
	defer platform.Close()
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		t.Fatalf("migrations through 000071: %v", err)
	}
	resetModelSchema(t, ctx, platform)
	syncModelCanonical(t, ctx, platform)

	store, err := modelpostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}
	dispatchStore, err := dispatchpostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}
	identityStore, err := identitypostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}
	egressStore, err := egresspostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}

	// A fresh revision, isolated from whatever the real canonical
	// documents materialized under the organization's current one -- so
	// this pool policy's own Provider/Profile rows (test.fake) can never
	// collide with anything real sync already inserted.
	poolRoutingHash := modelruntime.SHA256Bytes([]byte("dynamic-routing-integration-v1"))
	poolEgressHash := modelruntime.SHA256Bytes([]byte("dynamic-routing-integration-egress-v1"))
	poolCapabilityHash := modelruntime.SHA256Bytes([]byte("dynamic-routing-integration-capabilities-v1"))
	revisionID := insertFakeRoutingRevision(t, ctx, platform, poolRoutingHash, poolEgressHash, poolCapabilityHash)

	if _, err = egressStore.Apply(ctx, fakeEgressPlan(revisionID, poolEgressHash)); err != nil {
		t.Fatalf("apply fixture egress policy: %v", err)
	}
	identityCanonical, err := modelidentity.LoadCanonicalPolicy(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = identityStore.Apply(ctx, modelIntegrationOrganization, identityCanonical); err != nil {
		t.Fatal(err)
	}

	// --- ApplyRegistry: real pool materialization ---
	//
	// BuildRegistryPlan's pool logic (YAML parsing, validation, candidate
	// materialization into distinct profiles) is already exercised
	// end-to-end against docs/canonical-shaped fixtures in
	// internal/modelruntime/canonical_routing_pool_test.go
	// (TestBuildRegistryPlanPoolMaterializesDistinctCandidateProfiles and
	// siblings). modelruntime.LoadCanonicalRouting additionally requires a
	// full canonical directory (organization.yaml and friends) that this
	// package cannot construct without either duplicating
	// docs/canonical or reaching into modelruntime's unexported
	// loadCanonicalRoutingDocument (unavailable from the external
	// postgres_test package). This test instead hand-builds the
	// RegistryPlan the same way fakeRegistryPlan already does for the
	// static test.fake fixture above, and focuses on what BuildRegistryPlan
	// itself cannot prove: ApplyRegistry's real SQL, the read-back APIs,
	// and InvocationService.Create against a live database.
	plan := poolIntegrationRegistryPlan(revisionID, poolRoutingHash)
	if len(plan.RoutingPolicies) != 1 || len(plan.RoutingCandidates) != 2 {
		t.Fatalf("expected 1 pool policy / 2 candidates in the plan, got %d/%d", len(plan.RoutingPolicies), len(plan.RoutingCandidates))
	}
	applied, err := store.ApplyRegistry(ctx, plan, 10)
	if err != nil || !applied.Applied || applied.RoutingPolicies != 1 || applied.RoutingCandidates != 2 {
		t.Fatalf("ApplyRegistry(pool plan) = %+v, err=%v", applied, err)
	}
	// Re-apply must be a true NoOp -- proves the routing_policies/
	// routing_candidates existence check in ApplyRegistry's NoOp branch is
	// real, not just inherited by accident from the pre-existing counts.
	reapplied, err := store.ApplyRegistry(ctx, plan, 10)
	if err != nil || !reapplied.NoOp {
		t.Fatalf("re-applying the identical pool plan must be a NoOp: %+v err=%v", reapplied, err)
	}

	// --- GetRoutingPolicy / ListRoutingCandidates / GetCandidateRoute ---
	policyID := "research.worker.integration"
	policy, ok, err := store.GetRoutingPolicy(ctx, modelIntegrationOrganization, revisionID, policyID)
	if err != nil || !ok || policy.RoutingMode != modelruntime.RoutingModePool || policy.SelectorID != "free_capacity_v1" {
		t.Fatalf("GetRoutingPolicy = %+v ok=%v err=%v", policy, ok, err)
	}
	candidates, err := store.ListRoutingCandidates(ctx, modelIntegrationOrganization, revisionID, policyID)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("ListRoutingCandidates = %+v err=%v", candidates, err)
	}
	for _, c := range candidates {
		route, routeErr := store.GetCandidateRoute(ctx, modelIntegrationOrganization, revisionID, c.ProfileID)
		if routeErr != nil {
			t.Fatalf("GetCandidateRoute(%s): %v", c.ProfileID, routeErr)
		}
		if route.Version.ProviderID != c.ProviderID || route.Version.ProviderModelID != c.ProviderModelID {
			t.Fatalf("GetCandidateRoute(%s) = %+v, does not match candidate %+v", c.ProfileID, route, c)
		}
	}

	// --- InvocationService.Create: real end-to-end dispatch of one pool invocation ---
	catalog := catalogFixture{
		organization: modelruntime.OrganizationRef{ID: modelIntegrationOrganization, RevisionID: revisionID, ModelEgressPolicyHash: poolEgressHash, CapabilityMatrixHash: poolCapabilityHash},
		roles: map[string]modelruntime.RoleRef{
			"ingenieria_ia/code-runner": {ID: "ingenieria_ia/code-runner", ModelPolicy: policyID, Enabled: true, Executable: true, AuthorityClass: "execution_service", UnitID: "ingenieria_ia"},
		},
	}
	task, snapshot := insertModelExecutionFixture(t, ctx, platform, revisionID, "ingenieria_ia/code-runner", "dynamic-routing-pool")
	contexts := &staticContextReader{ref: snapshot, rendered: []byte("safe integration context")}
	tasks := staticTaskReader{ref: task}
	_, _ = fixturePrincipalAndAssignment(t, ctx, dispatchStore, task, "ingenieria_ia/code-runner", "ingenieria_ia/code-runner", "dynamic-routing-pool")

	service, err := modelruntime.NewInvocationService(modelIntegrationOrganization, catalog, tasks, contexts, store, egressStore, identityStore, dispatchStore, modelruntime.ClockFunc(time.Now), 10, false)
	if err != nil {
		t.Fatal(err)
	}

	cmd := validInvocationCommand(task, snapshot, "ingenieria_ia/code-runner", "dynamic-routing-integration-1")
	first, err := service.Create(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if first.Invocation.ProviderID != "test.fake" || (first.Invocation.ProviderModelID != "pool-candidate-a" && first.Invocation.ProviderModelID != "pool-candidate-b") {
		t.Fatalf("unexpected selected route: %s/%s", first.Invocation.ProviderID, first.Invocation.ProviderModelID)
	}
	if first.Invocation.RoutingMode != modelruntime.RoutingModePool || first.Invocation.RoutingPolicyID != policyID || first.Invocation.RoutingSelectorID != "free_capacity_v1" {
		t.Fatalf("routing provenance not persisted on the row: mode=%s policy=%s selector=%s", first.Invocation.RoutingMode, first.Invocation.RoutingPolicyID, first.Invocation.RoutingSelectorID)
	}
	if first.Invocation.RoutingCandidateSetHash == "" || first.Invocation.RoutingCandidateHash == "" {
		t.Fatal("candidate-set/candidate hashes must be persisted")
	}
	if first.Invocation.IdempotencyIntentHash == "" {
		t.Fatal("idempotency_intent_hash must be set on every new invocation")
	}

	// --- Get readback: every pin must round-trip through real SQL ---
	got, err := service.Get(ctx, first.Invocation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProviderID != first.Invocation.ProviderID || got.ProviderModelID != first.Invocation.ProviderModelID ||
		got.ModelProfileVersionID != first.Invocation.ModelProfileVersionID ||
		got.RoutingMode != first.Invocation.RoutingMode || got.RoutingPolicyID != first.Invocation.RoutingPolicyID ||
		got.RoutingSelectorID != first.Invocation.RoutingSelectorID || got.RoutingCandidateSetHash != first.Invocation.RoutingCandidateSetHash ||
		got.RoutingCandidateHash != first.Invocation.RoutingCandidateHash || got.IdempotencyIntentHash != first.Invocation.IdempotencyIntentHash ||
		got.RequestHash != first.Invocation.RequestHash {
		t.Fatalf("Get() readback does not match what Create() returned:\n  created=%+v\n  got=%+v", first.Invocation, got)
	}

	// --- Same-key idempotent replay: real ON CONFLICT, original provenance ---
	replay, err := service.Create(ctx, cmd)
	if err != nil {
		t.Fatalf("idempotent replay must succeed: %v", err)
	}
	if !replay.Reused || replay.Invocation.ID != first.Invocation.ID {
		t.Fatalf("replay = %+v, want Reused=true and the same invocation ID as %d", replay, first.Invocation.ID)
	}
	if replay.Invocation.RequestHash != first.Invocation.RequestHash || replay.Invocation.ProviderID != first.Invocation.ProviderID {
		t.Fatal("replay must return the ORIGINAL provenance/route, never a recomputed one")
	}

	// --- Distinct-key fallback: a genuinely second Invocation ---
	secondCmd := validInvocationCommand(task, snapshot, "ingenieria_ia/code-runner", "dynamic-routing-integration-2")
	second, err := service.Create(ctx, secondCmd)
	if err != nil {
		t.Fatal(err)
	}
	if second.Invocation.ID == first.Invocation.ID {
		t.Fatal("a distinct idempotency key must produce a distinct Invocation")
	}

	// --- Concurrency over the real ON CONFLICT constraint ---
	concurrentCmd := validInvocationCommand(task, snapshot, "ingenieria_ia/code-runner", "dynamic-routing-integration-concurrent")
	var wg sync.WaitGroup
	results := make([]modelruntime.CreateInvocationResult, 4)
	errs := make([]error, 4)
	wg.Add(4)
	for i := 0; i < 4; i++ {
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = service.Create(ctx, concurrentCmd)
		}(i)
	}
	wg.Wait()
	var wins, losses int
	var wonID int64
	for i, r := range results {
		if errs[i] != nil {
			t.Fatalf("concurrent Create() call %d failed: %v", i, errs[i])
		}
		if r.Reused {
			losses++
		} else {
			wins++
			wonID = r.Invocation.ID
		}
	}
	if wins != 1 || losses != 3 {
		t.Fatalf("expected exactly 1 winner and 3 losers under the same idempotency key, got wins=%d losses=%d", wins, losses)
	}
	for i, r := range results {
		if r.Invocation.ID != wonID {
			t.Fatalf("call %d observed invocation ID %d, want the winner's %d -- every concurrent caller must see the SAME row", i, r.Invocation.ID, wonID)
		}
	}
	var rowCount int
	if err = platform.Pool().QueryRow(ctx, `SELECT count(*) FROM model_invocations WHERE organization_id=$1 AND idempotency_key=$2`, modelIntegrationOrganization, "dynamic-routing-integration-concurrent").Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if rowCount != 1 {
		t.Fatalf("exactly one row must exist for the concurrently-contended idempotency key, found %d", rowCount)
	}
}
