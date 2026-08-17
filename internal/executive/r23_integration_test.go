//go:build integration

package executive_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	taskcontextprovider "github.com/Mireuz13/explorarte-organization/internal/tasks/contextprovider"
	taskpostgres "github.com/Mireuz13/explorarte-organization/internal/tasks/postgres"
)

func TestR23PostgreSQLProjectsWorkerEvidenceAndClosesDAGRace(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()

	models := newIntegrationModelRuntime()
	completionGate := &countingCompletion{delegate: h.completion}
	limits := executive.DefaultLimits()
	baseTasks := runtimeadapter.Tasks{Service: h.tasks, OrganizationID: "explorarte"}
	evidenceTasks := runtimeadapter.EvidenceTasks{Tasks: baseTasks, Models: models, Completion: completionGate, Limits: limits}
	dagTasks := runtimeadapter.DAGTasks{TaskCoordinator: evidenceTasks}
	modelBudget := runtimeadapter.ModelCallBudget{Models: models, Tasks: dagTasks, Limits: limits}

	orchestrator, err := executive.NewOrchestrator(executive.Dependencies{
		OrganizationID: "explorarte",
		Registry:       runtimeadapter.Registry{Reader: h.registry, OrganizationID: "explorarte"},
		Tasks:          dagTasks,
		Contexts:       &integrationContext{},
		Assignments:    integrationAssignments{},
		Principals:     h.principals,
		Models:         models,
		Harness:        models,
		Budget:         modelBudget,
		Completion:     completionGate,
		Decisions:      h.decisions,
		Authorization:  h.authz,
		Limits:         limits,
		Clock:          executive.ClockFunc(time.Now),
	})
	if err != nil {
		t.Fatal(err)
	}
	run, reused, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{
		ActorRoleID: executive.OwnerRoleID, IdempotencyKey: "r23-postgres-evidence",
		Goal: executive.OwnerGoal{Goal: "Analyze one engineering area without external actions.", AcceptanceCriteria: []string{"review real worker evidence", "verified closure"}},
	})
	if err != nil || reused {
		t.Fatalf("submit: run=%+v reused=%v err=%v", run, reused, err)
	}

	for i := 0; i < 30; i++ {
		run, err = orchestrator.Resume(h.ctx, run.RootTaskID)
		if err != nil {
			t.Fatalf("resume %d: %v", i, err)
		}
		if run.State == executive.StateCompleted {
			break
		}
	}
	if run.State != executive.StateCompleted {
		t.Fatalf("run did not complete: %+v", run)
	}

	values, err := h.tasks.ListTasks(h.ctx, tasks.TaskFilter{OrganizationID: "explorarte", CorrelationID: run.CorrelationID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var workerID, reviewID, planningID int64
	for _, task := range values {
		switch {
		case strings.Contains(task.IdempotencyKey, ":leader-plan:ingenieria_ia"):
			planningID = task.ID
		case strings.Contains(task.IdempotencyKey, ":worker:ingenieria_ia:"):
			workerID = task.ID
		case strings.Contains(task.IdempotencyKey, ":leader-review:ingenieria_ia"):
			reviewID = task.ID
		}
	}
	if workerID == 0 || reviewID == 0 || planningID == 0 {
		t.Fatalf("missing r23 tasks: plan=%d worker=%d review=%d", planningID, workerID, reviewID)
	}
	workerDetail, err := h.tasks.GetTask(h.ctx, workerID)
	if err != nil {
		t.Fatal(err)
	}
	foundSourceDependency := false
	for _, dep := range workerDetail.Dependencies {
		if dep.ID == planningID {
			foundSourceDependency = true
			break
		}
	}
	if !foundSourceDependency {
		t.Fatalf("worker %d was not created with planning task %d as a durable dependency: %+v", workerID, planningID, workerDetail.Dependencies)
	}

	reviewDetail, err := h.tasks.GetTask(h.ctx, reviewID)
	if err != nil {
		t.Fatal(err)
	}
	foundBundle := false
	for _, evidence := range reviewDetail.Evidence {
		if strings.HasPrefix(evidence.Reference, "executive-evidence:department:ingenieria_ia:") {
			foundBundle = true
			break
		}
	}
	if !foundBundle {
		t.Fatalf("review task has no executive evidence bundle: %+v", reviewDetail.Evidence)
	}

	taskStore, err := taskpostgres.New(h.store)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := taskcontextprovider.New(taskStore)
	if err != nil {
		t.Fatal(err)
	}
	// ORG-AUDIT-010: GetTaskContext now requires ActorRoleID to match the
	// task's real assignee -- this test is about evidence-content
	// projection, not actor identity, so it supplies the real assignee
	// rather than exercising that check.
	record, err := provider.GetTaskContext(context.Background(), contextengine.BuildRequest{
		OrganizationID: "explorarte", OrganizationRevisionID: reviewDetail.Task.OrganizationRevisionID,
		ActorRoleID: reviewDetail.Task.AssignedRoleID,
		TaskRef:     fmt.Sprintf("task:%d", reviewID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if record == nil || !strings.Contains(string(record.Content), "bounded findings") || !strings.Contains(string(record.Content), "integration:evidence:1") {
		t.Fatalf("review context does not contain projected durable worker evidence: %s", string(record.Content))
	}
}
