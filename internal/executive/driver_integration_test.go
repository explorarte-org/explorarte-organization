//go:build integration

package executive_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/executive/driver"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
)

func TestCampaignDriver_AutonomousAdvancement(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	completionGate := &countingCompletion{delegate: h.completion}
	orchestrator := newOrchestrator(t, h, models, integrationAssignments{}, completionGate)
	taskAdapter := runtimeadapter.Tasks{Service: h.tasks, OrganizationID: "explorarte"}

	run, reused, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{
		ActorRoleID: executive.OwnerRoleID, IdempotencyKey: "driver-auto-advance-1",
		Goal: executive.OwnerGoal{
			Goal: "Analyze the organization and return a one-area plan without external actions.",
			AcceptanceCriteria: []executive.AcceptanceCriterion{
				{Text: "one department reviewed", Phase: executive.AcceptanceDesign},
				{Text: "closure verified", Phase: executive.AcceptanceImplementation},
			},
		},
	})
	if err != nil || reused {
		t.Fatalf("submit: run=%+v reused=%v err=%v", run, reused, err)
	}
	rootID := run.RootTaskID

	// Invariant check: immediately after submit, 0 department tasks exist.
	childrenBefore, err := taskAdapter.ListByCorrelation(h.ctx, run.CorrelationID)
	if err != nil {
		t.Fatalf("list children before: %v", err)
	}
	if len(childrenBefore) != 1 { // only root task
		t.Fatalf("expected only root task before driver execution, got %d tasks", len(childrenBefore))
	}

	coord := driver.NewPostgresRootCoordinator(h.store.Pool())
	drv, err := driver.NewCampaignDriver(
		orchestrator,
		taskAdapter,
		coord,
		driver.DefaultConfig("explorarte"),
	)
	if err != nil {
		t.Fatalf("NewCampaignDriver: %v", err)
	}

	// CRITICAL TEST PROPERTY: NO explicit orchestrator.Resume(rootID) is called by test code!
	// Advancement is driven entirely by the autonomous driver.
	metrics, err := drv.RunOnce(h.ctx)
	if err != nil {
		t.Fatalf("driver.RunOnce: %v", err)
	}

	if metrics.RootsDiscovered != 1 {
		t.Errorf("RootsDiscovered = %d, want 1", metrics.RootsDiscovered)
	}
	if metrics.RootsClaimed != 1 {
		t.Errorf("RootsClaimed = %d, want 1", metrics.RootsClaimed)
	}
	if metrics.ResumeCalls != 1 {
		t.Errorf("ResumeCalls = %d, want 1", metrics.ResumeCalls)
	}
	if metrics.ResumeSuccess != 1 {
		t.Errorf("ResumeSuccess = %d, want 1", metrics.ResumeSuccess)
	}

	// Verify in PostgreSQL that the CEO plan child task was created and progressed
	childrenAfter, err := taskAdapter.ListByCorrelation(h.ctx, run.CorrelationID)
	if err != nil {
		t.Fatalf("list children after: %v", err)
	}
	var foundCEOPlan bool
	for _, child := range childrenAfter {
		if child.TaskClass == executive.TaskClassCoordinationCEOPlan {
			foundCEOPlan = true
			if child.Status != "completed" {
				t.Errorf("CEO plan task status = %s, want completed", child.Status)
			}
		}
	}
	if !foundCEOPlan {
		t.Errorf("autonomous driver failed to create and drive CEO plan task")
	}
	t.Logf("PASS: autonomous driver progressed root %d from submitted to CEO plan completion without chat or manual intervention.", rootID)
}

