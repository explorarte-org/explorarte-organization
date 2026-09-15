package driver

import (
	"errors"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// ResultClassification categorizes the outcome of an Executive advancement pass.
type ResultClassification string

const (
	// ResultContinue indicates the root made progress and remains active and eligible.
	ResultContinue ResultClassification = "CONTINUE"

	// ResultTerminal indicates the root reached a terminal state (completed, failed, cancelled).
	ResultTerminal ResultClassification = "TERMINAL"

	// ResultBusy indicates another worker or replica currently holds an active lease or lock on this root or its children.
	ResultBusy ResultClassification = "BUSY"

	// ResultRetryLater indicates a transient condition that will resolve itself after a delay.
	ResultRetryLater ResultClassification = "RETRY_LATER"

	// ResultBlockedHuman indicates the campaign is blocked awaiting human/owner review or decision.
	ResultBlockedHuman ResultClassification = "BLOCKED_HUMAN"

	// ResultBlockedSafety indicates the campaign is blocked due to safety/correctness invariants (e.g. indeterminate tool side effects).
	ResultBlockedSafety ResultClassification = "BLOCKED_SAFETY"

	// ResultInfraFailure indicates an infrastructure, database, or unexpected operational failure.
	ResultInfraFailure ResultClassification = "INFRA_FAILURE"
)

// ClassifyResult deterministically maps a ResumeDurable outcome to a canonical classification.
// It relies strictly on typed error checking (errors.Is) and typed Run state inspection,
// never string pattern matching.
func ClassifyResult(run executive.Run, err error) ResultClassification {
	if err == nil {
		if run.State.Terminal() {
			return ResultTerminal
		}
		return ResultContinue
	}

	switch {
	case errors.Is(err, executive.ErrActiveLeaseBarrier):
		return ResultBusy

	case errors.Is(err, executive.ErrIndeterminateToolExecution):
		// A tool execution was indeterminate; external side effects cannot be safely re-driven.
		return ResultBlockedSafety

	case errors.Is(err, executive.ErrOrphanedModelResult),
		errors.Is(err, executive.ErrModelOutcomeAmbiguous):
		return ResultBlockedSafety

	case errors.Is(err, executive.ErrRunBlocked),
		errors.Is(err, executive.ErrCompletionInconclusive):
		return ResultBlockedHuman

	case errors.Is(err, executive.ErrLeaseLost),
		errors.Is(err, executive.ErrPriorExecutionUnresolved),
		errors.Is(err, executive.ErrExecutionInterrupted),
		errors.Is(err, executive.ErrExecutionAuthorityUnavailable),
		errors.Is(err, executive.ErrExecutionPrincipalUnavailable),
		errors.Is(err, executive.ErrDispatchAssignmentRequired):
		return ResultRetryLater

	default:
		return ResultInfraFailure
	}
}
