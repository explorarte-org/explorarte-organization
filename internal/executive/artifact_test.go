package executive

import (
	"context"
	"testing"
)

type artifactModels struct{ results map[int64]InvocationResult }

func (f artifactModels) GetInvocation(context.Context, int64) (InvocationRecord, error) {
	panic("unused")
}
func (f artifactModels) ProviderFailureRetryable(context.Context, int64) (bool, error) {
	panic("unused")
}

func (f artifactModels) FindTaskAttemptInvocations(_ context.Context, taskID, attemptID int64) ([]InvocationRecord, error) {
	if _, ok := f.results[taskID]; !ok {
		return nil, nil
	}
	return []InvocationRecord{{ID: taskID, TaskID: taskID, AttemptID: attemptID, Status: "succeeded"}}, nil
}

func (f artifactModels) GetResult(_ context.Context, id int64) (InvocationResult, error) {
	return f.results[id], nil
}

func finished(id int64) []AttemptRecord {
	return []AttemptRecord{{ID: id * 10, Ordinal: 1, State: "finished"}}
}

// The candidate design is what the departments produced, never the verdicts
// their leaders returned about them.
//
// Those were confused for a long time and nothing could tell, because the
// artifact only surfaced as identities and digests. The rule is cheap to
// state once it is known, and this is the statement: a review verdict is a
// judgement about the design, so including it as the design asks the next
// reviewer to judge the previous reviewer.
func TestTheArtifactIsTheDeliverablesAndNotTheVerdictAboutThem(t *testing.T) {
	root := TaskRecord{ID: 1, CorrelationID: "c"}
	all := []TaskRecord{
		{ID: 20, TaskClass: "engineering.design", Status: "completed", AssignedUnitID: "ingenieria_ia",
			IdempotencyKey: "executive:1:worker:ingenieria_ia:design", Attempts: finished(20)},
		{ID: 21, TaskClass: "engineering.design_finalize", Status: "completed", AssignedUnitID: "ingenieria_ia",
			IdempotencyKey: "executive:1:worker:ingenieria_ia:finalize", Attempts: finished(21)},
		{ID: 30, TaskClass: TaskClassCoordinationDeptReview, Status: "completed", AssignedUnitID: "ingenieria_ia",
			IdempotencyKey: "executive:1:leader-review:ingenieria_ia", Attempts: finished(30)},
	}
	o := &Orchestrator{models: artifactModels{results: map[int64]InvocationResult{
		20: {InvocationID: 20, TextOutput: "the design", ResponseHash: "h20"},
		21: {InvocationID: 21, TextOutput: "the finalized design", ResponseHash: "h21"},
		30: {InvocationID: 30, TextOutput: "verdict: accept", ResponseHash: "h30"},
	}}}

	artifact, units, ok := o.candidateDesign(context.Background(), root, all, 1)
	if !ok {
		t.Fatal("a department with completed deliverables must produce an artifact")
	}
	if len(artifact.Units) != 2 {
		t.Fatalf("the artifact is the SET of deliverables, got %d: %+v", len(artifact.Units), artifact.Units)
	}
	for _, unit := range artifact.Units {
		if unit.TaskID == 30 {
			t.Fatal("the leader's review verdict is a judgement about the design, not the design")
		}
	}
	if artifact.Units[0].TaskID != 20 || artifact.Units[1].TaskID != 21 {
		t.Fatalf("deliverables must be ordered deterministically: %+v", artifact.Units)
	}
	if len(units) != 1 || units[0] != "ingenieria_ia" {
		t.Fatalf("one entry per contributing department, got %v", units)
	}
}

// A completed review still decides WHICH departments contribute. It gates;
// it is not the content.
func TestADepartmentWithoutACompletedReviewDoesNotContribute(t *testing.T) {
	root := TaskRecord{ID: 1, CorrelationID: "c"}
	all := []TaskRecord{
		{ID: 20, TaskClass: "engineering.design", Status: "completed", AssignedUnitID: "ingenieria_ia",
			IdempotencyKey: "executive:1:worker:ingenieria_ia:design", Attempts: finished(20)},
		{ID: 30, TaskClass: TaskClassCoordinationDeptReview, Status: "running", AssignedUnitID: "ingenieria_ia",
			IdempotencyKey: "executive:1:leader-review:ingenieria_ia"},
	}
	o := &Orchestrator{models: artifactModels{results: map[int64]InvocationResult{
		20: {InvocationID: 20, TextOutput: "the design", ResponseHash: "h20"},
	}}}
	if _, _, ok := o.candidateDesign(context.Background(), root, all, 1); ok {
		t.Fatal("a department whose leader has not finished judging it must not contribute a design")
	}
}

// A failed worker produced no deliverable. Its failure was already weighed by
// the leader review; presenting nothing as part of the design would be worse
// than presenting less.
func TestAFailedWorkerContributesNothingToTheDesign(t *testing.T) {
	root := TaskRecord{ID: 1, CorrelationID: "c"}
	all := []TaskRecord{
		{ID: 20, TaskClass: "engineering.design", Status: "completed", AssignedUnitID: "ingenieria_ia",
			IdempotencyKey: "executive:1:worker:ingenieria_ia:design", Attempts: finished(20)},
		{ID: 22, TaskClass: "engineering.security_review", Status: "failed", AssignedUnitID: "ingenieria_ia",
			IdempotencyKey: "executive:1:worker:ingenieria_ia:security", Attempts: finished(22)},
		{ID: 30, TaskClass: TaskClassCoordinationDeptReview, Status: "completed", AssignedUnitID: "ingenieria_ia",
			IdempotencyKey: "executive:1:leader-review:ingenieria_ia", Attempts: finished(30)},
	}
	o := &Orchestrator{models: artifactModels{results: map[int64]InvocationResult{
		20: {InvocationID: 20, TextOutput: "the design", ResponseHash: "h20"},
		22: {InvocationID: 22, TextOutput: "should not appear", ResponseHash: "h22"},
		30: {InvocationID: 30, TextOutput: "verdict", ResponseHash: "h30"},
	}}}
	artifact, _, ok := o.candidateDesign(context.Background(), root, all, 1)
	if !ok {
		t.Fatal("one completed deliverable is still a design")
	}
	if len(artifact.Units) != 1 || artifact.Units[0].TaskID != 20 {
		t.Fatalf("a failed worker contributes nothing: %+v", artifact.Units)
	}
}
