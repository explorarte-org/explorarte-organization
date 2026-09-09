//go:build integration

package postgres_test

import (
	"context"
	"crypto/ed25519"
	"errors"
	"path/filepath"
	"testing"
	"time"

	dispatchpostgres "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
	egresspostgres "github.com/Mireuz13/explorarte-organization/internal/modelegress/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelidentity"
	identitypostgres "github.com/Mireuz13/explorarte-organization/internal/modelidentity/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelrouting"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/adapter"
	modelpostgres "github.com/Mireuz13/explorarte-organization/internal/modelruntime/postgres"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

// TestDynamicCanonicalModelRoutingUsesRealCapacityState is Model Capacity
// State V1's Section 13-16 closing E2E: no hand-built capacity state
// anywhere -- every state change below comes from a REAL ProviderOutcome
// produced by a real DispatchService.Dispatch() call against the exact
// two-candidate pool fixture (poolIntegrationPolicyYAML,
// poolIntegrationPolicyID/poolIntegrationRoleID) TestDynamicCanonicalModelRoutingPostgreSQL
// already established: candidate A = test.fake/pool-candidate-a
// (free_daily, priority 10, preferred), candidate B =
// test.fake/pool-candidate-b (credit_monthly, priority 20).
//
//	K1 created -> selects A (both healthy, A has lower priority)
//	Dispatch(K1) with a retryable 500  -> real insertProviderOutcome ->
//	    ClassifyCapacityFeedback -> Cooldown -> model_routing_capacity_state(A)
//	K2 created (new idempotency key) -> CapacityStateReader reads A in
//	    cooldown -> FreeCapacityV1 discards A -> selects B
//	K1 is re-read and must still point at A -- never mutated
//	cooldown_until for A is moved into the past directly in PostgreSQL
//	    (Section 14 explicitly allows this -- it is moving the clock
//	    forward for the TEST, not faking the feedback mechanism)
//	K3 created -> A eligible again -> selects A (still the preferred
//	    candidate); Dispatch(K3) SUCCEEDS -> healthy feedback clears A's
//	    (already-expired) cooldown for real (Section 15 recovery)
//	K4/K5 created and dispatched with retryable failures -> A AND B both
//	    now in cooldown
//	K6 created -> RouteResolver has no eligible candidate ->
//	    modelrouting.ErrNoCapacity, and no new model_invocations row is
//	    ever persisted for K6's idempotency key (Section 16)
//
// No provider API calls anywhere: fake_adapter/test.fake throughout, exactly
// like TestDynamicCanonicalModelRoutingPostgreSQL.
func TestDynamicCanonicalModelRoutingUsesRealCapacityState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	platform := openModelStore(t, ctx)
	defer platform.Close()
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		t.Fatalf("migrations through 000072: %v", err)
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

	poolEgressHash := modelruntime.SHA256Bytes([]byte("capacity-state-e2e-egress-v1"))
	poolCapabilityHash := modelruntime.SHA256Bytes([]byte("capacity-state-e2e-capabilities-v1"))
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

	tmp := buildPoolCanonicalDir(t)
	routing, err := modelruntime.LoadCanonicalRouting(tmp)
	if err != nil {
		t.Fatalf("LoadCanonicalRouting: %v", err)
	}
	plan, err := modelruntime.BuildRegistryPlan(
		[]modelruntime.RoleRef{{ID: poolIntegrationRoleID, ModelPolicy: poolIntegrationPolicyID, Enabled: true, Executable: true}},
		modelruntime.OrganizationRef{ID: modelIntegrationOrganization, RevisionID: revisionID},
		routing,
	)
	if err != nil {
		t.Fatalf("BuildRegistryPlan: %v", err)
	}
	if applied, applyErr := store.ApplyRegistry(ctx, plan, 10); applyErr != nil || !applied.Applied {
		t.Fatalf("ApplyRegistry(plan) = %+v, err=%v", applied, applyErr)
	}

	catalog := catalogFixture{
		organization: modelruntime.OrganizationRef{ID: modelIntegrationOrganization, RevisionID: revisionID, ModelEgressPolicyHash: poolEgressHash, CapabilityMatrixHash: poolCapabilityHash},
		roles: map[string]modelruntime.RoleRef{
			poolIntegrationRoleID: {ID: poolIntegrationRoleID, ModelPolicy: poolIntegrationPolicyID, Enabled: true, Executable: true, AuthorityClass: "execution_service", UnitID: "ingenieria_ia"},
		},
	}
	task, snapshot := insertModelExecutionFixture(t, ctx, platform, revisionID, poolIntegrationRoleID, "capacity-state-e2e")
	contexts := &staticContextReader{ref: snapshot, rendered: []byte("safe integration context")}
	tasks := staticTaskReader{ref: task}
	principal, _ := fixturePrincipalAndAssignment(t, ctx, dispatchStore, task, poolIntegrationRoleID, poolIntegrationRoleID, "capacity-state-e2e")

	identityPrivateKey, identityKeyFile := writeExecutionIdentityKeyFile(t)
	identityPublicKey := identityPrivateKey.Public().(ed25519.PublicKey)
	preparedIdentityKey := modelidentity.PreparedKey{OrganizationID: modelIntegrationOrganization, ExecutionPrincipalID: principal.ID, PublicKey: identityPublicKey, PublicKeyFingerprint: modelidentity.PublicKeyFingerprint(identityPublicKey), SecretRef: "file://model-execution/capacity-state-e2e/key-1", IdempotencyKey: "capacity-state-e2e-identity-key", CreatedByRoleID: "empresa/human"}
	preparedIdentityKey.RequestHash, err = modelidentity.KeyRequestHash(preparedIdentityKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = identityStore.RegisterKey(ctx, preparedIdentityKey); err != nil {
		t.Fatal(err)
	}
	identityService, err := modelidentity.NewChallengeService(identityStore, modelidentity.ClockFunc(time.Now))
	if err != nil {
		t.Fatal(err)
	}

	cfg := modelruntime.RuntimeConfig{Enabled: true, CommandTimeout: 30 * time.Second, GlobalConcurrency: 4, MaxResponseBytes: 1 << 20, MaxToolIntents: 8, ClaimTTL: time.Minute, ReconcileBatchSize: 100, OutboxMaxAttempts: 10, ExecutionPrincipalKey: principal.PrincipalKey, ExecutionIdentityEnabled: true, ExecutionIdentityKeyFile: identityKeyFile}
	capabilityEvaluator := allowEvaluator{matrixHash: poolCapabilityHash}

	service, err := modelruntime.NewInvocationService(modelIntegrationOrganization, catalog, tasks, contexts, store, egressStore, identityStore, dispatchStore, modelruntime.ClockFunc(time.Now), 10, false)
	if err != nil {
		t.Fatal(err)
	}

	// dispatchWith builds a fresh DispatchService wired to the given adapter
	// (a new one each time: classifiedAdapter is single-shot per instance,
	// and this keeps each dispatch's outcome fixture explicit at its own
	// call site rather than shared/mutated state).
	dispatchWith := func(t *testing.T, provider modelruntime.ProviderAdapter) *modelruntime.DispatchService {
		t.Helper()
		d, err := modelruntime.NewDispatchService(modelIntegrationOrganization, cfg, catalog, tasks, contexts, capabilityEvaluator, egressStore, modelegress.NewEvaluator(), store, dispatchStore, dispatchStore, identityService, store, adapter.NewRegistry(provider), modelruntime.ClockFunc(time.Now))
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	retryable500 := func() *classifiedAdapter {
		return &classifiedAdapter{phase: modelruntime.AdapterFailureResponseReceived, outcome: modelruntime.ProviderOutcome{OutcomeClassification: modelruntime.ProviderOutcomeRejected, HTTPStatus: 500, ErrorClass: "transport", ErrorCode: "http_error", Retryable: true, ResponseHash: modelruntime.SHA256Bytes([]byte("capacity-e2e-500")), ResponseSchemaVersion: "test.fake.response.v1"}}
	}

	// ============================================================
	// K1: created (selects A) -> real retryable-500 dispatch -> A cools down
	// ============================================================
	k1cmd := validInvocationCommand(task, snapshot, poolIntegrationRoleID, "capacity-e2e-k1")
	k1, err := service.Create(ctx, k1cmd)
	if err != nil {
		t.Fatal(err)
	}
	if k1.Invocation.ProviderModelID != "pool-candidate-a" {
		t.Fatalf("K1 must select candidate A (lower priority, both healthy), got %q", k1.Invocation.ProviderModelID)
	}
	if result, dispatchErr := dispatchWith(t, retryable500()).Dispatch(ctx, k1.Invocation.ID); dispatchErr == nil || result.Invocation.Status != modelruntime.InvocationFailed {
		t.Fatalf("K1 dispatch = %+v err=%v, want a failed retryable-500 outcome", result, dispatchErr)
	}

	stateA, err := store.CapacityState(ctx, modelIntegrationOrganization, "test.fake", "pool-candidate-a")
	if err != nil {
		t.Fatal(err)
	}
	if stateA.CooldownUntil == nil || !time.Now().Before(*stateA.CooldownUntil) {
		t.Fatalf("candidate A must be in an active cooldown after a real retryable-500 outcome, got %+v", stateA)
	}
	if stateA.Disabled || stateA.QuotaExhausted {
		t.Fatalf("a retryable 500 must never set Disabled or QuotaExhausted: %+v", stateA)
	}

	// ============================================================
	// K2: new idempotency key -> A in cooldown -> selects B
	// ============================================================
	k2cmd := validInvocationCommand(task, snapshot, poolIntegrationRoleID, "capacity-e2e-k2")
	k2, err := service.Create(ctx, k2cmd)
	if err != nil {
		t.Fatal(err)
	}
	if k2.Invocation.ProviderModelID != "pool-candidate-b" {
		t.Fatalf("K2 must fall over to candidate B while A is cooling down, got %q", k2.Invocation.ProviderModelID)
	}

	// K1 must never be mutated by K2's creation or by A's cooldown.
	k1Reread, err := service.Get(ctx, k1.Invocation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if k1Reread.ProviderModelID != "pool-candidate-a" || k1Reread.ID != k1.Invocation.ID {
		t.Fatalf("K1 changed after K2 was created: %+v", k1Reread)
	}

	// ============================================================
	// Cooldown expiry: move A's cooldown_until into the past (Section 14 --
	// moving the test clock forward in PostgreSQL, not faking the feedback
	// mechanism), then prove A becomes selectable again.
	// ============================================================
	if _, err = platform.Pool().Exec(ctx, `
UPDATE model_routing_capacity_state
SET cooldown_until = clock_timestamp() - interval '1 minute'
WHERE organization_id=$1 AND provider_id='test.fake' AND provider_model_id='pool-candidate-a'`, modelIntegrationOrganization); err != nil {
		t.Fatal(err)
	}

	k3cmd := validInvocationCommand(task, snapshot, poolIntegrationRoleID, "capacity-e2e-k3")
	k3, err := service.Create(ctx, k3cmd)
	if err != nil {
		t.Fatal(err)
	}
	if k3.Invocation.ProviderModelID != "pool-candidate-a" {
		t.Fatalf("K3 must select A again once its cooldown has expired, got %q", k3.Invocation.ProviderModelID)
	}

	// ============================================================
	// Section 15: a real SUCCESSFUL dispatch for K3 (still on A) must
	// clear the (already expired, but still non-NULL) cooldown for real.
	// ============================================================
	if result, dispatchErr := dispatchWith(t, adapter.NewFake()).Dispatch(ctx, k3.Invocation.ID); dispatchErr != nil || result.Invocation.Status != modelruntime.InvocationSucceeded {
		t.Fatalf("K3 dispatch = %+v err=%v, want success", result, dispatchErr)
	}
	stateA, err = store.CapacityState(ctx, modelIntegrationOrganization, "test.fake", "pool-candidate-a")
	if err != nil {
		t.Fatal(err)
	}
	if stateA.CooldownUntil != nil || stateA.QuotaExhausted {
		t.Fatalf("a real successful outcome must clear cooldown/quota_exhausted: %+v", stateA)
	}

	// ============================================================
	// Section 16: degrade BOTH candidates for real, then prove ErrNoCapacity
	// and that no invocation is created when nothing is eligible.
	// ============================================================
	k4cmd := validInvocationCommand(task, snapshot, poolIntegrationRoleID, "capacity-e2e-k4")
	k4, err := service.Create(ctx, k4cmd)
	if err != nil {
		t.Fatal(err)
	}
	if k4.Invocation.ProviderModelID != "pool-candidate-a" {
		t.Fatalf("K4 must select A (healthy again after K3's success), got %q", k4.Invocation.ProviderModelID)
	}
	if result, dispatchErr := dispatchWith(t, retryable500()).Dispatch(ctx, k4.Invocation.ID); dispatchErr == nil || result.Invocation.Status != modelruntime.InvocationFailed {
		t.Fatalf("K4 dispatch = %+v err=%v, want a failed retryable-500 outcome", result, dispatchErr)
	}

	k5cmd := validInvocationCommand(task, snapshot, poolIntegrationRoleID, "capacity-e2e-k5")
	k5, err := service.Create(ctx, k5cmd)
	if err != nil {
		t.Fatal(err)
	}
	if k5.Invocation.ProviderModelID != "pool-candidate-b" {
		t.Fatalf("K5 must fall over to B (A just cooled down again), got %q", k5.Invocation.ProviderModelID)
	}
	if result, dispatchErr := dispatchWith(t, retryable500()).Dispatch(ctx, k5.Invocation.ID); dispatchErr == nil || result.Invocation.Status != modelruntime.InvocationFailed {
		t.Fatalf("K5 dispatch = %+v err=%v, want a failed retryable-500 outcome", result, dispatchErr)
	}

	var invocationCountBefore int
	if err = platform.Pool().QueryRow(ctx, `SELECT count(*) FROM model_invocations WHERE organization_id=$1 AND idempotency_key='capacity-e2e-k6'`, modelIntegrationOrganization).Scan(&invocationCountBefore); err != nil {
		t.Fatal(err)
	}
	if invocationCountBefore != 0 {
		t.Fatalf("K6's idempotency key must not already exist, got %d rows", invocationCountBefore)
	}

	k6cmd := validInvocationCommand(task, snapshot, poolIntegrationRoleID, "capacity-e2e-k6")
	_, err = service.Create(ctx, k6cmd)
	if !errors.Is(err, modelrouting.ErrNoCapacity) {
		t.Fatalf("K6 must fail with modelrouting.ErrNoCapacity when both candidates are unavailable, got %v", err)
	}

	var invocationCountAfter int
	if err = platform.Pool().QueryRow(ctx, `SELECT count(*) FROM model_invocations WHERE organization_id=$1 AND idempotency_key='capacity-e2e-k6'`, modelIntegrationOrganization).Scan(&invocationCountAfter); err != nil {
		t.Fatal(err)
	}
	if invocationCountAfter != 0 {
		t.Fatalf("ErrNoCapacity must never persist a new invocation, found %d rows for K6's idempotency key", invocationCountAfter)
	}
}
