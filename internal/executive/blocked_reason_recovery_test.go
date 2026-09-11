package executive

import "testing"

// TestIsAutonomouslyReconsiderableBlockedReason is the mutation guard for the
// exact set ResumeDurable is willing to reconsider on its own. It must match
// ResumeDurable's blocked-status switch in recovery.go case for case: any
// drift between the two either silently reopens a reason that needs a human
// (dangerous) or silently stops rediscovering a reason ResumeDurable already
// knows how to recover (the AUTONOMOUS-E2E-002 regression this round fixes).
func TestIsAutonomouslyReconsiderableBlockedReason(t *testing.T) {
	cases := []struct {
		reason string
		want   bool
	}{
		// The two reasons ResumeDurable's own switch falls through to
		// recovery logic for.
		{ReasonDispatchAssignmentRequired, true},
		{ReasonModelOutcomeAmbiguous, true},

		// Every other reason ResumeDurable's switch returns an error for
		// immediately, and every other reason this codebase actually
		// produces via BlockTask -- none may be reconsidered autonomously.
		{"owner_decision_required", false},
		// ReasonOwnerDecisionRequired by its constant, not just its literal
		// above: a structurally valid CEO plan that names an owner decision
		// must stay excluded from autonomous rediscovery forever -- polling
		// again cannot answer a question only the owner can answer. Root 618
		// blocked on this exact reason.
		{ReasonOwnerDecisionRequired, false},
		{"indeterminate_tool_execution", false},
		{"orphaned_model_result", false},
		{"completion_verification_inconclusive", false},
		{"organization_revision_drift", false},
		{"design_revision_rejected", false},
		{"design_rounds_exhausted", false},
		{"department_review_blocked", false},
		{"department_replans_exhausted", false},
		{"run_child_failed", false},
		{"evidence_insufficient", false},
		{"context_source_missing", false},
		{"model_authority_violation", false},
		{"adversarial_review_unavailable", false},
		{"awaiting_child_coordination", false},

		// Unknown/empty/typo'd reasons must fail closed, not open.
		{"", false},
		{"dispatch_assignment_require", false},
		{"DISPATCH_ASSIGNMENT_REQUIRED", false},
		{"model_outcome_ambiguous ", false},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			if got := IsAutonomouslyReconsiderableBlockedReason(tc.reason); got != tc.want {
				t.Fatalf("IsAutonomouslyReconsiderableBlockedReason(%q) = %v, want %v", tc.reason, got, tc.want)
			}
		})
	}
}
