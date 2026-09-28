package executive

import (
	"context"
	"fmt"
)

// A mission that fails deterministically goes back to its implementation plan, not to the design.
//
// Local smoke #40 (root 1990, 2026-09-28): the first self-audit to reach the code runner froze its
// design, planned it and provisioned a mission; the mission applied its patches, built, and failed
// `go vet` because the new test's fake did not implement an interface method. The code runner
// classified it check_failed and did not retry the same plan (audit A7), and the root blocked -- a
// compile error in a test cost the whole campaign, although the frozen design was sound.
//
// The external audit's A7 asks exactly this: deterministic defects return to the plan with their
// evidence. After a mission fails with check_failed or patch_not_applicable, the executive asks the
// implementation planner once more -- the same frozen design and file manifest, plus the failure's own
// output and the patch that failed -- and provisions a new mission from that plan through the same
// patch validation and manifest checks. Nothing about the design is revisited. The new mission's
// evidence names the one it supersedes, and missionTaskID follows that chain.

// MaxImplementationRetries bounds how many times a root re-plans its implementation after failures.
const MaxImplementationRetries = 2

// SupersedesMissionKey is the mission evidence metadata naming the failed mission a retry replaces.
const SupersedesMissionKey = "supersedes_mission_task_id"

// retryableMissionFailures are the mission failure classes a new plan can change.
var retryableMissionFailures = map[string]bool{"check_failed": true, "patch_not_applicable": true}

const (
	retryFailureBytes = 3000
	retryPatchBytes   = 4000
)

// implementationRetryDue reports the retry the root's latest mission failure calls for, if any.
func (o *Orchestrator) implementationRetryDue(ctx context.Context, root TaskRecord) (implementationAttempt, bool, error) {
	detail, err := o.tasks.GetTask(ctx, root.ID)
	if err != nil {
		return implementationAttempt{}, false, err
	}
	missionID, err := missionTaskID(detail)
	if err != nil {
		return implementationAttempt{}, false, nil
	}
	mission, err := o.tasks.GetTask(ctx, missionID)
	if err != nil {
		return implementationAttempt{}, false, err
	}
	if !missionFailed(mission.Status) || !retryableMissionFailures[mission.ReasonCode] {
		return implementationAttempt{}, false, nil
	}
	ordinal := len(missionReferences(detail))
	if ordinal > MaxImplementationRetries {
		return implementationAttempt{}, false, nil
	}
	return implementationAttempt{
		Ordinal:           ordinal,
		SupersedesMission: mission.ID,
		RetryContext:      implementationRetryContext(mission),
	}, true, nil
}

// implementationRetryContext is what the planner is told about the mission its new plan replaces.
func implementationRetryContext(mission TaskRecord) string {
	failure := boundedUTF8(mission.Reason, retryFailureBytes)
	patch := boundedUTF8(closureAppliedPatch(mission.Instructions), retryPatchBytes)
	return fmt.Sprintf("\n\nPREVIOUS IMPLEMENTATION FAILED (mission task %d, %s). Keep the frozen design and its files; "+
		"write a new plan whose patches fix this failure.\nFAILURE:\n%s\n\nPATCH THAT FAILED:\n%s",
		mission.ID, mission.ReasonCode, failure, patch)
}

func implementationPlanTitle(attempt implementationAttempt) string {
	if attempt.Ordinal == 0 {
		return "Implementation plan for frozen design"
	}
	return fmt.Sprintf("Implementation plan retry %d for frozen design", attempt.Ordinal)
}

func mergeMetadata(first, second map[string]any) map[string]any {
	out := make(map[string]any, len(first)+len(second))
	for key, value := range second {
		out[key] = value
	}
	for key, value := range first {
		out[key] = value
	}
	return out
}

func boundedUTF8(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && (text[cut]&0xC0) == 0x80 {
		cut--
	}
	return text[:cut] + fmt.Sprintf(" [cut: first %d of %d bytes]", cut, len(text))
}
