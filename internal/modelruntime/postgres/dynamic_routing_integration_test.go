//go:build integration

package postgres_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

const (
	poolIntegrationPolicyID = "research.worker.integration"
	poolIntegrationRoleID   = "ingenieria_ia/code-runner"
)

// poolIntegrationPolicyYAML is the pool test-only fixture: test.fake/
// fake_adapter throughout, two candidates, zero real network. Inserted
// into a COPY of the real docs/canonical/model-routing.yaml -- see
// buildPoolCanonicalDir for why it is appended, never substituted whole.
const poolIntegrationPolicyYAML = `  research.worker.integration:
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
`

// canonicalDocumentNames are the 9 documents
// internal/organization/registry.Loader.readDocuments requires present and
// mutually consistent for Load (and therefore modelruntime.LoadCanonicalRouting,
// which calls it purely to obtain model-routing.yaml's semantic hash) to
// succeed at all. Listed here as plain filenames -- not parsing/validation
// logic -- so this test can copy them; the actual parsing/cross-document
// validation stays entirely inside the production loader.
var canonicalDocumentNames = []string{
	"organization.yaml", "role-catalog.yaml", "leader-worker-map.yaml",
	"model-routing.yaml", "model-egress-policy.yaml", "capability-matrix.yaml",
	"instruction-precedence.yaml", "decisions-required.yaml", "source-manifest.yaml",
}

// buildPoolCanonicalDir copies the REAL docs/canonical documents verbatim
// into a temp directory (the productive files on disk are never touched),
// then APPENDS -- never replaces -- poolIntegrationPolicyYAML into the
// copied model-routing.yaml.
//
// modelruntime.LoadCanonicalRouting requires all 9 canonical documents:
// role-catalog.yaml's roles are cross-validated against model-routing.yaml's
// policy set (internal/organization/registry/validation.go:
// role.model_policy_unknown), so a model-routing.yaml containing ONLY the
// pool test fixture would make every real role's model_policy reference
// fail to resolve, and Load() would return a ValidationError before this
// test ever reached BuildRegistryPlan. Appending the pool policy alongside
// the real ones keeps every real cross-reference intact -- the same shape
// production docs/canonical/model-routing.yaml would have if a pool policy
// were ever adopted there, which it deliberately is not in this round.
//
// The RoleRef this test passes to BuildRegistryPlan is a Go value
// constructed directly by the test (per FASE 1), never read back out of
// the copied role-catalog.yaml -- so role-catalog.yaml's actual content is
// exercised only for Load()'s own internal consistency, nothing else.
func buildPoolCanonicalDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	srcDir := filepath.Join("..", "..", "..", "docs", "canonical")
	for _, name := range canonicalDocumentNames {
		body, err := os.ReadFile(filepath.Join(srcDir, name))
		if err != nil {
			t.Fatalf("read real canonical document %s: %v", name, err)
		}
		if name == "model-routing.yaml" {
			const marker = "routing_invariants:"
			idx := strings.Index(string(body), marker)
			if idx < 0 {
				t.Fatalf("model-routing.yaml missing %q marker to insert the pool test fixture before", marker)
			}
			body = []byte(string(body)[:idx] + poolIntegrationPolicyYAML + string(body)[idx:])
		}
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
			t.Fatalf("write %s into temp canonical dir: %v", name, err)
		}
	}
	return dir
}