func TestCampaignDriver_MultiReplicaConcurrency(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	completionGate := &countingCompletion{delegate: h.completion}
	orchestrator := newOrchestrator(t, h, models, integrationAssignments{}, completionGate)
	taskAdapter := runtimeadapter.Tasks{Service: h.tasks, OrganizationID: "explorarte"}

	run, reused, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{
		ActorRoleID: executive.OwnerRoleID, IdempotencyKey: "driver-concurrent-goal-1",
		Goal: executive.OwnerGoal{
			Goal: "Analyze the organization and return a one-area plan without external actions.",
			AcceptanceCriteria: []executive.AcceptanceCriterion{
				{Text: "one department reviewed", Phase: executive.AcceptanceDesign},
				{Text: "closure verified", Phase: executive.AcceptanceImplementation},
			},
		},
	})
	if err != nil || reused {
		t.Fatalf("submit: run=%+v reused=%v err=%v", run, reused, err)
	}

	// Simulate two separate driver replicas (Driver A and Driver B) with independent coordinators
	coordA := driver.NewPostgresRootCoordinator(h.store.Pool())
	driverA, err := driver.NewCampaignDriver(
		orchestrator,
		taskAdapter,
		coordA,
		driver.DefaultConfig("explorarte"),
	)
	if err != nil {
		t.Fatalf("NewCampaignDriver A: %v", err)
	}

	coordB := driver.NewPostgresRootCoordinator(h.store.Pool())
	driverB, err := driver.NewCampaignDriver(
		orchestrator,
		taskAdapter,
		coordB,
		driver.DefaultConfig("explorarte"),
	)
	if err != nil {
		t.Fatalf("NewCampaignDriver B: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		<-start
		_, _ = driverA.RunOnce(h.ctx)
	}()

	go func() {
		defer wg.Done()
		<-start
		_, _ = driverB.RunOnce(h.ctx)
	}()

	// Release both simultaneously
	close(start)
	wg.Wait()

	metricsA := driverA.SnapshotMetrics()
	metricsB := driverB.SnapshotMetrics()

	totalCalls := metricsA.ResumeCalls + metricsB.ResumeCalls
	totalBusy := metricsA.ResumeBusy + metricsB.ResumeBusy

	t.Logf("Concurrency results: Replica A calls=%d busy=%d; Replica B calls=%d busy=%d",
		metricsA.ResumeCalls, metricsA.ResumeBusy, metricsB.ResumeCalls, metricsB.ResumeBusy)

	if totalCalls != 1 {
		t.Errorf("total Resume calls across both replicas = %d, want exactly 1", totalCalls)
	}
	if totalBusy != 1 {
		t.Errorf("total busy reports across both replicas = %d, want exactly 1", totalBusy)
	}

	// Prove exactly 1 model advancement executed
	if len(models.runs) != 1 {
		t.Errorf("model advancements count = %d, want exactly 1", len(models.runs))
	}

	// Verify in PostgreSQL: exactly 1 CEO plan task created (NO duplicates)
	children, err := taskAdapter.ListByCorrelation(h.ctx, run.CorrelationID)
	if err != nil {
		t.Fatalf("list children: %v", err)
	}
	var planCount int
	for _, child := range children {
		if child.TaskClass == executive.TaskClassCoordinationCEOPlan {
			planCount++
		}
	}
	if planCount != 1 {
		t.Errorf("CEO plan task count in PostgreSQL = %d, want exactly 1 (no duplicate children)", planCount)
	}
}

