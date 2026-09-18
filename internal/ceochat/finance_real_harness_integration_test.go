//go:build integration

// FINANCE_HARNESS_RUNTIME_HOTFIX_V1: real-Postgres, real-Task-Engine,
// real-tasksauthority.Adapter, real-Model-Runtime proof that Finance's own
// ExecuteReviewTask -> runHarnessModel -> executionharness.Runtime path
// works end to end with MockOutput=nil -- never exercised by any test
// before this round, since every other Finance integration test in this
// package (finance_worker_integration_test.go) uses MockOutput to
// substitute for the model call itself.
//
// REAL_PROVIDER_CALLS is always 0 here: the "real Model Runtime" is
// dispatched through the repository's own deterministic
// internal/modelruntime/adapter.Fake ("test.fake"), registered via
// modelbootstrap.WithExtraAdapters exactly like
// canonical_dispatch_e2e_test.go already does for empresa/ceo. Fake's
// Dispatch is otherwise a fixed, non-configurable hash placeholder, which
// cannot satisfy FinanceReviewOutput's own JSON schema -- this round adds
// one small, additive, backward-compatible extraction to Fake.Dispatch
// (internal/modelruntime/adapter/fake.go) so a [fake-json-b64:...] marker
// anywhere in the rendered prompt makes it echo back a caller-chosen JSON
// payload instead. The marker is embedded via a real CampaignProposal's
// own Goal field (never Finance's own production prompt-rendering code,
// which is untouched) -- financeFakeJSONGoal below builds it.
package ceochat_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationpostgres "github.com/Mireuz13/explorarte-organization/internal/authorization/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/campaign/financeworker"
	campaignpostgres "github.com/Mireuz13/explorarte-organization/internal/campaign/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
	executionharnesspostgres "github.com/Mireuz13/explorarte-organization/internal/executionharness/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	modeldispatchbootstrap "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/adapter"
	modelbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelruntime/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// financeFakeJSONGoal marshals a FinanceReviewOutput and embeds it as a
// [fake-json-b64:...] marker inside otherwise-ordinary goal prose -- it
// flows into Finance's real, unmodified promptContent purely because
// runHarnessModel JSON-marshals the whole proposal (Goal included)
// verbatim. Never touches finance_service.go's own rendering.
func financeFakeJSONGoal(t *testing.T, output campaign.FinanceReviewOutput) string {
	t.Helper()
	body, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal fake finance output: %v", err)
	}
	return "Prove the real Finance Harness round trip. [fake-json-b64:" + base64.StdEncoding.EncodeToString(body) + "]"
}