// TestDynamicCanonicalModelRoutingPostgreSQL is the closing composition
// gap: the FULL real chain, no hand-built RegistryPlan anywhere --
//
//	YAML (real docs/canonical + appended pool fixture)
//	  -> modelruntime.LoadCanonicalRouting (real parser)
//	  -> modelruntime.BuildRegistryPlan (real materialization)
//	  -> Store.ApplyRegistry (real SQL)
//	  -> live Postgres 17
//	  -> Store.GetRoutingPolicy/ListRoutingCandidates/GetCandidateRoute (real reads)
//	  -> DefaultRouteResolver -> ValidateResolvedRouteAgainstCanonical
//	  -> InvocationService.Create (real)
//	  -> InvocationService.Get (real readback)
//
// No provider API calls: fake_adapter/test.fake throughout. No production
// file is modified; docs/canonical/model-routing.yaml is read, never
// written.
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
	// this run's Provider/Profile rows (real statics + test.fake pool)
	// can never collide with anything syncModelCanonical already inserted.
	poolEgressHash := modelruntime.SHA256Bytes([]byte("dynamic-routing-e2e-egress-v1"))
	poolCapabilityHash := modelruntime.SHA256Bytes([]byte("dynamic-routing-e2e-capabilities-v1"))
	// revisionID/canonicalHash placeholder swapped for the real routing
	// hash once loaded below; insertFakeRoutingRevision just needs SOME
	// hash to seed organization_registry_revisions.document_hashes with.
	revisionID := insertFakeRoutingRevision(t, ctx, platform, "pending", poolEgressHash, poolCapabilityHash)

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

	// ============================================================
	// 1. YAML -> LoadCanonicalRouting -> BuildRegistryPlan (real, no hand-built plan)
	// ============================================================
	tmp := buildPoolCanonicalDir(t)
	routing, err := modelruntime.LoadCanonicalRouting(tmp)
	if err != nil {
		t.Fatalf("LoadCanonicalRouting (real parser + real cross-document validation): %v", err)
	}
	plan, err := modelruntime.BuildRegistryPlan(
		[]modelruntime.RoleRef{{ID: poolIntegrationRoleID, ModelPolicy: poolIntegrationPolicyID, Enabled: true, Executable: true}},
		modelruntime.OrganizationRef{ID: modelIntegrationOrganization, RevisionID: revisionID},
		routing,
	)
	if err != nil {
		t.Fatalf("BuildRegistryPlan (real materialization from the loaded YAML): %v", err)
	}

	// ============================================================
	// 2. Prove the plan BEFORE persisting it
	// ============================================================
	if plan.CanonicalHash != routing.Hash {
		t.Fatalf("plan.CanonicalHash = %s, want routing.Hash %s", plan.CanonicalHash, routing.Hash)
	}
	if len(plan.RoutingPolicies) != 1 {
		t.Fatalf("expected exactly 1 RoutingPolicy in the plan, got %d: %+v", len(plan.RoutingPolicies), plan.RoutingPolicies)
	}
	planPolicy := plan.RoutingPolicies[0]
	if planPolicy.PolicyID != poolIntegrationPolicyID || planPolicy.RoutingMode != modelruntime.RoutingModePool ||
		planPolicy.SelectorID != "free_capacity_v1" || planPolicy.AllowPaid {
		t.Fatalf("unexpected RoutingPolicy in the plan: %+v", planPolicy)
	}
	if len(plan.RoutingCandidates) != 2 {
		t.Fatalf("expected exactly 2 RoutingCandidates in the plan, got %d: %+v", len(plan.RoutingCandidates), plan.RoutingCandidates)
	}
	versionsByProfile := make(map[string]modelruntime.ProfileVersion, len(plan.Versions))
	for _, v := range plan.Versions {
		versionsByProfile[v.ProfileID] = v
	}
	seenHashes := map[string]bool{}
	for _, c := range plan.RoutingCandidates {
		if c.PolicyID != poolIntegrationPolicyID {
			t.Fatalf("candidate belongs to the wrong policy: %+v", c)
		}
		if c.ProviderID != "test.fake" || c.Transport != modelruntime.TransportFake {
			t.Fatalf("unexpected candidate provider/transport: %+v", c)
		}
		if c.ProfileID == "" || c.CandidateHash == "" {
			t.Fatalf("candidate missing ProfileID/CandidateHash: %+v", c)
		}
		if seenHashes[c.CandidateHash] {
			t.Fatalf("two candidates share the same CandidateHash: %+v", c)
		}
		seenHashes[c.CandidateHash] = true
		// The candidate's ProfileID must resolve to a version BuildRegistryPlan
		// itself synthesized (synthesizeCandidateProfileID) with matching
		// provider/model identity -- proves the plan's candidate list and
		// its profile/version materialization actually agree with each
		// other, not just that both independently look plausible.
		// ModelProfileVersionID is intentionally 0 at this stage: it is a
		// database-assigned id that does not exist until ApplyRegistry
		// inserts the row (see postgres/registry.go's versionIDs map) --
		// checked for real, post-insert, in stage 3 below.
		version, ok := versionsByProfile[c.ProfileID]
		if !ok {
			t.Fatalf("candidate ProfileID %q has no matching synthesized ProfileVersion in the plan", c.ProfileID)
		}
		if version.ProviderID != c.ProviderID || version.ProviderModelID != c.ProviderModelID {
			t.Fatalf("candidate %+v and its synthesized version %+v disagree on provider/model", c, version)
		}
	}
	// No candidate depends on a binding chosen from request JSON: this is
	// a structural guarantee, not a runtime check. RoutingCandidate/
	// RoutingPolicy carry no field a caller, an LLM response, or a
	// request body could populate -- provider/model/priority/
	// capacity_class above came exclusively from the YAML candidates list
	// BuildRegistryPlan parsed, verified field-by-field over the loop
	// above against the plan's own synthesized Versions.

	// ============================================================
	// 3. ApplyRegistry (real) -> PostgreSQL -> read back and compare
	// ============================================================
	applied, err := store.ApplyRegistry(ctx, plan, 10)
	if err != nil || !applied.Applied {
		t.Fatalf("ApplyRegistry(plan) = %+v, err=%v", applied, err)
	}
	reapplied, err := store.ApplyRegistry(ctx, plan, 10)
	if err != nil || !reapplied.NoOp {
		t.Fatalf("re-applying the identical plan must be a NoOp: %+v err=%v", reapplied, err)
	}

	dbPolicy, ok, err := store.GetRoutingPolicy(ctx, modelIntegrationOrganization, revisionID, poolIntegrationPolicyID)
	if err != nil || !ok {
		t.Fatalf("GetRoutingPolicy: ok=%v err=%v", ok, err)
	}
	if dbPolicy.RoutingMode != planPolicy.RoutingMode || dbPolicy.SelectorID != planPolicy.SelectorID ||
		dbPolicy.AllowPaid != planPolicy.AllowPaid || dbPolicy.CanonicalHash != planPolicy.CanonicalHash {
		t.Fatalf("Postgres-materialized policy disagrees with the plan: db=%+v plan=%+v", dbPolicy, planPolicy)
	}
	dbCandidates, err := store.ListRoutingCandidates(ctx, modelIntegrationOrganization, revisionID, poolIntegrationPolicyID)
	if err != nil || len(dbCandidates) != 2 {
		t.Fatalf("ListRoutingCandidates = %+v err=%v", dbCandidates, err)
	}
	planByCandidateHash := make(map[string]modelruntime.RoutingCandidate, 2)
	for _, c := range plan.RoutingCandidates {
		planByCandidateHash[c.CandidateHash] = c
	}
	for _, dbc := range dbCandidates {
		planC, ok := planByCandidateHash[dbc.CandidateHash]
		if !ok {
			t.Fatalf("Postgres candidate hash %s has no matching plan candidate", dbc.CandidateHash)
		}
		if dbc.ProviderID != planC.ProviderID || dbc.ProviderModelID != planC.ProviderModelID ||
			dbc.ProfileID != planC.ProfileID || dbc.CapacityClass != planC.CapacityClass || dbc.Priority != planC.Priority {
			t.Fatalf("Postgres-materialized candidate disagrees with the plan: db=%+v plan=%+v", dbc, planC)
		}
		if dbc.ModelProfileVersionID == 0 {
			t.Fatalf("Postgres-materialized candidate has no real ModelProfileVersionID: %+v", dbc)
		}
		route, routeErr := store.GetCandidateRoute(ctx, modelIntegrationOrganization, revisionID, dbc.ProfileID)
		if routeErr != nil {
			t.Fatalf("GetCandidateRoute(%s): %v", dbc.ProfileID, routeErr)
		}
		if route.Version.ID != dbc.ModelProfileVersionID || route.Version.ProviderID != dbc.ProviderID || route.Version.ProviderModelID != dbc.ProviderModelID {
			t.Fatalf("GetCandidateRoute(%s) = %+v, disagrees with ListRoutingCandidates entry %+v", dbc.ProfileID, route, dbc)
		}
	}

	// ============================================================
	// 4/5. InvocationService.Create (real, through DefaultRouteResolver
	// + ValidateResolvedRouteAgainstCanonical) -> full provenance
	// ============================================================
	catalog := catalogFixture{
		organization: modelruntime.OrganizationRef{ID: modelIntegrationOrganization, RevisionID: revisionID, ModelEgressPolicyHash: poolEgressHash, CapabilityMatrixHash: poolCapabilityHash},
		roles: map[string]modelruntime.RoleRef{
			poolIntegrationRoleID: {ID: poolIntegrationRoleID, ModelPolicy: poolIntegrationPolicyID, Enabled: true, Executable: true, AuthorityClass: "execution_service", UnitID: "ingenieria_ia"},
		},
	}
	task, snapshot := insertModelExecutionFixture(t, ctx, platform, revisionID, poolIntegrationRoleID, "dynamic-routing-e2e")
	contexts := &staticContextReader{ref: snapshot, rendered: []byte("safe integration context")}
	tasks := staticTaskReader{ref: task}
	_, _ = fixturePrincipalAndAssignment(t, ctx, dispatchStore, task, poolIntegrationRoleID, poolIntegrationRoleID, "dynamic-routing-e2e")

	// AlwaysAvailableCapacityState: this round does not test capacity
	// feedback (FASE 4) -- NewInvocationService's default RouteResolver
	// already uses it unless overridden, so no explicit wiring is needed
	// here; noted for clarity since the corrective-round instructions ask
	// for it by name.
	service, err := modelruntime.NewInvocationService(modelIntegrationOrganization, catalog, tasks, contexts, store, egressStore, identityStore, dispatchStore, modelruntime.ClockFunc(time.Now), 10, false)
	if err != nil {
		t.Fatal(err)
	}

	cmd := validInvocationCommand(task, snapshot, poolIntegrationRoleID, "dynamic-routing-e2e-1")
	first, err := service.Create(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	var selected *modelruntime.RoutingCandidate
	for i := range dbCandidates {
		if dbCandidates[i].ProviderID == first.Invocation.ProviderID && dbCandidates[i].ProviderModelID == first.Invocation.ProviderModelID {
			selected = &dbCandidates[i]
		}
	}
	if selected == nil {
		t.Fatalf("selected route %s/%s is not one of the materialized candidates %+v", first.Invocation.ProviderID, first.Invocation.ProviderModelID, dbCandidates)
	}
	if first.Invocation.ModelProfileVersionID != selected.ModelProfileVersionID {
		t.Fatalf("frozen ModelProfileVersionID %d does not match the selected candidate's %d", first.Invocation.ModelProfileVersionID, selected.ModelProfileVersionID)
	}
	if first.Invocation.RoutingMode != modelruntime.RoutingModePool ||
		first.Invocation.RoutingPolicyID != poolIntegrationPolicyID ||
		first.Invocation.RoutingSelectorID != "free_capacity_v1" ||
		first.Invocation.RoutingCandidateSetHash != planPolicy.CanonicalHash ||
		first.Invocation.RoutingCandidateHash != selected.CandidateHash ||
		first.Invocation.RoutingDecisionReason == "" {
		t.Fatalf("routing provenance on the created invocation is incomplete or wrong: %+v (want candidate_set_hash=%s candidate_hash=%s)", first.Invocation, planPolicy.CanonicalHash, selected.CandidateHash)
	}
	if first.Invocation.IdempotencyIntentHash == "" || first.Invocation.RequestHash == "" {
		t.Fatal("IdempotencyIntentHash/RequestHash must be set on every new invocation")
	}

	// ============================================================
	// InvocationService.Get -- real Postgres readback, identical to Create()
	// ============================================================
	got, err := service.Get(ctx, first.Invocation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProviderID != first.Invocation.ProviderID || got.ProviderModelID != first.Invocation.ProviderModelID ||
		got.ModelProfileVersionID != first.Invocation.ModelProfileVersionID ||
		got.RoutingMode != first.Invocation.RoutingMode || got.RoutingPolicyID != first.Invocation.RoutingPolicyID ||
		got.RoutingSelectorID != first.Invocation.RoutingSelectorID ||
		got.RoutingCandidateSetHash != first.Invocation.RoutingCandidateSetHash ||
		got.RoutingCandidateHash != first.Invocation.RoutingCandidateHash ||
		got.RoutingDecisionReason != first.Invocation.RoutingDecisionReason ||
		got.IdempotencyIntentHash != first.Invocation.IdempotencyIntentHash ||
		got.RequestHash != first.Invocation.RequestHash {
		t.Fatalf("Get() readback does not match what Create() returned:\n  created=%+v\n  got=%+v", first.Invocation, got)
	}

	// ============================================================
	// 6. Idempotent replay: the YAML->plan->DB path does not break it
	// ============================================================
	replay, err := service.Create(ctx, cmd)
	if err != nil {
		t.Fatalf("idempotent replay must succeed: %v", err)
	}
	if !replay.Reused || replay.Invocation.ID != first.Invocation.ID {
		t.Fatalf("replay = %+v, want Reused=true and the same invocation ID as %d", replay, first.Invocation.ID)
	}
	if replay.Invocation.RequestHash != first.Invocation.RequestHash ||
		replay.Invocation.IdempotencyIntentHash != first.Invocation.IdempotencyIntentHash ||
		replay.Invocation.ProviderID != first.Invocation.ProviderID ||
		replay.Invocation.ProviderModelID != first.Invocation.ProviderModelID ||
		replay.Invocation.RoutingCandidateHash != first.Invocation.RoutingCandidateHash {
		t.Fatal("replay must return the ORIGINAL route/provenance, never a recomputed one")
	}
}

// TestCurrentCanonicalRoutingStaysBackwardCompatibleWithPool is FASE 7's
// smoke: the REAL, unmodified docs/canonical/model-routing.yaml -- which
// today contains only static policies -- still loads and materializes
// correctly through the exact same LoadCanonicalRouting/BuildRegistryPlan
// path the pool test above exercises. No file is modified; no policy is
// converted to routing_mode: pool; no provider is called. Pure parser +
// BuildRegistryPlan, no Postgres needed (FASE 7 explicitly allows this).
func TestCurrentCanonicalRoutingStaysBackwardCompatibleWithPool(t *testing.T) {
	canonicalDir := filepath.Join("..", "..", "..", "docs", "canonical")
	routing, err := modelruntime.LoadCanonicalRouting(canonicalDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := routing.Policies[poolIntegrationPolicyID]; ok {
		t.Fatalf("the real canonical document must NOT contain the test-only pool policy %q", poolIntegrationPolicyID)
	}
	for policyID, policy := range routing.Policies {
		if policy.RoutingMode == modelruntime.RoutingModePool {
			t.Fatalf("the real canonical document must not contain any pool policy yet, found one at %q", policyID)
		}
	}

	roles := []modelruntime.RoleRef{
		{ID: "empresa/ceo", ModelPolicy: "executive.ceo", Enabled: true, Executable: true},
		{ID: "ingenieria_ia/orquestador", ModelPolicy: "department.leader", Enabled: true, Executable: true},
		{ID: poolIntegrationRoleID, ModelPolicy: "department.worker", Enabled: true, Executable: true},
	}
	plan, err := modelruntime.BuildRegistryPlan(roles, modelruntime.OrganizationRef{ID: modelIntegrationOrganization, RevisionID: 1}, routing)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.RoutingPolicies) != 0 || len(plan.RoutingCandidates) != 0 {
		t.Fatalf("a document with no pool policies must produce zero RoutingPolicies/RoutingCandidates, got %d/%d", len(plan.RoutingPolicies), len(plan.RoutingCandidates))
	}
	if len(plan.Bindings) != len(roles) {
		t.Fatalf("every static role must still receive exactly one RoleBinding, got %d bindings for %d roles", len(plan.Bindings), len(roles))
	}
	for _, b := range plan.Bindings {
		if !b.Active {
			t.Fatalf("enabled+executable role got an inactive binding: %+v", b)
		}
	}
}
