package executive

import (
	"context"
	"fmt"
)

// Incorporation says how far a governed campaign's change has come on its way into the program.
//
// External audit A6 (2026-09-27): roots 1554, 1575 and 1665 are "completed" with real test evidence
// while their missions still await verification and their promotions await gates. The root's state
// did not distinguish a verified candidate from an accepted one or from a change actually applied. A
// completed root with a candidate the owner has not reviewed is a verified candidate pending
// incorporation, and the run now says so; nothing here reviews, accepts or applies anything.
type IncorporationState string

const (
	// IncorporationCandidateVerified: the code runner's execution is verified; no promotion is open yet.
	IncorporationCandidateVerified IncorporationState = "candidate_verified"
	// IncorporationPendingOwnerReview: a promotion is open and awaits the owner's review.
	IncorporationPendingOwnerReview IncorporationState = "pending_owner_review"
	// IncorporationAccepted: the owner approved the promotion; it is not applied yet.
	IncorporationAccepted IncorporationState = "accepted"
	// IncorporationApplied: the promotion was applied to its target ref.
	IncorporationApplied IncorporationState = "applied"
	// IncorporationRejected and IncorporationConflicted: the candidate will not land as it is.
	IncorporationRejected   IncorporationState = "rejected"
	IncorporationConflicted IncorporationState = "conflicted"
)

// MissionIncorporationReader reports the promotion state of a mission's candidate: the promotion
// status, or "" when the mission has none.
type MissionIncorporationReader interface {
	MissionPromotionStatus(ctx context.Context, missionTaskID int64) (string, error)
}

// WithMissionIncorporationReader lets the run report how far its change has come.
func WithMissionIncorporationReader(reader MissionIncorporationReader) OrchestratorOption {
	return func(o *Orchestrator) { o.incorporation = reader }
}

// incorporationFromPromotion maps a promotion status to the run's incorporation state.
func incorporationFromPromotion(status string) (IncorporationState, error) {
	switch status {
	case "":
		return IncorporationCandidateVerified, nil
	case "requested", "awaiting_gates":
		return IncorporationPendingOwnerReview, nil
	case "approved":
		return IncorporationAccepted, nil
	case "applied":
		return IncorporationApplied, nil
	case "rejected":
		return IncorporationRejected, nil
	case "conflicted":
		return IncorporationConflicted, nil
	}
	return "", fmt.Errorf("%w: unknown promotion status %q", ErrContractRejected, status)
}

// runIncorporation is the incorporation state of a root that requires a code-runner execution whose
// mission exists; "" for any other root, and whenever no reader is wired.
func (o *Orchestrator) runIncorporation(ctx context.Context, root TaskRecord) (IncorporationState, error) {
	if o.incorporation == nil {
		return "", nil
	}
	if _, required := requiredRootRequirement(root, CodeRunnerExecutionEvidenceRequirementKey); !required {
		return "", nil
	}
	missionID, err := missionTaskID(root)
	if err != nil || missionID <= 0 {
		return "", nil
	}
	status, err := o.incorporation.MissionPromotionStatus(ctx, missionID)
	if err != nil {
		return "", err
	}
	return incorporationFromPromotion(status)
}
