package executive

import (
	"context"
	"strings"
	"testing"
)

// Authorization is the tuple (task, invocation, result digest, reference).
// Before this, three of its four elements came from labels the artifact
// asserted rather than facts the host confirmed: the invocation's own task was
// never compared, the result bytes were never hashed against the recorded
// digest, and an invocation with no snapshot was silently skipped.
//
// A tuple assembled from unchecked labels is not a binding. It reads like one.

type mismatchModels struct {
	invocation InvocationRecord
	result     InvocationResult
}

func (m mismatchModels) GetInvocation(context.Context, int64) (InvocationRecord, error) {
	return m.invocation, nil
}
func (m mismatchModels) GetResult(context.Context, int64) (InvocationResult, error) {
	return m.result, nil
}
func (m mismatchModels) FindTaskAttemptInvocations(context.Context, int64, int64) ([]InvocationRecord, error) {
	return nil, nil
}
func (m mismatchModels) ProviderFailureRetryable(context.Context, int64) (bool, error) {
	return false, nil
}

func citationFixture(t *testing.T, invocation InvocationRecord, result InvocationResult, unit designUnitRef) error {
	t.Helper()
	tasksPort := newMemoryTasks()
	const rootID int64 = 1
	pin := DesignBaseSHAReference + "1"
	tasksPort.tasks[rootID] = TaskRecord{
		ID: rootID, CorrelationID: "executive:pin", AssignedRoleID: CEORoleID,
		Evidence: []EvidenceRecord{{Reference: pin, Metadata: map[string]any{"design_base_sha": designSHA}}},
	}
	orchestrator := &Orchestrator{
		tasks: tasksPort, models: mismatchModels{invocation: invocation, result: result},
		snapshotSources: snapshotWith(),
	}
	_, err := orchestrator.verifiedDesignCitations(context.Background(),
		tasksPort.tasks[rootID], designArtifact{Units: []designUnitRef{unit}})
	return err
}

// The invocation must belong to the task the deliverable claims. An invocation
// from another task cannot ground these claims, however genuine its own
// citations are.
func TestAnInvocationFromAnotherTaskCannotGroundThisDeliverable(t *testing.T) {
	err := citationFixture(t,
		InvocationRecord{ID: 5, TaskID: 999, ContextSnapshotID: 7},
		InvocationResult{InvocationID: 5, ResponseHash: "d1", TextOutput: "see " + realCite},
		designUnitRef{UnitID: "ingenieria_ia", TaskID: 2, InvocationID: 5, ResultHash: "d1"})
	if err == nil {
		t.Fatal("an invocation belonging to another task must not authorize this deliverable")
	}
	if !strings.Contains(err.Error(), "belongs to task") {
		t.Fatalf("the refusal must name the mismatch, got %v", err)
	}
}

// The text verified must be the text the artifact recorded. Otherwise
// references extracted from whatever the store returns today are published
// under a digest describing different bytes.
func TestCitationsAreNotPublishedUnderAnotherResultsDigest(t *testing.T) {
	err := citationFixture(t,
		InvocationRecord{ID: 5, TaskID: 2, ContextSnapshotID: 7},
		// The store returns text whose hash is not the one recorded.
		InvocationResult{InvocationID: 5, ResponseHash: "actual-d2", TextOutput: "see " + realCite},
		designUnitRef{UnitID: "ingenieria_ia", TaskID: 2, InvocationID: 5, ResultHash: "recorded-d1"})
	if err == nil {
		t.Fatal("citations from bytes that are not the recorded deliverable must not be authorized")
	}
	if !strings.Contains(err.Error(), "hashes") {
		t.Fatalf("the refusal must name the digest mismatch, got %v", err)
	}
}

// No snapshot means there is no record of what the model was shown. Skipping
// it left the deliverable out of deliverables[] while its text stayed in the
// candidate design: claims with no owner beside other deliverables'
// references, which is the laundering the structure exists to prevent,
// arriving through omission instead of aggregation.
func TestADeliverableWithNoSnapshotIsRefusedNotSkipped(t *testing.T) {
	err := citationFixture(t,
		InvocationRecord{ID: 5, TaskID: 2, ContextSnapshotID: 0},
		InvocationResult{InvocationID: 5, ResponseHash: "d1", TextOutput: "see " + realCite},
		designUnitRef{UnitID: "ingenieria_ia", TaskID: 2, InvocationID: 5, ResultHash: "d1"})
	if err == nil {
		t.Fatal("a deliverable whose context is unknown must be refused, not silently omitted")
	}
	if !strings.Contains(err.Error(), "context snapshot") {
		t.Fatalf("the refusal must say the context is unknown, got %v", err)
	}
}

// And the well-formed case still authorizes, bound to all four elements.
func TestAWellFormedDeliverableIsAuthorizedWithItsWholeTuple(t *testing.T) {
	tasksPort := newMemoryTasks()
	const rootID int64 = 1
	tasksPort.tasks[rootID] = TaskRecord{
		ID: rootID, CorrelationID: "executive:pin", AssignedRoleID: CEORoleID,
		Evidence: []EvidenceRecord{{Reference: DesignBaseSHAReference + "1",
			Metadata: map[string]any{"design_base_sha": designSHA}}},
	}
	orchestrator := &Orchestrator{
		tasks: tasksPort,
		models: mismatchModels{
			invocation: InvocationRecord{ID: 5, TaskID: 2, ContextSnapshotID: 7},
			result:     InvocationResult{InvocationID: 5, ResponseHash: "d1", TextOutput: "see " + realCite},
		},
		snapshotSources: snapshotWith(),
	}
	deliverables, err := orchestrator.verifiedDesignCitations(context.Background(),
		tasksPort.tasks[rootID],
		designArtifact{Units: []designUnitRef{{UnitID: "ingenieria_ia", TaskID: 2, InvocationID: 5, ResultHash: "d1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(deliverables) != 1 {
		t.Fatalf("deliverables=%+v", deliverables)
	}
	got := deliverables[0]
	if got.TaskID != 2 || got.InvocationID != 5 || got.ResultDigest != "d1" {
		t.Fatalf("the tuple must be complete, got %+v", got)
	}
	if len(got.VerifiedRepositoryRefs) != 1 || got.VerifiedRepositoryRefs[0] != realCite {
		t.Fatalf("refs=%v", got.VerifiedRepositoryRefs)
	}
}
