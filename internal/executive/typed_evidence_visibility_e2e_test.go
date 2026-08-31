package executive

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TYPED-EVIDENCE-VISIBILITY-FIX-005, end to end.
//
// The wiring under test is driveTypedTask's own PurposeDepartmentReview
// provenance wrapper (orchestrator.go), which reads task.Evidence directly --
// exactly the pattern TestBugA_PreexistingTaskGetsGuidanceAtExecutionTime
// (bug_fixes_test.go) already established for exercising this function
// against a hand-built task record instead of driving a whole campaign to
// the review stage. A bundle attached via RecordEvidence before
// driveTypedTask is called reproduces the exact durable shape
// EvidenceTasks.attachDepartmentBundle (runtimeadapter/evidence_tasks.go)
// leaves in production: evidence recorded on the task before its own attempt
// ever starts.
//
// testOrchestratorWithHarness wires no SnapshotSourceReader by default, so
// each test sets orchestrator.snapshotSources directly (an in-package test,
// same as every other file here) to the task_context segment the model was
// actually shown -- the fact describeInadmissibleReferences' execution
// scoping depends on.

func reviewRequirement() []RequirementProposal {
	return []RequirementProposal{{Key: "typed_review", Type: "result", Description: "Validated DepartmentReview invocation result", Required: true}}
}

// TEST I -- THE INCIDENT, end to end: a department review offers
// model-invocation:21 as evidence for an "accept" verdict, where the ONLY
// reason that ref is real is a bundle the host itself attached to this exact
// review task. VerifyEvidenceProvenance correctly rejects it either way
// (repository_evidence remains the only citable class); what this proves is
// that the rejection is now truthful instead of falsely claiming the host
// never showed it.
func TestTypedVisibility_I_DepartmentReviewEmbeddedRefIsRejectedTruthfully(t *testing.T) {
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	harness := newFakeHarness(models)
	harness.body = json.RawMessage(
		`{"schema_version":"department-review/v2","verdict":"accept",` +
			`"findings":["design matches invocation 21"],"unsatisfied_criteria":[],` +
			`"evidence_refs":["model-invocation:21"],` +
			`"proposed_followup_tasks":[],"followup_ownership":[],"revision_outcomes":[]}`)
	orchestrator := testOrchestratorWithHarness(t, tasksPort, models, harness, &countingBudget{}, &fakeCompletion{verdict: CompletionPass})

	root, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, IdempotencyKey: "root-typed-visibility-i",
		Title: "root", Instructions: "root", AcceptanceCriteria: []string{"x"},
		CorrelationID: "executive:typed-visibility-i",
	})
	task, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador",
		IdempotencyKey: "child-typed-visibility-i", Title: "Department review: ingenieria_ia",
		Instructions:       "Review only this bounded durable task/evidence summary and return DepartmentReview JSON.",
		AcceptanceCriteria: []string{"x"}, CorrelationID: root.CorrelationID,
		Requirements: reviewRequirement(),
	})

	// The durable fact EvidenceTasks writes in production, before this
	// task's own attempt ever starts: a bundle summarizing the deliverables
	// under review, embedding the real invocation identifiers that produced
	// them, recorded with Satisfies: false (requirement_id NULL) because the
	// bundle itself is never authoritative for anything.
	if err := tasksPort.RecordEvidence(context.Background(), EvidenceCommand{
		TaskID: task.ID, Type: "result",
		Reference: "executive-evidence:department:ingenieria_ia:74ae8f9df6b6d17e",
		Metadata: map[string]any{"bundle": map[string]any{
			"schema_version": "executive-evidence.v1",
			"department_id":  "ingenieria_ia",
			"workers": []any{map[string]any{
				"task_id": float64(12432), "evidence_refs": []any{}, "task_evidence_refs": []any{"model-invocation:21"},
			}},
		}},
		Satisfies: false, RecordedBy: "executive-orchestrator",
	}); err != nil {
		t.Fatalf("attaching the executive-evidence bundle: %v", err)
	}
	task, err := tasksPort.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}

	// The task_context segment this task's own attempt was actually shown --
	// the fact execution scoping checks before trusting any embedded ref.
	orchestrator.snapshotSources = stubSnapshotSources{sources: []SnapshotSource{
		{Kind: "task_context", Reference: taskRef(task.ID), Version: "task.v1:1:x", Included: true},
	}}

	_, err = orchestrator.driveTypedTask(context.Background(), root, task, departmentReviewOutputSchema, PurposeDepartmentReview,
		func(result InvocationResult) error {
			_, pErr := ParseDepartmentReview(result.JSONOutput, orchestrator.limits)
			return pErr
		})
	if err == nil || !errors.Is(err, ErrModelResultContractRejected) {
		t.Fatalf("a department review citing a bundle-embedded, non-repository reference must be rejected, got: %v", err)
	}
	if strings.Contains(err.Error(), "cannot verify was shown") {
		t.Fatalf("a genuinely attached bundle ref must not be told it was never shown: %v", err)
	}
	if !strings.Contains(err.Error(), "model-invocation:21") {
		t.Fatalf("rejection does not name the offending ref: %v", err)
	}
}

// TEST J -- the honest, intended answer for a review with nothing citable:
// evidence_refs: [] alongside a normal accept verdict must still pass. This
// fix changes no acceptance policy -- an empty offer needed no provenance
// before and needs none now.
func TestTypedVisibility_J_DepartmentReviewEmptyEvidenceStillPasses(t *testing.T) {
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	harness := newFakeHarness(models)
	harness.body = json.RawMessage(
		`{"schema_version":"department-review/v2","verdict":"accept",` +
			`"findings":["reviewed"],"unsatisfied_criteria":[],"evidence_refs":[],` +
			`"proposed_followup_tasks":[],"followup_ownership":[],"revision_outcomes":[]}`)
	orchestrator := testOrchestratorWithHarness(t, tasksPort, models, harness, &countingBudget{}, &fakeCompletion{verdict: CompletionPass})

	root, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, IdempotencyKey: "root-typed-visibility-j",
		Title: "root", Instructions: "root", AcceptanceCriteria: []string{"x"},
		CorrelationID: "executive:typed-visibility-j",
	})
	task, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador",
		IdempotencyKey: "child-typed-visibility-j", Title: "Department review: ingenieria_ia",
		Instructions:       "Review only this bounded durable task/evidence summary and return DepartmentReview JSON.",
		AcceptanceCriteria: []string{"x"}, CorrelationID: root.CorrelationID,
		Requirements: reviewRequirement(),
	})

	if _, err := orchestrator.driveTypedTask(context.Background(), root, task, departmentReviewOutputSchema, PurposeDepartmentReview,
		func(result InvocationResult) error {
			_, pErr := ParseDepartmentReview(result.JSONOutput, orchestrator.limits)
			return pErr
		}); err != nil {
		t.Fatalf("an honest empty-evidence department review was rejected: %v", err)
	}
}
