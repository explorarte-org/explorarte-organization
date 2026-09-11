package executive

// Blocked-status reason codes an executive root can carry. Only the two
// named here are ones ResumeDurable is willing to reconsider on its own --
// see ResumeDurable's blocked-status switch in recovery.go, which is the
// authority this list is derived from, not the other way around. Every
// other reason (ReasonOwnerDecisionRequired, indeterminate_tool_execution,
// orphaned_model_result, completion_verification_inconclusive,
// organization_revision_drift, and any evidence/context/design/department
// reason not listed here) requires a human, new evidence, or an explicit
// reconciliation command, and must stay excluded from autonomous
// rediscovery.
const (
	ReasonDispatchAssignmentRequired = "dispatch_assignment_required"
	ReasonModelOutcomeAmbiguous      = "model_outcome_ambiguous"

	// ReasonOwnerDecisionRequired marks a root blocked because a completed,
	// structurally valid CEO plan named at least one owner_decisions_required
	// entry. This is a lifecycle decision, not a contract defect: the model
	// answered correctly and asked the owner a question the plan itself
	// cannot resolve. It must never become autonomously reconsiderable --
	// see IsAutonomouslyReconsiderableBlockedReason below -- because
	// nothing about polling again changes whether the owner has decided.
	ReasonOwnerDecisionRequired = "owner_decision_required"
)

// IsAutonomouslyReconsiderableBlockedReason reports whether a blocked
// executive root carrying this status_reason_code may safely be
// re-presented to ResumeDurable by the persistent worker's next poll,
// instead of staying permanently excluded from discovery once it first
// blocks.
//
// This does NOT mean the root will unblock. ResumeDurable still runs its
// own guards on every call -- local-lease adoption, orphaned-succeeded-
// invocation detection, and (for model_outcome_ambiguous) whether every
// ambiguity now has a host-policy resolution -- and can leave the root
// blocked, or re-block it, exactly as it already does today. This predicate
// only answers the narrower, earlier question of whether even ATTEMPTING
// that reconsideration is safe: a reason that needs a human decision, fresh
// evidence, or an explicit reconciliation command is never safe to retry
// automatically, no matter how many times the worker polls.
//
// The set here is deliberately the same one ResumeDurable's own
// blocked-status switch already special-cases (see recovery.go) --
// dispatch_assignment_required and model_outcome_ambiguous are the only two
// case labels that fall through to actual recovery logic there; every other
// case returns an error immediately. Keeping both switch labels expressed as
// the same two constants this function tests against means there is one
// place that defines "recoverable", not two lists that can drift apart.
func IsAutonomouslyReconsiderableBlockedReason(reason string) bool {
	switch reason {
	case ReasonDispatchAssignmentRequired, ReasonModelOutcomeAmbiguous:
		return true
	default:
		return false
	}
}
