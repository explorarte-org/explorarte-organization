package executive

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// requirementPlan builds a department plan whose single worker task carries
// one requirement of the given type and blocking-ness.
func requirementPlan(reqType string, required bool) DepartmentPlan {
	plan := workerPlan("ingenieria_ia/arquitecto_software")
	plan.Tasks[0].Requirements = []RequirementProposal{{
		Key:         "the_requirement",
		Type:        reqType,
		Description: "an obligation attached by the department leader",
		Required:    required,
	}}
	return plan
}

func satisfiabilityValidator(t *testing.T) *Validator {
	t.Helper()
	leader := RoleRef{ID: "ingenieria_ia/orquestador", UnitID: "ingenieria_ia", Enabled: true, Executable: true, CanonicalLeader: true, AuthorityClass: "department_leadership"}
	architect := RoleRef{ID: "ingenieria_ia/arquitecto_software", UnitID: "ingenieria_ia", Enabled: true, Executable: true, AuthorityClass: "specialist"}
	registry := fakeRegistry{
		rev:     RevisionRef{ID: 7},
		units:   map[string]UnitRef{"ingenieria_ia": {ID: "ingenieria_ia", Operational: true, LeaderRoleID: leader.ID}},
		roles:   map[string]RoleRef{leader.ID: leader, architect.ID: architect},
		leaders: map[string]RoleRef{"ingenieria_ia": leader},
	}
	value, err := NewValidator(registry, allowAuthz{}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func validateRequirement(t *testing.T, reqType string, required bool) error {
	t.Helper()
	return satisfiabilityValidator(t).ValidateDepartmentPlan(context.Background(), 7,
		"ingenieria_ia", "ingenieria_ia/orquestador", requirementPlan(reqType, required))
}

// A: a required result is the one obligation a cognitive worker can actually
// discharge -- its outcome IS a validated durable model result.
func TestRequiredResultIsAllowedOnACognitiveWorker(t *testing.T) {
	if err := validateRequirement(t, "result", true); err != nil {
		t.Fatalf("required result was rejected: %v", err)
	}
}

// B, C, D and approval: every other type is a blocking obligation with no
// satisfier in the worker execution path. Rejecting artifact alone would have
// let the next run die on condition instead, which is what task 87 actually
// carried alongside it.
func TestRequiredNonResultRequirementsAreRefused(t *testing.T) {
	for _, reqType := range []string{"artifact", "check", "approval", "condition"} {
		t.Run(reqType, func(t *testing.T) {
			err := validateRequirement(t, reqType, true)
			if !errors.Is(err, ErrRequirementUnsatisfiable) {
				t.Fatalf("err=%v, want ErrRequirementUnsatisfiable", err)
			}
			if !strings.Contains(err.Error(), reqType) || !strings.Contains(err.Error(), "the_requirement") {
				t.Fatalf("the refusal names neither the type nor the key: %v", err)
			}
		})
	}
}

// Optional requirements of any type stay legal. They do not block completion
// and can carry real descriptive intent; the rule is about blocking
// obligations, not about which types are respectable.
func TestOptionalRequirementsOfAnyTypeRemainAllowed(t *testing.T) {
	for _, reqType := range []string{"artifact", "check", "approval", "condition", "result"} {
		t.Run(reqType, func(t *testing.T) {
			if err := validateRequirement(t, reqType, false); err != nil {
				t.Fatalf("optional %s was rejected: %v", reqType, err)
			}
		})
	}
}

// The exact shape that failed in production: a required artifact and a
// required condition on the same task, alongside a satisfiable result.
func TestTheProductionFailureShapeIsRefusedUpFront(t *testing.T) {
	plan := workerPlan("ingenieria_ia/arquitecto_software")
	plan.Tasks[0].Requirements = []RequirementProposal{
		{Key: "updated_doc_artifact", Type: "artifact", Description: "the document exists and is updated", Required: true},
		{Key: "scope_limited_condition", Type: "condition", Description: "only the authorized file changed", Required: true},
		{Key: "model_result", Type: "result", Description: "validated durable model invocation result", Required: true},
	}
	err := satisfiabilityValidator(t).ValidateDepartmentPlan(context.Background(), 7,
		"ingenieria_ia", "ingenieria_ia/orquestador", plan)
	if !errors.Is(err, ErrRequirementUnsatisfiable) {
		t.Fatalf("the plan that stalled a real run was accepted: %v", err)
	}
}

// Follow-ups proposed by a department review travel the same validation path,
// so a review cannot smuggle in what a plan cannot.
func TestReviewFollowupsInheritTheSatisfiabilityRule(t *testing.T) {
	if err := validateRequirement(t, "artifact", true); !errors.Is(err, ErrRequirementUnsatisfiable) {
		t.Fatalf("followup path accepted an unsatisfiable requirement: %v", err)
	}
	if err := validateRequirement(t, "result", true); err != nil {
		t.Fatalf("followup path rejected a satisfiable requirement: %v", err)
	}
}

// The rule is a refusal, not a rewrite. Nothing downgrades a required
// requirement to optional, satisfies it automatically, or drops it: the plan
// is refused whole, before any durable work is created.
func TestUnsatisfiableRequirementsAreRefusedNotRewritten(t *testing.T) {
	plan := requirementPlan("artifact", true)
	if err := satisfiabilityValidator(t).ValidateDepartmentPlan(context.Background(), 7,
		"ingenieria_ia", "ingenieria_ia/orquestador", plan); err == nil {
		t.Fatal("expected a refusal")
	}
	// The caller's plan is untouched -- the validator did not quietly flip
	// Required or change the type to make it pass.
	if !plan.Tasks[0].Requirements[0].Required || plan.Tasks[0].Requirements[0].Type != "artifact" {
		t.Fatalf("the validator mutated the proposal: %+v", plan.Tasks[0].Requirements[0])
	}
}

func TestGuidanceStatesTheRequirementRule(t *testing.T) {
	if !strings.Contains(taskClassGuidance, "may require model results only") {
		t.Fatal("the delivered guidance does not state the requirement rule")
	}
}
