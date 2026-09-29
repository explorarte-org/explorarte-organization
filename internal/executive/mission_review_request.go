package executive

import (
	"context"
	"fmt"
)

// A verified mission asks for its engineering review.
//
// A code-runner mission is created with three requirements: the sealed candidate (satisfied by the
// code-runner), the engineering gates, and an independent review. The gates requirement is satisfied
// only by RequestPromotion, which records the gate check against the attempt's own evidence and opens
// the promotion that the review then decides. Nothing in the governed path called it: an external
// watcher did, and that watcher also approved every promotion itself and pushed to main, so it was
// stopped (2026-09-27). Smokes #20, #21 and #22 then reached the CEO closure with the gates still
// pending, and the closure refused to call the campaign complete over a mission that had not even
// asked for review.
//
// The executive now asks, once, as soon as it has verified the code-runner's evidence: the gates are
// checked against that same evidence and a promotion awaits the review. It never reviews, approves or
// applies; those stay the owner's (orgctl code-runner mission review, orgctl staging promotion apply).

//
// The request is two durable writes -- the gate check, then the promotion -- and a failure between them
// used to be permanent: the gates were no longer pending, so the next pass asked for nothing and the
// execution barrier passed with no promotion to review (external audit A2, 2026-09-27, reproduced with
// fault injection). So the executive asks on every pass while the mission awaits verification, and the
// requester is idempotent: an existing promotion for the workspace is the answer; recorded gates with
// no promotion get only the promotion; pending gates get both.

// MissionReviewRequester opens a mission's promotion for review once its execution is verified. It must
// be idempotent per workspace: asked again, it opens nothing new when a promotion already exists.
// gatesRecorded tells it the gate check is already durable and must not be recorded again.
type MissionReviewRequester interface {
	RequestMissionReview(ctx context.Context, missionTaskID, workspaceID int64, gatesRecorded bool) error
}

// WithMissionReviewRequester lets the executive ask for a verified mission's review.
func WithMissionReviewRequester(requester MissionReviewRequester) OrchestratorOption {
	return func(o *Orchestrator) { o.missionReviews = requester }
}

const missionGatesRequirementKey = "engineering-required-gates"

// requestMissionReview asks for the review of a mission whose execution evidence was verified, on every
// pass while it awaits verification; the requester makes the repeat harmless (see above). A failure
// leaves the execution pending, to be asked again on the next pass; it never blocks the root.
func (o *Orchestrator) requestMissionReview(ctx context.Context, mission TaskRecord, attemptEvidence EvidenceRecord) error {
	if o.missionReviews == nil || mission.Status != "awaiting_verification" {
		return nil
	}
	gates, found := findRequirementByKey(mission.Requirements, missionGatesRequirementKey)
	if !found || (gates.Status != "pending" && gates.Status != "satisfied") {
		return nil
	}
	candidate, _ := attemptEvidence.Metadata["candidate_revision"].(map[string]any)
	workspaceID, ok := metadataInt64(candidate["workspace_id"])
	if !ok || workspaceID <= 0 {
		return fmt.Errorf("%w: mission task %d evidence names no sealed workspace", ErrCodeRunnerExecutionInvalid, mission.ID)
	}
	if err := o.missionReviews.RequestMissionReview(ctx, mission.ID, workspaceID, gates.Status == "satisfied"); err != nil {
		return fmt.Errorf("%w: request the engineering review of mission %d (workspace %d): %v", ErrCodeRunnerExecutionPending, mission.ID, workspaceID, err)
	}
	return nil
}