func TestPostgresRootCoordinator_SamePhysicalSessionLifecycle(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()

	coord := driver.NewPostgresRootCoordinator(h.store.Pool())
	orgID := "explorarte"
	rootID := int64(9881)

	claim, claimed, err := coord.TryClaimRoot(h.ctx, orgID, rootID)
	if err != nil || !claimed {
		t.Fatalf("TryClaimRoot: claimed=%v, err=%v", claimed, err)
	}

	pidAtClaim := claim.SessionPID()
	if pidAtClaim == 0 {
		t.Fatalf("expected non-zero session PID at claim, got 0")
	}

	// Verify in PostgreSQL pg_locks that the advisory lock is owned by PID_AT_CLAIM
	var lockedPID uint32
	lockQuery := `SELECT pid FROM pg_locks WHERE locktype = 'advisory' AND ((classid::bigint << 32) | (objid::bigint & 4294967295)) = hashtextextended($1, 0)`
	lockKey := "executive-root:" + orgID + ":9881"
	err = h.store.Pool().QueryRow(h.ctx, lockQuery, lockKey).Scan(&lockedPID)
	if err != nil {
		t.Fatalf("query pg_locks while held: %v", err)
	}

	pidWhileHeld := lockedPID
	if pidWhileHeld != pidAtClaim {
		t.Fatalf("PID mismatch: PID_AT_CLAIM=%d, PID_WHILE_HELD=%d", pidAtClaim, pidWhileHeld)
	}

	// Release claim
	if err := claim.Release(h.ctx); err != nil {
		t.Fatalf("claim.Release failed: %v", err)
	}

	// Verify advisory lock is no longer held in PostgreSQL
	var count int
	_ = h.store.Pool().QueryRow(h.ctx, "SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND ((classid::bigint << 32) | (objid::bigint & 4294967295)) = hashtextextended($1, 0)", lockKey).Scan(&count)
	if count != 0 {
		t.Fatalf("advisory lock still held in pg_locks after Release: count=%d", count)
	}

	t.Logf("PASS: same physical session proven: PID_AT_CLAIM (%d) == PID_WHILE_LOCK_HELD (%d)", pidAtClaim, pidWhileHeld)
}

func TestPostgresRootCoordinator_PoolReuseNegative(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()

	coordA := driver.NewPostgresRootCoordinator(h.store.Pool())
	coordB := driver.NewPostgresRootCoordinator(h.store.Pool())
	orgID := "explorarte"
	rootID := int64(9882)

	// 1. Driver A acquires root claim
	claimA, claimedA, err := coordA.TryClaimRoot(h.ctx, orgID, rootID)
	if err != nil || !claimedA {
		t.Fatalf("Driver A claim failed: claimed=%v, err=%v", claimedA, err)
	}

	// 2. Hold claim open and test Driver B on same root
	_, claimedB, err := coordB.TryClaimRoot(h.ctx, orgID, rootID)
	if err != nil {
		t.Fatalf("Driver B unexpected error: %v", err)
	}
	if claimedB {
		t.Fatalf("Driver B claimed root while Driver A holds lock; want BUSY (false)")
	}

	// 3. Unrelated queries can use another pooled connection without being blocked
	var pingVal int
	if err := h.store.Pool().QueryRow(h.ctx, "SELECT 42").Scan(&pingVal); err != nil || pingVal != 42 {
		t.Fatalf("unrelated pooled query failed while lock held: val=%d, err=%v", pingVal, err)
	}

	// 4. Driver A releases claim
	if err := claimA.Release(h.ctx); err != nil {
		t.Fatalf("Driver A release failed: %v", err)
	}

	// 5. Driver B can now claim
	claimB2, claimedB2, err := coordB.TryClaimRoot(h.ctx, orgID, rootID)
	if err != nil || !claimedB2 {
		t.Fatalf("Driver B claim after release failed: claimed=%v, err=%v", claimedB2, err)
	}
	_ = claimB2.Release(h.ctx)

	t.Logf("PASS: pool reuse negative test verified - lock pinned without monopolizing pool")
}

type barrierExecutiveResumer struct {
	delegate     driver.ExecutiveResumer
	beforeResume func(id int64)
}

func (b *barrierExecutiveResumer) ResumeDurable(ctx context.Context, id int64) (executive.Run, error) {
	if b.beforeResume != nil {
		b.beforeResume(id)
	}
	return b.delegate.ResumeDurable(ctx, id)
}

