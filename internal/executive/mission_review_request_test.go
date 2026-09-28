package executive

import (
	"context"
	"errors"
	"testing"
)

// See mission_review_request.go: smokes #20 to #22 reached the closure with the mission's gates still
// pending, because nothing in the governed path asked for the mission's review.

type recordingReviewRequester struct {
	calls         [][2]int64
	gatesRecorded []bool
	err           error
}

func (r *recordingReviewRequester) RequestMissionReview(_ context.Context, missionTaskID, workspaceID int64, gatesRecorded bool) error {
	r.calls = append(r.calls, [2]int64{missionTaskID, workspaceID})
	r.gatesRecorded = append(r.gatesRecorded, gatesRecorded)
	return r.err
}

func missionWithGatesPending() TaskRecord {
	mission := codeRunnerTaskForTest()
	for i := range mission.Requirements {
		if mission.Requirements[i].Key == missionGatesRequirementKey {
			mission.Requirements[i].Status = "pending"
		}
	}
	return mission
}

func reviewFixture(mission TaskRecord, requester MissionReviewRequester) (*Orchestrator, TaskRecord) {
	tasks := newMemoryTasks()
	root := codeRunnerRootForTest()
	tasks.tasks[root.ID] = root
	tasks.tasks[mission.ID] = mission
	o := &Orchestrator{tasks: tasks}
	if requester != nil {
		WithMissionReviewRequester(requester)(o)
	}
	return o, root
}

func TestAVerifiedMissionAsksForItsReviewOnce(t *testing.T) {
	requester := &recordingReviewRequester{}
	o, root := reviewFixture(missionWithGatesPending(), requester)
	if err := o.ensureRequiredCodeRunnerExecution(context.Background(), root); err != nil {
		t.Fatalf("verified execution refused: %v", err)
	}
	if len(requester.calls) != 1 || requester.calls[0] != [2]int64{91, 501} {
		t.Fatalf("review requests=%v, want one for mission 91 / workspace 501", requester.calls)
	}
}

// A mission whose gates are recorded is still asked, telling the requester the check is durable, so a
// promotion lost between the two writes is opened on the next pass (external audit A2). The requester
// opens nothing when the promotion exists.
func TestAMissionWhoseGatesAreRecordedIsAskedWithoutRecordingThemAgain(t *testing.T) {
	requester := &recordingReviewRequester{}
	o, root := reviewFixture(codeRunnerTaskForTest(), requester) // gates satisfied in the fixture
	if err := o.ensureRequiredCodeRunnerExecution(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if len(requester.calls) != 1 || !requester.gatesRecorded[0] {
		t.Fatalf("calls=%v gatesRecorded=%v, want one request that says the gates are recorded", requester.calls, requester.gatesRecorded)
	}
}

// External audit A2 (2026-09-27), reproduced by fault injection: the gate check commits, the promotion
// request fails. The next pass must ask again -- for the promotion only -- instead of accepting the
// execution with nothing to review.
type partialCommitRequester struct {
	tasks         *memoryTasks
	gatesRecorded []bool
}

func (r *partialCommitRequester) RequestMissionReview(_ context.Context, taskID, _ int64, gatesRecorded bool) error {
	r.gatesRecorded = append(r.gatesRecorded, gatesRecorded)
	if len(r.gatesRecorded) > 1 {
		return nil
	}
	mission := r.tasks.tasks[taskID]
	for i := range mission.Requirements {
		if mission.Requirements[i].Key == missionGatesRequirementKey {
			mission.Requirements[i].Status = "satisfied"
		}
	}
	r.tasks.tasks[taskID] = mission
	return errors.New("injected failure after the gate check, before the promotion exists")
}

func TestAPromotionLostAfterTheGateCheckIsRequestedOnTheNextPass(t *testing.T) {
	o, root := reviewFixture(missionWithGatesPending(), nil)
	requester := &partialCommitRequester{tasks: o.tasks.(*memoryTasks)}
	o.missionReviews = requester
	if err := o.ensureRequiredCodeRunnerExecution(context.Background(), root); !errors.Is(err, ErrCodeRunnerExecutionPending) {
		t.Fatalf("first pass: %v, want pending", err)
	}
	if err := o.ensureRequiredCodeRunnerExecution(context.Background(), root); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if len(requester.gatesRecorded) != 2 || requester.gatesRecorded[0] || !requester.gatesRecorded[1] {
		t.Fatalf("requests gatesRecorded=%v, want [false true]: the second pass asks for the promotion only", requester.gatesRecorded)
	}
}

func TestAFailedReviewRequestLeavesTheExecutionPendingNotBlocked(t *testing.T) {
	requester := &recordingReviewRequester{err: errors.New("staging unavailable")}
	o, root := reviewFixture(missionWithGatesPending(), requester)
	err := o.ensureRequiredCodeRunnerExecution(context.Background(), root)
	if !errors.Is(err, ErrCodeRunnerExecutionPending) || errors.Is(err, ErrCodeRunnerExecutionInvalid) || errors.Is(err, ErrCodeRunnerExecutionFailed) {
		t.Fatalf("a failed review request = %v, want pending (retried on the next pass)", err)
	}
}

func TestWithoutARequesterNothingIsAsked(t *testing.T) {
	o, root := reviewFixture(missionWithGatesPending(), nil)
	if err := o.ensureRequiredCodeRunnerExecution(context.Background(), root); err != nil {
		t.Fatalf("an executive without a requester refused a verified execution: %v", err)
	}
}

func TestAReviewRequestNeedsTheSealedWorkspace(t *testing.T) {
	mission := missionWithGatesPending()
	for i, evidence := range mission.Evidence {
		if revision, ok := evidence.Metadata["candidate_revision"].(map[string]any); ok {
			delete(revision, "workspace_id")
			mission.Evidence[i] = evidence
		}
	}
	requester := &recordingReviewRequester{}
	o := &Orchestrator{missionReviews: requester}
	var attempt EvidenceRecord
	for _, evidence := range mission.Evidence {
		if evidence.Type == "result" {
			attempt = evidence
		}
	}
	if err := o.requestMissionReview(context.Background(), mission, attempt); !errors.Is(err, ErrCodeRunnerExecutionInvalid) || len(requester.calls) != 0 {
		t.Fatalf("err=%v calls=%v, want invalid and no request", err, requester.calls)
	}
}
