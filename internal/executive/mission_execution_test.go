package executive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/engineeringmission"
)

func codeRunnerRootForTest() TaskRecord {
	return TaskRecord{
		ID: 1,
		Requirements: []RequirementRecord{
			{ID: 11, Key: MissionRequirementKey, Required: true, Status: "satisfied"},
			{ID: 12, Key: CodeRunnerExecutionEvidenceRequirementKey, Required: true, Type: "result", Status: "pending"},
		},
		Evidence: []EvidenceRecord{{
			Reference: "engineering-mission://91",
			Type:      "result",
		}},
	}
}

func codeRunnerTaskForTest() TaskRecord {
	const (
		taskID      int64 = 91
		attemptID   int64 = 7
		workspaceID int64 = 501
	)
	digest := strings.Repeat("a", 64)
	manifestDigest := strings.Repeat("b", 64)
	checks := []any{
		map[string]any{"type": "GO_BUILD", "success": true},
		map[string]any{"type": "GO_VET", "success": true},
		map[string]any{"type": "GO_TEST", "success": true},
		map[string]any{"type": "FITNESS", "success": true},
	}
	return TaskRecord{
		ID:             taskID,
		AssignedRoleID: engineeringmission.CodeRunnerRole,
		Status:         "awaiting_verification",
		Attempts:       []AttemptRecord{{ID: attemptID, State: "finished"}},
		Requirements: []RequirementRecord{
			{ID: 9101, Key: "candidate-artifact", Required: true, Status: "satisfied"},
			{ID: 9102, Key: "engineering-required-gates", Required: true, Status: "satisfied"},
			{ID: 9103, Key: "review", Required: true, Status: "pending"},
		},
		Evidence: []EvidenceRecord{
			{
				RequirementID: 9101,
				Type:          "artifact",
				Reference:     "artifact://sha256/" + manifestDigest,
				Digest:        manifestDigest,
			},
			{
				Type:      "result",
				Reference: fmt.Sprintf("code-runner-attempt-evidence://task/%d/attempt/%d", taskID, attemptID),
				Digest:    digest,
				Metadata: map[string]any{
					"schema_version": "code-runner-attempt-evidence/v1",
					"task_id":        float64(taskID),
					"attempt_id":     float64(attemptID),
					"operations_executed": []any{
						map[string]any{"type": "APPLY_PATCH", "success": true, "truncated": false},
						map[string]any{"type": "GO_BUILD", "success": true, "truncated": false},
						map[string]any{"type": "GO_VET", "success": true, "truncated": false},
						map[string]any{"type": "GO_TEST", "success": true, "truncated": false},
						map[string]any{"type": "FITNESS", "success": true, "truncated": false},
					},
					"checks_run": checks,
					"candidate_revision": map[string]any{
						"workspace_id":  float64(workspaceID),
						"workspace_key": "mission-91",
					},
				},
			},
		},
	}
}

func TestCodeRunnerPlanContractIsOptInAndBounded(t *testing.T) {
	root := TaskRecord{Requirements: []RequirementRecord{{
		Key: CodeRunnerExecutionEvidenceRequirementKey, Required: true,
	}}}
	valid := ExecutivePlan{DepartmentRequests: []DepartmentRequest{{UnitID: "ingenieria_ia"}}}
	if err := validateCodeRunnerExecutivePlan(root, valid); err != nil {
		t.Fatalf("valid bounded plan rejected: %v", err)
	}
	valid.DepartmentRequests = append(valid.DepartmentRequests, DepartmentRequest{UnitID: "ingenieria_ia"})
	if err := validateCodeRunnerExecutivePlan(root, valid); !errors.Is(err, ErrCodeRunnerExecutionInvalid) {
		t.Fatalf("multi-department plan error=%v, want ErrCodeRunnerExecutionInvalid", err)
	}
	valid.DepartmentRequests = []DepartmentRequest{{UnitID: "investigacion"}}
	if err := validateCodeRunnerExecutivePlan(root, valid); !errors.Is(err, ErrCodeRunnerExecutionInvalid) {
		t.Fatalf("wrong-unit plan error=%v, want ErrCodeRunnerExecutionInvalid", err)
	}

	department := DepartmentPlan{Tasks: []WorkerTaskProposal{{ClientKey: "one"}}}
	if err := validateCodeRunnerDepartmentPlan(root, "ingenieria_ia", department); err != nil {
		t.Fatalf("valid bounded department plan rejected: %v", err)
	}
	department.Tasks = append(department.Tasks, WorkerTaskProposal{ClientKey: "two"})
	if err := validateCodeRunnerDepartmentPlan(root, "ingenieria_ia", department); !errors.Is(err, ErrCodeRunnerExecutionInvalid) {
		t.Fatalf("multi-task department plan error=%v, want ErrCodeRunnerExecutionInvalid", err)
	}
}

