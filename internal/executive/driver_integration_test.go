//go:build integration

package executive_test

import (
	"sync"
	"testing"

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