func TestCampaignDriver_ConnectionNotReleasedBeforeResumeCompletes(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	completionGate := &countingCompletion{delegate: h.completion}
	orchestrator := newOrchestrator(t, h, models, integrationAssignments{}, completionGate)
	taskAdapter := runtimeadapter.Tasks{Service: h.tasks, OrganizationID: "explorarte"}

	run, reused, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{
		ActorRoleID: executive.OwnerRoleID, IdempotencyKey: "driver-barrier-1",
		Goal: executive.OwnerGoal{
			Goal: "Analyze the organization and return a one-area plan without external actions.",
			AcceptanceCriteria: []executive.AcceptanceCriterion{
				{Text: "one department reviewed", Phase: executive.AcceptanceDesign},
				{Text: "closure verified", Phase: executive.AcceptanceImplementation},
			},
		},
	})
	if err != nil || reused {
		t.Fatalf("submit: %v", err)
	}

	inResume := make(chan struct{})
	allowResume := make(chan struct{})

	barrierResumer := &barrierExecutiveResumer{
		delegate: orchestrator,
		beforeResume: func(id int64) {
			close(inResume)
			<-allowResume
		},
	}

	coordA := driver.NewPostgresRootCoordinator(h.store.Pool())
	driverA, _ := driver.NewCampaignDriver(barrierResumer, taskAdapter, coordA, driver.DefaultConfig("explorarte"))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = driverA.RunOnce(h.ctx)
	}()

	// Wait until driverA is actively blocked inside Resume
	<-inResume

	// While blocked inside Resume: Driver B attempts same root claim
	coordB := driver.NewPostgresRootCoordinator(h.store.Pool())
	_, claimedB, err := coordB.TryClaimRoot(h.ctx, "explorarte", run.RootTaskID)
	if err != nil {
		t.Fatalf("Driver B TryClaimRoot err: %v", err)
	}
	if claimedB {
		t.Fatalf("CRITICAL FAILURE: Driver B claimed root while Driver A is inside Resume! Connection was returned early!")
	}

	// Allow Resume to complete
	close(allowResume)
	wg.Wait()

	// After Resume finishes, connection is released and Driver B can now claim
	claimB2, claimedB2, err := coordB.TryClaimRoot(h.ctx, "explorarte", run.RootTaskID)
	if err != nil || !claimedB2 {
		t.Fatalf("Driver B claim after Resume complete failed: claimed=%v, err=%v", claimedB2, err)
	}
	_ = claimB2.Release(h.ctx)

	t.Logf("PASS: connection pinned throughout entire Resume execution and not released early")
}

