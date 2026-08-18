package engineeringmission

import (
	"context"
	"strconv"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// taskDetailStub is a minimal TaskPort whose only meaningful behavior is
// GetTask returning a fixed tasks.TaskDetail -- everything VerifyRequiredGates
// and RequestPromotion actually read. The remaining TaskService/TaskReader
// methods are unused no-ops; they exist only because TaskPort is an
// interface composition and Go requires the full method set.
type taskDetailStub struct{ detail tasks.TaskDetail }

func (s taskDetailStub) GetTask(context.Context, int64) (tasks.TaskDetail, error) {
	return s.detail, nil
}
func (taskDetailStub) ListTasks(context.Context, tasks.TaskFilter) ([]tasks.Task, error) {
	return nil, nil
}
func (taskDetailStub) ListEvents(context.Context, int64, int) ([]tasks.Event, error) { return nil, nil }
func (taskDetailStub) ListAttempts(context.Context, int64) ([]tasks.Attempt, error) {
	return nil, nil
}
func (taskDetailStub) ListDeadLetters(context.Context, int) ([]tasks.DeadLetter, error) {
	return nil, nil
}
func (taskDetailStub) GetDeadLetter(context.Context, int64) (tasks.DeadLetter, error) {
	return tasks.DeadLetter{}, nil
}
func (taskDetailStub) CreateTask(context.Context, tasks.CreateRequest, string, string) (tasks.Task, bool, error) {
	return tasks.Task{}, false, nil
}
func (taskDetailStub) AddDependency(context.Context, tasks.AddDependencyCommand) error { return nil }
func (taskDetailStub) AddRequirement(context.Context, tasks.AddRequirementCommand) (tasks.Requirement, error) {
	return tasks.Requirement{}, nil
}
func (taskDetailStub) RecordEvidence(context.Context, tasks.RecordEvidenceCommand) (tasks.Evidence, error) {
	return tasks.Evidence{}, nil
}
func (taskDetailStub) RecordRequirementVerification(context.Context, tasks.RequirementVerificationCommand) (tasks.Evidence, error) {
	return tasks.Evidence{}, nil
}
func (taskDetailStub) FinalizeTask(context.Context, tasks.FinalizeCommand) (tasks.Task, error) {
	return tasks.Task{}, nil
}
func (taskDetailStub) BlockTask(context.Context, tasks.BlockCommand) (tasks.Task, error) {
	return tasks.Task{}, nil
}
func (taskDetailStub) UnblockTask(context.Context, tasks.UnblockCommand) (tasks.Task, error) {
	return tasks.Task{}, nil
}
func (taskDetailStub) CancelTask(context.Context, tasks.CancelCommand) (tasks.Task, error) {
	return tasks.Task{}, nil
}

// attemptEvidence builds one code-runner-attempt-evidence:// entry, in the
// exact JSON-decoded shape coderunner's real evidence.go produces
// (checkEvidence: type/packages/race/integration/success,
// candidateRevision: workspace_id), for a given attempt/workspace and a
// single check result.
func attemptEvidence(attemptID, workspaceID int64, checkType string, packages []string, race, integration, success bool) tasks.Evidence {
	pkgs := make([]any, len(packages))
	for i, p := range packages {
		pkgs[i] = p
	}
	digest := "d"
	return tasks.Evidence{
		Reference: "code-runner-attempt-evidence://task/1/attempt/" + strconv.FormatInt(attemptID, 10),
		Digest:    &digest,
		Metadata: map[string]any{
			"checks_run": []any{
				map[string]any{
					"type":        checkType,
					"packages":    pkgs,
					"race":        race,
					"integration": integration,
					"success":     success,
				},
			},
			"candidate_revision": map[string]any{"workspace_id": float64(workspaceID)},
		},
	}
}

// TestVerifyRequiredGates_WrongPackagesDenied is the round-2 review's P1-1
// regression test: a required gate's Packages must match the evidence's own
// Packages exactly (as a set), not merely share the same Type+success.
func TestVerifyRequiredGates_WrongPackagesDenied(t *testing.T) {
	detail := tasks.TaskDetail{Evidence: []tasks.Evidence{
		attemptEvidence(1, 100, "GO_TEST", []string{"./internal/foo"}, false, false, true),
	}}
	svc := Service{Tasks: taskDetailStub{detail: detail}}
	policy := MissionPolicy{RequiredGates: []RequiredGate{{Type: GateTest, Packages: []string{"./..."}}}}
	if err := svc.VerifyRequiredGates(context.Background(), 1, 100, policy); err == nil {
		t.Fatal("VerifyRequiredGates succeeded despite a package mismatch (./... required, ./internal/foo run), want error")
	}
}

// TestVerifyRequiredGates_WrongRaceDenied is the P1-1 regression test for
// Race: a required gate declaring race=true must not be satisfied by
// evidence recording race=false for the same type/packages.
func TestVerifyRequiredGates_WrongRaceDenied(t *testing.T) {
	detail := tasks.TaskDetail{Evidence: []tasks.Evidence{
		attemptEvidence(1, 100, "GO_TEST", []string{"./..."}, false, false, true),
	}}
	svc := Service{Tasks: taskDetailStub{detail: detail}}
	policy := MissionPolicy{RequiredGates: []RequiredGate{{Type: GateTest, Packages: []string{"./..."}, Race: true}}}
	if err := svc.VerifyRequiredGates(context.Background(), 1, 100, policy); err == nil {
		t.Fatal("VerifyRequiredGates succeeded despite race=true required but evidence recording race=false, want error")
	}
}

// TestVerifyRequiredGates_WrongIntegrationDenied mirrors the Race case for
// Integration.
func TestVerifyRequiredGates_WrongIntegrationDenied(t *testing.T) {
	detail := tasks.TaskDetail{Evidence: []tasks.Evidence{
		attemptEvidence(1, 100, "GO_TEST", []string{"./..."}, false, false, true),
	}}
	svc := Service{Tasks: taskDetailStub{detail: detail}}
	policy := MissionPolicy{RequiredGates: []RequiredGate{{Type: GateTest, Packages: []string{"./..."}, Integration: true}}}
	if err := svc.VerifyRequiredGates(context.Background(), 1, 100, policy); err == nil {
		t.Fatal("VerifyRequiredGates succeeded despite integration=true required but evidence recording integration=false, want error")
	}
}

// TestVerifyRequiredGates_MixedAttemptsDenied is the round-2 review's P1-2
// regression test (attribution): a BUILD check satisfied by one attempt's
// evidence and a TEST check satisfied by a DIFFERENT attempt's evidence
// must never combine to satisfy a policy requiring both -- each required
// gate must be found within the SAME workspace's own attempt evidence.
func TestVerifyRequiredGates_MixedAttemptsDenied(t *testing.T) {
	detail := tasks.TaskDetail{Evidence: []tasks.Evidence{
		attemptEvidence(1, 100, "GO_BUILD", []string{"./..."}, false, false, true), // workspace 100: BUILD only
		attemptEvidence(2, 200, "GO_TEST", []string{"./..."}, false, false, true),  // workspace 200: TEST only
	}}
	svc := Service{Tasks: taskDetailStub{detail: detail}}
	policy := MissionPolicy{RequiredGates: []RequiredGate{
		{Type: GateBuild, Packages: []string{"./..."}},
		{Type: GateTest, Packages: []string{"./..."}},
	}}
	// Promoting workspace 100 must fail: it only has BUILD, never TEST --
	// even though TEST exists somewhere else on the same task, attached to
	// a different workspace/attempt.
	if err := svc.VerifyRequiredGates(context.Background(), 1, 100, policy); err == nil {
		t.Fatal("VerifyRequiredGates succeeded for workspace 100 by borrowing a GO_TEST check that belongs to workspace 200's attempt, want error")
	}
	// Symmetric check: workspace 200 only has TEST, never BUILD.
	if err := svc.VerifyRequiredGates(context.Background(), 1, 200, policy); err == nil {
		t.Fatal("VerifyRequiredGates succeeded for workspace 200 by borrowing a GO_BUILD check that belongs to workspace 100's attempt, want error")
	}
}

// TestVerifyRequiredGates_EvidenceFromOtherWorkspaceDenied is the P1-2
// regression test for the narrower "evidence exists, but for a different
// workspace than the one being promoted" case: a task whose only attempt
// evidence claims an unrelated workspace ID must never satisfy gates for
// the workspace actually being promoted.
func TestVerifyRequiredGates_EvidenceFromOtherWorkspaceDenied(t *testing.T) {
	detail := tasks.TaskDetail{Evidence: []tasks.Evidence{
		attemptEvidence(1, 999, "GO_TEST", []string{"./..."}, false, false, true), // claims workspace 999
	}}
	svc := Service{Tasks: taskDetailStub{detail: detail}}
	policy := MissionPolicy{RequiredGates: []RequiredGate{{Type: GateTest, Packages: []string{"./..."}}}}
	// Promoting workspace 100 (not 999) must fail closed: no attempt
	// evidence claims workspace 100 at all.
	if err := svc.VerifyRequiredGates(context.Background(), 1, 100, policy); err == nil {
		t.Fatal("VerifyRequiredGates succeeded for workspace 100 using evidence that actually claims workspace 999, want error")
	}
}

// TestVerifyRequiredGates_ExactMatchSatisfies is the positive control for
// all the above: identical Type/Packages/Race/Integration/success and the
// same workspace ID must still succeed, proving the stricter comparison
// isn't simply always rejecting.
func TestVerifyRequiredGates_ExactMatchSatisfies(t *testing.T) {
	detail := tasks.TaskDetail{Evidence: []tasks.Evidence{
		attemptEvidence(1, 100, "GO_TEST", []string{"./..."}, true, false, true),
	}}
	svc := Service{Tasks: taskDetailStub{detail: detail}}
	policy := MissionPolicy{RequiredGates: []RequiredGate{{Type: GateTest, Packages: []string{"./..."}, Race: true}}}
	if err := svc.VerifyRequiredGates(context.Background(), 1, 100, policy); err != nil {
		t.Fatalf("VerifyRequiredGates failed for an exact-matching gate: %v", err)
	}
}