func TestVerifiedCodeRunnerEvidenceRequiresAllHostGates(t *testing.T) {
	task := codeRunnerTaskForTest()
	if _, _, err := verifiedCodeRunnerEvidence(task); err != nil {
		t.Fatalf("valid CodeRunner evidence rejected: %v", err)
	}

	for i, evidence := range task.Evidence {
		if strings.HasPrefix(evidence.Reference, codeRunnerEvidenceReferencePrefix) {
			checks := evidence.Metadata["checks_run"].([]any)
			evidence.Metadata["checks_run"] = checks[:3]
			task.Evidence[i] = evidence
		}
	}
	if _, _, err := verifiedCodeRunnerEvidence(task); err == nil {
		t.Fatal("evidence without FITNESS passed the host-owned execution barrier")
	}
}

func TestVerifiedCodeRunnerEvidenceIgnoresCheckLinkWithResultReference(t *testing.T) {
	task := codeRunnerTaskForTest()
	result := task.Evidence[1]
	task.Evidence = append(task.Evidence, EvidenceRecord{
		Type:      "check",
		Reference: result.Reference,
		Digest:    result.Digest,
	})
	if _, _, err := verifiedCodeRunnerEvidence(task); err != nil {
		t.Fatalf("verification link sharing the immutable result reference was treated as a second attempt: %v", err)
	}

	task.Evidence = append(task.Evidence, result)
	if _, _, err := verifiedCodeRunnerEvidence(task); err == nil || !strings.Contains(err.Error(), "duplicate attempt evidence") {
		t.Fatalf("duplicate result evidence error=%v, want duplicate attempt evidence", err)
	}
}

func TestCompletionValidationCannotPassBeforeCodeRunnerEvidence(t *testing.T) {
	tasks := newMemoryTasks()
	root := codeRunnerRootForTest()
	mission := codeRunnerTaskForTest()
	mission.Status = "ready"
	mission.Attempts = nil
	mission.Evidence = nil
	tasks.tasks[root.ID] = root
	tasks.tasks[mission.ID] = mission

	orchestrator := &Orchestrator{tasks: tasks}
	err := orchestrator.validateRunCompletionEvidence(context.Background(), root, ExecutivePlan{})
	if !errors.Is(err, ErrCodeRunnerExecutionPending) {
		t.Fatalf("completion validation error=%v, want pending CodeRunner evidence", err)
	}
}

func TestCodeRunnerBarrierRecordsOnlyVerifiedExecution(t *testing.T) {
	tasks := newMemoryTasks()
	root := codeRunnerRootForTest()
	mission := codeRunnerTaskForTest()
	tasks.tasks[root.ID] = root
	tasks.tasks[mission.ID] = mission

	orchestrator := &Orchestrator{tasks: tasks}
	if err := orchestrator.ensureRequiredCodeRunnerExecution(context.Background(), root); err != nil {
		t.Fatalf("verified CodeRunner evidence rejected: %v", err)
	}
	updated, err := tasks.GetTask(context.Background(), root.ID)
	if err != nil {
		t.Fatal(err)
	}
	requirement, found := requiredRootRequirement(updated, CodeRunnerExecutionEvidenceRequirementKey)
	if !found || requirement.Status != "satisfied" {
		t.Fatalf("root CodeRunner requirement=%+v, want satisfied", requirement)
	}
	var linked bool
	for _, evidence := range updated.Evidence {
		if evidence.RequirementID == requirement.ID && strings.HasPrefix(evidence.Reference, "code-runner-attempt-evidence://") {
			linked = true
			if evidence.Metadata["mission_task_id"] != mission.ID {
				t.Fatalf("linked evidence metadata=%v, want mission task %d", evidence.Metadata, mission.ID)
			}
		}
	}
	if !linked {
		t.Fatal("root has no evidence linked to the verified CodeRunner attempt")
	}
}