func TestPostgresRootCoordinator_CrashConnectionDropAutoRelease(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()

	coord1 := driver.NewPostgresRootCoordinator(h.store.Pool())
	orgID := "explorarte"
	rootID := int64(9884)

	claim1, claimed1, err := coord1.TryClaimRoot(h.ctx, orgID, rootID)
	if err != nil || !claimed1 {
		t.Fatalf("initial claim failed: claimed=%v, err=%v", claimed1, err)
	}

	pid1 := claim1.SessionPID()
	if pid1 == 0 {
		t.Fatal("expected non-zero PID")
	}

	// Terminate the backend session in PostgreSQL to simulate worker crash / connection drop WITHOUT unlock
	var terminated bool
	err = h.store.Pool().QueryRow(h.ctx, "SELECT pg_terminate_backend($1)", pid1).Scan(&terminated)
	if err != nil {
		t.Fatalf("pg_terminate_backend failed: %v", err)
	}

	// Give PostgreSQL time to terminate backend and clean up session advisory locks
	var reaped bool
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		var count int
		_ = h.store.Pool().QueryRow(h.ctx, "SELECT count(*) FROM pg_stat_activity WHERE pid = $1", pid1).Scan(&count)
		if count == 0 {
			reaped = true
			break
		}
	}
	if !reaped {
		t.Fatal("backend was not reaped by PostgreSQL")
	}

	// claim1's local *pgxpool.Conn handle is still "acquired" from this
	// harness's own pool, even though the backend it pointed at is now
	// dead -- the pool has no way to know that on its own. Left alone,
	// that handle would never return to the pool, and h.close()'s own
	// pool.Close() (called via defer above) would block forever waiting
	// for it, hanging this whole test package. This is purely a test
	// artifact of simulating a mid-flight process crash while the test's
	// own pool object stays alive; a real crash takes the whole process
	// (and its pool) down with it, so production code never needs an
	// equivalent step. Release() against the now-dead connection is
	// expected to fail its unlock query and take its own fail-safe path
	// (Hijack + Close), which is exactly what frees the pool's slot here.
	if releaseErr := claim1.Release(h.ctx); releaseErr == nil {
		t.Fatal("claim1.Release on a session already terminated by pg_terminate_backend unexpectedly reported success")
	}

	// Now another coordinator attempts to claim the same root:
	// PostgreSQL must have freed the lock when the session died!
	coord2 := driver.NewPostgresRootCoordinator(h.store.Pool())
	claim2, claimed2, err2 := coord2.TryClaimRoot(h.ctx, orgID, rootID)
	if err2 != nil || !claimed2 {
		t.Fatalf("claim after session termination failed: claimed=%v, err=%v", claimed2, err2)
	}
	_ = claim2.Release(h.ctx)

	t.Logf("PASS: session drop automatically releases advisory lock without permanent leak")
}

func TestCampaignDriver_ContextCancellationDoesNotLeakLock(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	completionGate := &countingCompletion{delegate: h.completion}
	orchestrator := newOrchestrator(t, h, models, integrationAssignments{}, completionGate)
	taskAdapter := runtimeadapter.Tasks{Service: h.tasks, OrganizationID: "explorarte"}

	run, reused, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{
		ActorRoleID: executive.OwnerRoleID, IdempotencyKey: "driver-ctx-cancel-1",
		Goal: executive.OwnerGoal{
			Goal: "Analyze the organization and return a one-area plan without external actions.",
			AcceptanceCriteria: []executive.AcceptanceCriterion{
				{Text: "one department reviewed", Phase: executive.AcceptanceDesign},
				{Text: "closure verified", Phase: executive.AcceptanceImplementation},
			},
		},
	})
	if err != nil || reused {
		t.Fatalf("submit: %v", err)
	}

	resumeStarted := make(chan struct{})
	cancelCtx, cancel := context.WithCancel(h.ctx)

	blockingResumer := &barrierExecutiveResumer{
		delegate: orchestrator,
		beforeResume: func(id int64) {
			close(resumeStarted)
			<-cancelCtx.Done()
		},
	}

	coord := driver.NewPostgresRootCoordinator(h.store.Pool())
	drv, _ := driver.NewCampaignDriver(blockingResumer, taskAdapter, coord, driver.DefaultConfig("explorarte"))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = drv.RunOnce(cancelCtx)
	}()

	<-resumeStarted
	// Cancel parent context while Resume is in flight
	cancel()
	wg.Wait()

	// Verify that the lock is released or session destroyed, and a fresh driver can claim that root
	coordFresh := driver.NewPostgresRootCoordinator(h.store.Pool())
	claimFresh, claimedFresh, err := coordFresh.TryClaimRoot(h.ctx, "explorarte", run.RootTaskID)
	if err != nil || !claimedFresh {
		t.Fatalf("fresh claim after canceled Resume failed: claimed=%v, err=%v", claimedFresh, err)
	}
	_ = claimFresh.Release(h.ctx)

	t.Logf("PASS: context cancellation did not leak lock")
}

