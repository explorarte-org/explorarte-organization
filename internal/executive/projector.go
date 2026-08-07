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
	if root.Status == "failed" || root.Status == "rejected" || root.Status == "dead_letter" {
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
		if t.Status == "failed" || t.Status == "rejected" || t.Status == "dead_letter" {
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
