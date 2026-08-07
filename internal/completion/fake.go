package completion

import "context"

// fakeTasks, fakeArtifacts, fakeChecks, fakeApprovals and fakeBranches back the
// domain-level tests in service_test.go. The real adapters (task #19) read
// internal/tasks, internal/staging, internal/authorization and
// internal/decisiongraph's Postgres tables directly.

type fakeTasks struct {
	facts map[int64]TaskFact
	err   error
}

func (f *fakeTasks) TaskFacts(_ context.Context, taskID int64) (TaskFact, error) {
	if f.err != nil {
		return TaskFact{}, f.err
	}
	fact, ok := f.facts[taskID]
	if !ok {
		return TaskFact{}, ErrTaskNotFound
	}
	return fact, nil
}

type fakeArtifacts struct {
	digests map[string]string
}

func (f *fakeArtifacts) ArtifactDigest(_ context.Context, reference string) (string, error) {
	digest, ok := f.digests[reference]
	if !ok {
		return "", ErrArtifactNotFound
	}
	return digest, nil
}

type fakeChecks struct {
	passed map[int64]bool // keyed by requirementID
}

func (f *fakeChecks) CheckPassed(_ context.Context, _, requirementID int64) (bool, error) {
	return f.passed[requirementID], nil
}

type fakeApprovals struct {
	consumed map[string]string // requestRef -> action digest that was actually authorized
}

func (f *fakeApprovals) ApprovalConsumed(_ context.Context, requestRef, actionDigest string) (bool, error) {
	digest, ok := f.consumed[requestRef]
	if !ok {
		return false, nil
	}
	return digest == actionDigest, nil
}

type fakeBranches struct {
	// keyed by "taskID:attemptID"
	states map[string]string
}

func (f *fakeBranches) CurrentBranchStateForAttempt(_ context.Context, taskID, attemptID int64) (string, bool, error) {
	state, ok := f.states[itoa(taskID)+":"+itoa(attemptID)]
	return state, ok, nil
}