func TestCampaignDriver_RestartAfterSessionDrop(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	completionGate := &countingCompletion{delegate: h.completion}
	orchestrator := newOrchestrator(t, h, models, integrationAssignments{}, completionGate)
	taskAdapter := runtimeadapter.Tasks{Service: h.tasks, OrganizationID: "explorarte"}

	run, reused, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{
		ActorRoleID: executive.OwnerRoleID, IdempotencyKey: "driver-crash-restart-1",
		Goal: executive.OwnerGoal{
			Goal: "Analyze the organization and return a one-area plan without external actions.",
			AcceptanceCriteria: []executive.AcceptanceCriterion{
				{Text: "one department reviewed", Phase: executive.AcceptanceDesign},
				{Text: "closure verified", Phase: executive.AcceptanceImplementation},
			},
		},
	})
	if err != nil || reused {
		t.Fatalf("submit: %v", err)
	}

	// 1. Worker instance A claims the root and terminates mid-work (session drop)
	coordA := driver.NewPostgresRootCoordinator(h.store.Pool())
	claimA, claimedA, err := coordA.TryClaimRoot(h.ctx, "explorarte", run.RootTaskID)
	if err != nil || !claimedA {
		t.Fatalf("initial claim failed: %v", err)
	}

	// Terminate the physical connection abruptly
	pidA := claimA.SessionPID()
	var terminated bool
	_ = h.store.Pool().QueryRow(h.ctx, "SELECT pg_terminate_backend($1)", pidA).Scan(&terminated)

	// Wait for PostgreSQL to actually finish reaping the backend (and, with
	// it, the session-level advisory lock) instead of a flat sleep -- the
	// same bounded poll TestPostgresRootCoordinator_CrashConnectionDropAutoRelease
	// uses. A fixed short sleep here was racy: driverB's TryClaimRoot could
	// run before the server finished releasing the lock, observe BUSY, and
	// make this test's own root un-discoverable/un-claimable for reasons
	// that have nothing to do with what it is trying to prove.
	var reaped bool
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		var count int
		_ = h.store.Pool().QueryRow(h.ctx, "SELECT count(*) FROM pg_stat_activity WHERE pid = $1", pidA).Scan(&count)
		if count == 0 {
			reaped = true
			break
		}
	}
	if !reaped {
		t.Fatal("backend was not reaped by PostgreSQL")
	}

	// claimA's local *pgxpool.Conn handle is still "acquired" from this
	// harness's own pool even though the backend it pointed at is now dead
	// -- exactly the same test-only pool-accounting leak
	// TestPostgresRootCoordinator_CrashConnectionDropAutoRelease fixes the
	// same way. Left alone, h.close()'s deferred pool.Close() would block
	// forever waiting for this handle to return, hanging this whole test
	// binary until go test's own timeout kills it.
	if releaseErr := claimA.Release(h.ctx); releaseErr == nil {
		t.Fatal("claimA.Release on a session already terminated by pg_terminate_backend unexpectedly reported success")
	}

	// 2. Brand new worker instance B starts up and runs RunOnce
	coordB := driver.NewPostgresRootCoordinator(h.store.Pool())
	driverB, err := driver.NewCampaignDriver(orchestrator, taskAdapter, coordB, driver.DefaultConfig("explorarte"))
	if err != nil {
		t.Fatalf("NewCampaignDriver B: %v", err)
	}

	metrics, err := driverB.RunOnce(h.ctx)
	if err != nil {
		t.Fatalf("driverB.RunOnce: %v", err)
	}

	if metrics.RootsClaimed != 1 || metrics.ResumeSuccess != 1 {
		t.Errorf("new worker failed to claim and advance runnable root after session drop: %+v", metrics)
	}

	t.Logf("PASS: root recovered and advanced by fresh driver instance after abandoned session")
}

