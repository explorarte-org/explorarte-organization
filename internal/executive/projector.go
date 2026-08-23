package executive

import "strings"

const (
	keyCEOPlanMarker    = ":ceo-plan"
	keyLeaderPlanMarker = ":leader-plan:"
	keyWorkerMarker     = ":worker:"
	keyReviewMarker     = ":leader-review:"
	keyClosureMarker    = ":ceo-closure"
)

func ProjectRun(root TaskRecord, children []TaskRecord) Run {
	run := Run{RootTaskID: root.ID, CorrelationID: root.CorrelationID, State: StateAccepted, ReasonCode: root.ReasonCode, Reason: root.Reason}
	if root.Status == "completed" {
		run.State = StateCompleted
		return run
	}
	if isFailedTask(root.Status) {
		run.State = StateFailed
		return run
	}
	if root.Status == "cancelled" {
		run.State = StateCancelled
		return run
	}
	if root.Status == "blocked" {
		run.State = StateBlocked
		return run
	}
	var hasCEOPlan, ceoPlanDone, hasLeaderPlan, leaderPlanDone, hasWorker, workersDone, hasReview, reviewsDone, hasClosure bool
	workersDone = true
	leaderPlanDone = true
	reviewsDone = true
	for _, t := range children {
		k := t.IdempotencyKey
		switch {
		case strings.Contains(k, keyCEOPlanMarker):
			hasCEOPlan = true
			ceoPlanDone = ceoPlanDone || t.Status == "completed"
		case strings.Contains(k, keyLeaderPlanMarker):
			hasLeaderPlan = true
			if t.Status != "completed" {
				leaderPlanDone = false
			}
		case strings.Contains(k, keyWorkerMarker):
			hasWorker = true
			if t.Status != "completed" && t.Status != "no_action" {
				workersDone = false
			}
		case strings.Contains(k, keyReviewMarker):
			hasReview = true
			if t.Status != "completed" {
				reviewsDone = false
			}
		case strings.Contains(k, keyClosureMarker):
			hasClosure = true
			if t.Status == "completed" {
				run.State = StateCompletionVerification
			} else {
				run.State = StateCEOClosure
			}
			return run
		}
		if t.Status == "blocked" {
			run.State = StateBlocked
			run.ReasonCode = t.ReasonCode
			run.Reason = t.Reason
			return run
		}
		if isFailedTask(t.Status) {
			run.State = StateFailed
			run.ReasonCode = t.ReasonCode
			run.Reason = t.Reason
			return run
		}
	}
	_ = hasClosure
	switch {
	case !hasCEOPlan || !ceoPlanDone:
		run.State = StateCEOPlanning
	case hasLeaderPlan && !leaderPlanDone:
		run.State = StateDepartmentPlanning
	case hasWorker && !workersDone:
		run.State = StateWorkerExecution
	case hasReview && !reviewsDone:
		run.State = StateDepartmentReview
	case hasReview && reviewsDone:
		run.State = StateCEOClosure
	case hasLeaderPlan && leaderPlanDone && !hasWorker:
		run.State = StateDepartmentReview
	default:
		run.State = StateDepartmentPlanning
	}
	return run
}

// isFailedTask reports a task that ended without producing its result and
// cannot produce one later.
//
// It is one function, used by the projector that REPORTS a run as failed and
// by the orchestrator that has to ACT on it. Those were two different pieces
// of knowledge until AUTONOMY-SMOKE-013: the projector correctly reported the
// run as failed the moment a review task dead-lettered, and nothing anywhere
// read that projection, so the root sat ready forever while every pass of the
// worker returned no error and no progress.
//
// "blocked" is deliberately absent. A blocked task is waiting for someone, and
// waiting is not failing.
func isFailedTask(status string) bool {
	switch status {
	case "failed", "rejected", "dead_letter":
		return true
	default:
		return false
	}
}
