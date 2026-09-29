package executive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
)

// The owner may accept a department's round when the host has declined it another replan.
//
// A run whose department review asks for more rework than MaxDepartmentReplans allows is blocked as
// department_replans_exhausted, and Resume refuses to reopen it: re-driving the department would
// arrive at the same refusal at full price. That leaves the owner no way to say "the deliverable is
// good enough; go on", short of editing the review's recorded verdict -- which would forge a model
// result the design record is bound to.
//
// Local smoke #34 (root 1773, 2026-09-27): the round-3 review refused a design it could see only the
// first 1200 bytes of (the evidence bundle cut the worker summary; fixed separately) and the round
// had no replan left. The owner asked to accept that design and go on to the implementation.
//
// So acceptance is its own durable fact, recorded against the root by the owner and naming the one
// review it overrides. The review keeps its verdict. The run treats that review as accepted and
// continues exactly as after an accept: the candidate is the round's frontier, and the adversarial
// review and the CEO's adjudication still judge it in full.

// ErrOwnerAcceptanceRefused is returned when the owner's acceptance does not apply to the run.
var ErrOwnerAcceptanceRefused = errors.New("owner acceptance refused")

func ownerDepartmentAcceptanceReference(unit string, reviewTaskID int64) string {
	return "owner-accepts-department:" + unit + ":review:" + strconv.FormatInt(reviewTaskID, 10)
}

// ownerAcceptedDepartmentReview reports whether the owner accepted exactly this review's department round.
func (o *Orchestrator) ownerAcceptedDepartmentReview(ctx context.Context, rootID int64, unit string, reviewTaskID int64) (bool, error) {
	root, err := o.tasks.GetTask(ctx, rootID)
	if err != nil {
		return false, err
	}
	want := ownerDepartmentAcceptanceReference(unit, reviewTaskID)
	for _, evidence := range root.Evidence {
		if evidence.Reference == want {
			return true, nil
		}
	}
	return false, nil
}

// AcceptDepartmentByOwner records the owner's acceptance of unit's latest review in a run blocked as
// department_replans_exhausted, and reopens the run. It refuses anything else: another block reason,
// an actor that is not the owner, or a review that is not a completed needs_replan the host declined.
func (o *Orchestrator) AcceptDepartmentByOwner(ctx context.Context, rootID int64, unit, actorRoleID string) (Run, error) {
	if actorRoleID != OwnerRoleID {
		return Run{}, fmt.Errorf("%w: only %s may accept a department round, not %q", ErrOwnerAcceptanceRefused, OwnerRoleID, actorRoleID)
	}
	root, err := o.tasks.GetTask(ctx, rootID)
	if err != nil {
		return Run{}, err
	}
	if root.AssignedRoleID != CEORoleID || root.CorrelationID == "" {
		return Run{}, ErrInvalidInput
	}
	if root.Status != "blocked" || root.ReasonCode != ReasonDepartmentReplansExhausted {
		return Run{}, fmt.Errorf("%w: root %d is %s (%s), not blocked as %s", ErrOwnerAcceptanceRefused, rootID, root.Status, root.ReasonCode, ReasonDepartmentReplansExhausted)
	}
	all, err := o.tasks.ListByCorrelation(ctx, root.CorrelationID)
	if err != nil {
		return Run{}, err
	}
	round := o.activeDesignRound(ctx, all, root.ID)
	review, found := latestReviewTask(roundTasks(all, round), root.ID, unit+designRoundSuffix(round))
	if !found || review.Status != "completed" {
		return Run{}, fmt.Errorf("%w: department %s has no completed review in design round %d", ErrOwnerAcceptanceRefused, unit, round)
	}
	result, ok := o.resultForCompletedTask(ctx, review)
	if !ok {
		return Run{}, fmt.Errorf("%w: review %d has no result", ErrOwnerAcceptanceRefused, review.ID)
	}
	parsed, err := ParseDepartmentReview(result.JSONOutput, o.limits)
	if err != nil {
		return Run{}, err
	}
	if parsed.Verdict != ReviewNeedsReplan || replanCapacityRemains(review.IdempotencyKey, o.limits.MaxDepartmentReplans) {
		return Run{}, fmt.Errorf("%w: review %d is %s and not a replan the host declined", ErrOwnerAcceptanceRefused, review.ID, parsed.Verdict)
	}
	reference := ownerDepartmentAcceptanceReference(unit, review.ID)
	digest := sha256.Sum256([]byte(reference + "\x00" + result.ResponseHash))
	if err = o.tasks.RecordEvidence(ctx, EvidenceCommand{
		TaskID: root.ID, Type: "approval", Reference: reference,
		Digest: hex.EncodeToString(digest[:]), RecordedBy: actorRoleID,
		Metadata: map[string]any{
			"decision":             "accept_department_round",
			"department":           unit,
			"design_round":         round,
			"review_task_id":       review.ID,
			"review_verdict":       string(parsed.Verdict),
			"review_response_hash": result.ResponseHash,
		},
	}); err != nil {
		return Run{}, err
	}
	if _, err = o.tasks.UnblockTask(ctx, root.ID, "human", actorRoleID); err != nil {
		return Run{}, err
	}
	return o.Status(ctx, root.ID)
}

// roundTasks keeps the tasks of one design round, so a round-1 key prefix does not also match the
// reviews of later rounds.
func roundTasks(all []TaskRecord, round int) []TaskRecord {
	out := make([]TaskRecord, 0, len(all))
	for _, task := range all {
		if designRoundOf(task.IdempotencyKey) == round {
			out = append(out, task)
		}
	}
	return out
}