func TestCampaignDriver_PreexistingRootAndRestart(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	completionGate := &countingCompletion{delegate: h.completion}
	orchestrator := newOrchestrator(t, h, models, integrationAssignments{}, completionGate)
	taskAdapter := runtimeadapter.Tasks{Service: h.tasks, OrganizationID: "explorarte"}

	// Seed root BEFORE driver instance exists
	run, reused, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{
		ActorRoleID: executive.OwnerRoleID, IdempotencyKey: "driver-preexisting-goal-1",
		Goal: executive.OwnerGoal{
			Goal: "Analyze the organization and return a one-area plan without external actions.",
			AcceptanceCriteria: []executive.AcceptanceCriterion{
				{Text: "one department reviewed", Phase: executive.AcceptanceDesign},
				{Text: "closure verified", Phase: executive.AcceptanceImplementation},
			},
		},
	})
	if err != nil || reused {
		t.Fatalf("submit: run=%+v reused=%v err=%v", run, reused, err)
	}

	// Fresh driver instance starts up later
	coord := driver.NewPostgresRootCoordinator(h.store.Pool())
	drv, err := driver.NewCampaignDriver(
		orchestrator,
		taskAdapter,
		coord,
		driver.DefaultConfig("explorarte"),
	)
	if err != nil {
		t.Fatalf("NewCampaignDriver: %v", err)
	}

	metrics, err := drv.RunOnce(h.ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if metrics.RootsDiscovered != 1 || metrics.RootsClaimed != 1 || metrics.ResumeCalls != 1 {
		t.Errorf("preexisting root was not properly discovered and advanced: %+v", metrics)
	}
}

func TestCampaignDriver_ExcludesCEOChatAndBlockedHuman(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	completionGate := &countingCompletion{delegate: h.completion}
	orchestrator := newOrchestrator(t, h, models, integrationAssignments{}, completionGate)
	taskAdapter := runtimeadapter.Tasks{Service: h.tasks, OrganizationID: "explorarte"}

	// 1. Create a CEO Chat turn task
	chatTask, reused, err := taskAdapter.CreateTask(h.ctx, executive.CreateTaskCommand{
		RequestedByRoleID: executive.OwnerRoleID,
		AssignedRoleID:    executive.CEORoleID,
		TaskClass:         "executive.ceo_chat_turn",
		IdempotencyKey:    "test-chat-task-exclude-1",
		Title:             "CEO Chat Turn",
		Instructions:      "Answer user question",
		Priority:          100,
		MaxAttempts:       3,
	})
	if err != nil || reused {
		t.Fatalf("create chat task: %v", err)
	}

	// 2. Submit a campaign root and block it with owner_decision_required
	run, _, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{
		ActorRoleID: executive.OwnerRoleID, IdempotencyKey: "driver-blocked-human-1",
		Goal: executive.OwnerGoal{
			Goal: "Analyze the organization and return a one-area plan without external actions.",
			AcceptanceCriteria: []executive.AcceptanceCriterion{
				{Text: "one department reviewed", Phase: executive.AcceptanceDesign},
				{Text: "closure verified", Phase: executive.AcceptanceImplementation},
			},
		},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	_, err = taskAdapter.BlockTask(h.ctx, run.RootTaskID, "owner_decision_required", "waiting for owner input", "service", "test")
	if err != nil {
		t.Fatalf("block task: %v", err)
	}

	coord := driver.NewPostgresRootCoordinator(h.store.Pool())
	drv, err := driver.NewCampaignDriver(
		orchestrator,
		taskAdapter,
		coord,
		driver.DefaultConfig("explorarte"),
	)
	if err != nil {
		t.Fatalf("NewCampaignDriver: %v", err)
	}

	metrics, err := drv.RunOnce(h.ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Neither the chat task nor the human-blocked campaign root should be claimed or resumed!
	if metrics.RootsDiscovered != 0 {
		t.Errorf("RootsDiscovered = %d, want 0 (chat task and human-blocked root must be excluded)", metrics.RootsDiscovered)
	}
	if metrics.ResumeCalls != 0 {
		t.Errorf("ResumeCalls = %d, want 0", metrics.ResumeCalls)
	}
	_ = chatTask
}
