package executive

import (
	"strings"
	"testing"
)

// DEPARTMENT-REVIEW-DELEGATION-SCOPE-006.
//
// AUTONOMY-SMOKE-017-R17-V6 died one stage later than v5, at task 15763
// (coordination.department_review, ingenieria_ia/orquestador): a review
// proposed a follow-up task delegating to investigacion/revisor_adversarial
// -- a role in a different department. ValidateFollowups (a thin wrapper
// over ValidateDepartmentPlan, validator.go:242) correctly rejected it: the
// same-department rule at validator.go:144 has no carve-out for the
// adversarial reviewer, and none is needed -- AdversarialReviewerRoleID
// (design_freeze_phase.go) is dispatched only by the host's own
// driveDesignFreeze, gated on the root carrying the design-freeze
// requirement. Naming the role there is an authority statement, not a
// routing one: no legitimate path lets a review reach it through
// proposed_followup_tasks, in any campaign shape.
//
// The guard is correct and is not touched here. The gap is upstream: nothing
// ever told the reviewer its follow-up proposals are department-scoped, or
// that adversarial review and design-freeze happen automatically. Retry
// feedback named the exact rejected pair (ingenieria_ia -> the target role)
// but never explained the rule -- the same category-B feedback shape as
// every prior gap in this engagement.

// GUARD A -- THE GAP ITSELF. Before the fix, PurposeDepartmentReview's
// ExecutionContract said nothing distinguishing which departments a
// reviewer's proposed_followup_tasks may target, and nothing about
// adversarial review/design-freeze being host-orchestrated rather than
// review-requested. This is the RED regression: it fails against the
// pre-fix contract and must pass after.
func TestDepartmentReviewContractStatesDelegationScopeAndAdversarialRouting(t *testing.T) {
	contract := executionContractFor(PurposeDepartmentReview, nil)
	for _, want := range []string{
		"within your own reviewing department",
		"the host orchestrates",
		"proposed_followup_tasks",
	} {
		if !strings.Contains(contract, want) {
			t.Fatalf("department review contract must state delegation scope and adversarial-review routing (missing %q):\n%s", want, contract)
		}
	}
}

// GUARD B -- no regression: the new guidance must not leak into purposes
// that never produce a DepartmentReview, mirroring PR#129/PR#131/PR#132's
// own boundary for their respective guidance.
func TestNonReviewPurposesDoNotReceiveTheDelegationScopeGuidance(t *testing.T) {
	for _, purpose := range []ExecutionPurpose{PurposeDepartmentPlan, PurposeDepartmentWorker, PurposeDesignAdjudication} {
		contract := executionContractFor(purpose, nil)
		if strings.Contains(contract, "within your own reviewing department") {
			t.Fatalf("%s must not receive the department-review delegation-scope guidance:\n%s", purpose, contract)
		}
	}
}

// GUARD C -- no regression: PurposeDepartmentReview's existing guidance
// (task class rules, department consistency, non-repository evidence)
// remains present once the new guidance is added.
func TestDepartmentReviewContractStillCarriesItsExistingGuidance(t *testing.T) {
	contract := executionContractFor(PurposeDepartmentReview, nil)
	for _, want := range []string{
		"task_class MUST",                  // taskClassGuidance
		"Consistency rule for this review", // departmentConsistencyGuidance
		"executive/task bookkeeping",       // nonRepositoryReviewEvidenceGuidance
	} {
		if !strings.Contains(contract, want) {
			t.Fatalf("department review contract lost existing guidance (missing %q) after adding delegation-scope guidance:\n%s", want, contract)
		}
	}
}