// buildTestFinanceServiceRealHarness builds a REAL FinanceService with
// EVERY Harness execution dependency wired for real -- Authority
// (tasksauthority.Adapter, from modelRuntime.NewHarnessAuthority()),
// HarnessHistory/DescriptorStore (executionharnesspostgres.Store, the
// same production type cmd/orgctl/executive.go's own buildFinanceWorker
// uses), and NewModelExecutor (modelRuntime.NewHarnessModelExecutor,
// dispatching through the SAME real Model Runtime instance the caller's
// fixture opened with a test.fake adapter registered) -- mirroring
// buildTestFinanceService's own wiring in finance_worker_integration_test.go
// plus these four additional real dependencies that file never needed
// (every test there uses MockOutput). Duplicated rather than added as
// optional parameters to buildTestFinanceService itself, matching this
// package's own established convention of small, duplicated test helpers
// over shared ones with growing parameter lists (see
// finTestTaskCoordinator's and financeTestDispatchProvisioner's own doc
// comments).
func buildTestFinanceServiceRealHarness(t *testing.T, store *platformpostgres.Store, tasksSvc *tasks.Service, dispatch *modeldispatchbootstrap.Runtime, modelRuntime *modelbootstrap.Runtime, organizationID string) (svc *campaign.FinanceService, campStore *campaignpostgres.Store, reviewerRoleID string, revisionID int64, holderPrincipalID string, restore func()) {
	t.Helper()
	ctx := context.Background()

	campStore, err := campaignpostgres.New(store)
	if err != nil {
		t.Fatalf("open campaign store: %v", err)
	}
	registryRepo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatalf("open registry repository: %v", err)
	}
	authorizationStore, err := authorizationpostgres.New(store)
	if err != nil {
		t.Fatalf("open authorization store: %v", err)
	}
	authorizerPolicy, err := authorization.NewWithPolicyReader(authorizationStore, organizationID, filepath.Join("..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatalf("open capability authorizer: %v", err)
	}
	roleResolver := campaign.DefaultReviewerRoleResolver{Registry: registryRepo, Authorizer: authorizerPolicy}
	revision, err := registryRepo.GetCurrentRevision(ctx, organizationID)
	if err != nil || revision == nil {
		t.Fatalf("read current organization revision: revision=%+v err=%v", revision, err)
	}
	reviewerRoleID, err = roleResolver.ResolveReviewerRole(ctx, organizationID, revision.ID)
	if err != nil {
		t.Fatalf("resolve canonical finance reviewer role: %v", err)
	}
	restore = alignFinanceRoleForRealDispatch(t, store, organizationID, reviewerRoleID)

	financeAssignments, err := dispatch.NewAuthorizedAttemptProvisioner(chatTestDispatchPrincipalKey)
	if err != nil {
		t.Fatalf("create finance dispatch provisioner: %v", err)
	}
	roleBoundResolver, err := runtimeadapter.NewRoleBoundPrincipalResolver(dispatch.Store, organizationID)
	if err != nil {
		t.Fatalf("create role-bound principal resolver: %v", err)
	}
	financePrincipal, err := roleBoundResolver.Resolve(ctx, reviewerRoleID)
	if err != nil {
		t.Fatalf("resolve finance role-bound principal: %v", err)
	}
	holderPrincipalID = strconv.FormatInt(financePrincipal.ID, 10)

	harnessAuthority, err := modelRuntime.NewHarnessAuthority()
	if err != nil {
		t.Fatalf("create real harness authority: %v", err)
	}
	harnessHistory, err := executionharnesspostgres.New(store, organizationID)
	if err != nil {
		t.Fatalf("create real harness history/descriptor store: %v", err)
	}

	financeService, err := campaign.NewFinanceService(campaign.FinanceServiceConfig{
		OrganizationID:  organizationID,
		Store:           campStore,
		Tasks:           finTestTaskCoordinator{tasksSvc},
		Assignments:     financeTestDispatchProvisioner{financeAssignments},
		Authorizer:      authorizerPolicy,
		RoleResolver:    roleResolver,
		Authority:       harnessAuthority,
		HarnessHistory:  harnessHistory,
		DescriptorStore: harnessHistory,
		NewModelExecutor: func(execConfig modelruntimeadapter.Config) (executionharness.ModelExecutor, error) {
			return modelRuntime.NewHarnessModelExecutor(execConfig)
		},
		WorkerID:          "finance-worker-real-harness-test",
		HolderPrincipalID: holderPrincipalID,
		LeaseDuration:     2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("create finance service (real harness): %v", err)
	}
	return financeService, campStore, reviewerRoleID, revision.ID, holderPrincipalID, restore
}

// newFinanceRealHarnessFixture opens the same real Postgres/Task-Engine/
// dispatch stack newFinanceWorkerFixture does, but with a test.fake
// adapter registered in the underlying Model Runtime and a FinanceService
// wired with real Authority/HarnessHistory/DescriptorStore/
// NewModelExecutor -- the composition production's own
// cmd/orgctl/executive.go:buildFinanceWorker uses, minus only the real
// provider (test.fake stands in for it, REAL_PROVIDER_CALLS=0).
func newFinanceRealHarnessFixture(t *testing.T) (*chatFixture, *financeWorkerFixture, *modelbootstrap.Runtime) {
	t.Helper()
	f := newChatFixtureWithModelRuntimeOptions(t, modelbootstrap.WithExtraAdapters(adapter.NewFake()))
	dispatch := buildTestModelDispatch(t, f.store, f.runtime.Tasks, chatTestOrganization)
	financeService, campStore, reviewerRoleID, revisionID, holderPrincipalID, _ := buildTestFinanceServiceRealHarness(t, f.store, f.runtime.Tasks, dispatch, f.runtime.ModelRuntime, chatTestOrganization)
	return f, &financeWorkerFixture{
		store: campStore, financeService: financeService, reviewerRoleID: reviewerRoleID,
		revisionID: revisionID, tasksService: f.runtime.Tasks, holderPrincipalID: holderPrincipalID,
	}, f.runtime.ModelRuntime
}

// TestRealFinanceHarnessIntegration_MockOutputNilReachesRealModelDispatch is
// round sections 17-19: FinanceService.ExecuteReviewTask with
// MockOutput=nil, driving the REAL executionharness.Runtime, REAL
// tasksauthority.Adapter (proving lease/role/principal authority passes --
// section 16's positive case), REAL dispatch assignment provisioning, and
// a REAL Model Runtime dispatch attempt through test.fake.
//
// NEWLY DISCOVERED, SEPARATE BLOCKER (found only by actually driving this
// real round trip -- no test before this round ever did): Finance's
// RunSpec.Context.ID is a fabricated string
// (fmt.Sprintf("context-finrev-%d", ...)), never a real Model Runtime
// context-snapshot ID. internal/executionharness/modelruntimeadapter's
// REAL Adapter.Invoke (the one production's real Model Runtime executor
// actually uses -- modelruntimeadapter.Adapter, not a scripted
// ModelExecutor) requires Context.ID to parse as a positive int64
// referencing an existing context-engine snapshot row, and rejects
// anything else with "initial context ID must be a positive model runtime
// snapshot ID" (internal/executionharness/modelruntimeadapter/adapter.go).
// ceochat and Executive both satisfy this by calling a real
// ContextBuilder (contextengine.Service + contextcompiler.
// ContextAssemblyService, see internal/executive/runtimeadapter/context.go)
// BEFORE constructing their RunSpec -- campaign.FinanceServiceConfig has
// no equivalent dependency today, in production or in this branch.
//
// This is downstream of, and distinct from, the nil-ToolCatalog/
// ToolExecutor defect this round's hotfix actually fixes: production task
// 769 never reached this point (it failed earlier, at harness
// construction). Wiring Finance into the Context Engine is a real,
// necessary follow-up -- registering campaign proposal review content as
// a retrievable context-engine source is itself a non-trivial integration,
// not a hotfix-sized change, so it is intentionally NOT attempted in this
// round. This test pins the exact failure so the follow-up has a
// reproducible starting point, and proves everything upstream of it (real
// lease authority, real dispatch, real Harness construction, a real
// invocation actually reaching Model Runtime) already works correctly.
func TestRealFinanceHarnessIntegration_MockOutputNilReachesRealModelDispatch(t *testing.T) {
	f, fx, _ := newFinanceRealHarnessFixture(t)
	defer f.cleanup()
	ctx := context.Background()
	service := f.withScriptedModel(t, &scriptedModel{}) // ceochat's own turn 0 ("Hola.") never reaches Finance

	goal := financeFakeJSONGoal(t, campaign.FinanceReviewOutput{
		Verdict: "recommended", Summary: "Real Harness round trip proof.",
		Assumptions: []string{"none"}, Risks: []string{}, RequiredCorrections: []string{}, MissingInformation: []string{},
	})
	_, taskID, reviewRequestID := fx.seedReadyReviewTaskWithGoal(t, service, "real-harness-full", goal)

	_, _, err := fx.financeService.ExecuteReviewTask(ctx, campaign.ExecuteReviewParams{
		OrganizationID: chatTestOrganization, TaskID: taskID, ReviewRequestID: reviewRequestID,
	})
	if err == nil {
		t.Fatal("expected ExecuteReviewTask to fail at the known Context.ID/context-snapshot blocker, got nil error -- if this now passes, the context-engine follow-up may already be done: update this test to assert success instead")
	}
	const wantBlocker = "initial context ID must be a positive model runtime snapshot ID"
	if !strings.Contains(err.Error(), wantBlocker) {
		t.Fatalf("ExecuteReviewTask error = %v, want it to contain the known blocker %q (a different failure means something else regressed)", err, wantBlocker)
	}

	// The known blocker must fail closed BEFORE any durable side effect:
	// no review persisted, task not completed.
	var reviewCount, completedCount int
	if err := f.store.Pool().QueryRow(ctx, "SELECT count(*) FROM campaign_financial_reviews WHERE organization_id=$1 AND review_request_id=$2", chatTestOrganization, reviewRequestID).Scan(&reviewCount); err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if reviewCount != 0 {
		t.Errorf("review rows = %d, want 0 (must fail closed)", reviewCount)
	}
	if err := f.store.Pool().QueryRow(ctx, "SELECT count(*) FROM tasks WHERE id=$1 AND status='completed'", taskID).Scan(&completedCount); err != nil {
		t.Fatalf("count completed: %v", err)
	}
	if completedCount != 0 {
		t.Errorf("completed task count = %d, want 0 (must fail closed)", completedCount)
	}
}

// TestRealLeaseAuthority_EmptyLeaseTokenRejectedByAdapter is round section
// 16's negative control, exercised directly against the REAL
// tasksauthority.Adapter (not merely Finance's own
// validateFinanceHarnessPreconditions guard, which
// finance_harness_composition_internal_test.go's TestValidateFinanceHarnessPreconditions
// already pins): a real claimed task/attempt/lease from the real Task
// Engine, presented to AuthorizeExecution with LeaseToken="" instead of
// the real one, must fail BEFORE any model invocation is even reachable --
// this test never calls Invoke at all, so "before any model invocation"
// holds trivially and structurally, not just by assertion.
func TestRealLeaseAuthority_EmptyLeaseTokenRejectedByAdapter(t *testing.T) {
	f, fx, modelRuntime := newFinanceRealHarnessFixture(t)
	defer f.cleanup()
	ctx := context.Background()
	service := f.withScriptedModel(t, &scriptedModel{})

	goal := financeFakeJSONGoal(t, campaign.FinanceReviewOutput{Verdict: "recommended", Summary: "unused"})
	_, taskID, _ := fx.seedReadyReviewTaskWithGoal(t, service, "real-lease-neg", goal)

	claimed, err := fx.tasksService.ClaimTaskByID(ctx, taskID, tasks.ClaimRequest{
		OrganizationID: chatTestOrganization, WorkerID: fx.holderPrincipalID, AssignedRoleID: fx.reviewerRoleID, LeaseDuration: 2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}
	if _, err = fx.tasksService.StartAttempt(ctx, tasks.LeaseCommand{TaskID: claimed.Task.ID, AttemptID: claimed.Attempt.ID, LeaseToken: claimed.LeaseToken, ActorID: fx.holderPrincipalID}); err != nil {
		t.Fatalf("start attempt: %v", err)
	}

	authority, err := modelRuntime.NewHarnessAuthority()
	if err != nil {
		t.Fatalf("create real harness authority: %v", err)
	}
	identity := executionharness.RunIdentity{
		RunID: "finrev-negative-lease-test", OrganizationID: chatTestOrganization,
		TaskID: claimed.Task.ID, AttemptID: claimed.Attempt.ID, RoleID: fx.reviewerRoleID,
		ExecutionPrincipalID: fx.holderPrincipalID,
		CorrelationID:        "corr:negative-lease-test", CausationID: "cause:negative-lease-test",
	}

	// Positive control first: the REAL lease token authorizes.
	if err := authority.AuthorizeExecution(ctx, executionharness.AuthorityRequest{Identity: identity, LeaseToken: claimed.LeaseToken}); err != nil {
		t.Fatalf("expected the real lease token to authorize, got: %v", err)
	}

	// Negative control: an empty lease token must NOT authorize, even
	// though every other field (task/attempt/role/principal) is real and
	// currently valid.
	if err := authority.AuthorizeExecution(ctx, executionharness.AuthorityRequest{Identity: identity, LeaseToken: ""}); err == nil {
		t.Fatal("expected AuthorizeExecution to reject an empty lease token, got nil error")
	}
}

// TestFinanceWorkerRealHarness_RunOnce is round section 24: driven through
// financeworker.Worker.RunOnce's real discovery/claim loop (never a direct
// ExecuteReviewTask call), with the SAME real-harness FinanceService
// passed directly as the worker's Executor -- *campaign.FinanceService
// itself already satisfies financeworker.Executor, exactly like production
// composition (cmd/orgctl/executive.go's own buildFinanceWorker passes the
// same concrete FinanceService as its worker's executor).
//
// Per TestRealFinanceHarnessIntegration_MockOutputNilReachesRealModelDispatch's
// own doc comment, the real round trip currently fails closed at a
// separate, newly-discovered blocker (Finance's Context.ID is not yet a
// real context-engine snapshot ID) before a review can be persisted. What
// THIS test proves is that the autonomous worker loop itself stays
// healthy through that failure: RunOnce returns no error, classifies the
// task as ResultInfraFailure (never panics, never wedges), and persists
// nothing -- exactly section 23's "provider/model failure never becomes a
// process failure" concern, exercised through the real worker instead of
// a scripted model.
func TestFinanceWorkerRealHarness_RunOnce(t *testing.T) {
	f, fx, _ := newFinanceRealHarnessFixture(t)
	defer f.cleanup()
	ctx := context.Background()
	service := f.withScriptedModel(t, &scriptedModel{})

	goal := financeFakeJSONGoal(t, campaign.FinanceReviewOutput{Verdict: "recommended", Summary: "Real worker + real harness."})
	_, taskID, reviewRequestID := fx.seedReadyReviewTaskWithGoal(t, service, "real-harness-worker", goal)

	var mu sync.Mutex
	var classifications []financeworker.ResultClassification
	discovery := financeworker.DiscoveryTaskSource{Service: fx.tasksService, OrganizationID: chatTestOrganization, ReviewerRoleID: fx.reviewerRoleID}
	worker, err := financeworker.NewWorker(discovery, fx.store, fx.financeService, financeworker.DefaultConfig(chatTestOrganization),
		financeworker.WithObserver(func(observedTaskID int64, classification financeworker.ResultClassification, _ error) {
			if observedTaskID != taskID {
				return
			}
			mu.Lock()
			classifications = append(classifications, classification)
			mu.Unlock()
		}),
	)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v (the worker process itself must stay healthy even when a task execution fails)", err)
	}

	mu.Lock()
	observed := append([]financeworker.ResultClassification(nil), classifications...)
	mu.Unlock()
	if len(observed) != 1 || observed[0] != financeworker.ResultInfraFailure {
		t.Errorf("classifications for task %d = %v, want exactly [infra_failure] (the known Context.ID blocker)", taskID, observed)
	}

	var reviewCount, completedCount int
	if err := f.store.Pool().QueryRow(ctx, "SELECT count(*) FROM campaign_financial_reviews WHERE organization_id=$1 AND review_request_id=$2", chatTestOrganization, reviewRequestID).Scan(&reviewCount); err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if reviewCount != 0 {
		t.Errorf("review rows = %d, want 0 (must fail closed)", reviewCount)
	}
	if err := f.store.Pool().QueryRow(ctx, "SELECT count(*) FROM tasks WHERE id=$1 AND status='completed'", taskID).Scan(&completedCount); err != nil {
		t.Fatalf("count completed: %v", err)
	}
	if completedCount != 0 {
		t.Errorf("completed task count = %d, want 0 (must fail closed)", completedCount)
	}
}

// TestFinanceWorkerMultiReplicaRealHarness_TwoWorkersOneExecution is round
// section 25: two Worker replicas race on ONE real-harness Finance task
// (MockOutput=nil, test.fake provider). Task Engine's own FOR UPDATE SKIP
// LOCKED claim -- not this test -- is what must guarantee only one replica
// ever attempts the real Harness run for this task, even though (per the
// same known Context.ID blocker as the tests above) that one attempt
// currently fails closed rather than completing. What this test adds
// beyond the single-worker one above: proving the SKIP LOCKED claim
// itself still holds under real concurrency once a real (not MockOutput)
// Harness execution is in the mix -- never two replicas both attempting
// the same task, and never a duplicate review from either.
func TestFinanceWorkerMultiReplicaRealHarness_TwoWorkersOneExecution(t *testing.T) {
	f, fx, _ := newFinanceRealHarnessFixture(t)
	defer f.cleanup()
	ctx := context.Background()
	service := f.withScriptedModel(t, &scriptedModel{})

	goal := financeFakeJSONGoal(t, campaign.FinanceReviewOutput{Verdict: "recommended", Summary: "Multi-replica real harness."})
	_, taskID, reviewRequestID := fx.seedReadyReviewTaskWithGoal(t, service, "real-harness-multi", goal)

	var mu sync.Mutex
	var classifications []financeworker.ResultClassification
	observerFor := func() financeworker.Option {
		return financeworker.WithObserver(func(observedTaskID int64, classification financeworker.ResultClassification, _ error) {
			if observedTaskID != taskID {
				return
			}
			mu.Lock()
			classifications = append(classifications, classification)
			mu.Unlock()
		})
	}

	discovery := financeworker.DiscoveryTaskSource{Service: fx.tasksService, OrganizationID: chatTestOrganization, ReviewerRoleID: fx.reviewerRoleID}
	workerA, err := financeworker.NewWorker(discovery, fx.store, fx.financeService, financeworker.DefaultConfig(chatTestOrganization), observerFor())
	if err != nil {
		t.Fatalf("NewWorker A: %v", err)
	}
	workerB, err := financeworker.NewWorker(discovery, fx.store, fx.financeService, financeworker.DefaultConfig(chatTestOrganization), observerFor())
	if err != nil {
		t.Fatalf("NewWorker B: %v", err)
	}

	done := make(chan struct{}, 2)
	start := make(chan struct{})
	go func() { <-start; _, _ = workerA.RunOnce(ctx); done <- struct{}{} }()
	go func() { <-start; _, _ = workerB.RunOnce(ctx); done <- struct{}{} }()
	close(start)
	<-done
	<-done

	mu.Lock()
	observed := append([]financeworker.ResultClassification(nil), classifications...)
	mu.Unlock()
	attempted := 0
	for _, c := range observed {
		if c != financeworker.ResultBusy {
			attempted++
		}
	}
	if attempted != 1 {
		t.Errorf("non-busy classifications for task %d = %d (observed: %v), want exactly 1 (only one replica may ever attempt the real Harness run)", taskID, attempted, observed)
	}

	var reviewCount, completedCount int
	if err := f.store.Pool().QueryRow(ctx, "SELECT count(*) FROM campaign_financial_reviews WHERE organization_id=$1 AND review_request_id=$2", chatTestOrganization, reviewRequestID).Scan(&reviewCount); err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if reviewCount != 0 {
		t.Errorf("review rows = %d, want 0 (the known Context.ID blocker fails closed; and never a duplicate regardless)", reviewCount)
	}
	if err := f.store.Pool().QueryRow(ctx, "SELECT count(*) FROM tasks WHERE id=$1 AND status='completed'", taskID).Scan(&completedCount); err != nil {
		t.Fatalf("count completed: %v", err)
	}
	if completedCount != 0 {
		t.Errorf("completed task count = %d, want 0 (must fail closed)", completedCount)
	}
}
