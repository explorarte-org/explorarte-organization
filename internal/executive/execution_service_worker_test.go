package executive

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// workerPlan builds a department plan with a single worker task assigned to
// the given role.
func workerPlan(roleID string) DepartmentPlan {
	return DepartmentPlan{
		SchemaVersion: DepartmentPlanSchemaVersion,
		DepartmentID:  "ingenieria_ia",
		Tasks: []WorkerTaskProposal{{
			ClientKey: "k1", AssignedRoleID: roleID, TaskClass: TaskClassGeneralWork,
			Title: "modify the evidence document", Instructions: "write the file",
			AcceptanceCriteria: []string{"one file changes"},
			Dependencies:       []string{}, Requirements: []RequirementProposal{}, Priority: 1,
		}},
		ReviewCriteria: []string{"done"}, Unresolved: []string{},
	}
}

// executionServiceValidator mirrors the real catalog closely enough to matter:
// a leader, an ordinary specialist, an assurance role, and the deterministic
// executor that caused this rule to exist.
func executionServiceValidator(t *testing.T) *Validator {
	t.Helper()
	leader := RoleRef{ID: "ingenieria_ia/orquestador", UnitID: "ingenieria_ia", Enabled: true, Executable: true, CanonicalLeader: true, AuthorityClass: "department_leadership"}
	specialist := RoleRef{ID: "ingenieria_ia/ingeniero_ia", UnitID: "ingenieria_ia", Enabled: true, Executable: true, AuthorityClass: "specialist"}
	assurance := RoleRef{ID: "ingenieria_ia/qa", UnitID: "ingenieria_ia", Enabled: true, Executable: true, AuthorityClass: "assurance"}
	// Enabled and executable, exactly as the canonical catalog has it. That is
	// the whole point: operational participation is not model executability.
	codeRunner := RoleRef{ID: "ingenieria_ia/code-runner", UnitID: "ingenieria_ia", Enabled: true, Executable: true, AuthorityClass: "execution_service"}

	registry := fakeRegistry{
		rev:     RevisionRef{ID: 7},
		units:   map[string]UnitRef{"ingenieria_ia": {ID: "ingenieria_ia", Operational: true, LeaderRoleID: leader.ID}},
		roles:   map[string]RoleRef{leader.ID: leader, specialist.ID: specialist, assurance.ID: assurance, codeRunner.ID: codeRunner},
		leaders: map[string]RoleRef{"ingenieria_ia": leader},
	}
	value, err := NewValidator(registry, allowAuthz{}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// 1 and 2: an execution service of the same department is refused, and the
// specific role that exposed the gap is refused by name.
func TestExecutionServiceCannotBeADepartmentWorker(t *testing.T) {
	validator := executionServiceValidator(t)
	err := validator.ValidateDepartmentPlan(context.Background(), 7, "ingenieria_ia",
		"ingenieria_ia/orquestador", workerPlan("ingenieria_ia/code-runner"))
	if !errors.Is(err, ErrRoleNotAssignable) {
		t.Fatalf("err=%v, want ErrRoleNotAssignable", err)
	}
	if !strings.Contains(err.Error(), "ingenieria_ia/code-runner") {
		t.Fatalf("the refusal does not name the role: %v", err)
	}
	if !strings.Contains(err.Error(), "execution service") {
		t.Fatalf("the refusal does not name the reason: %v", err)
	}
}

// 3 and 4: cognitive workers are untouched. A specialist and an assurance role
// that were assignable before stay assignable.
func TestCognitiveWorkersRemainAssignable(t *testing.T) {
	validator := executionServiceValidator(t)
	for _, roleID := range []string{"ingenieria_ia/ingeniero_ia", "ingenieria_ia/qa"} {
		t.Run(roleID, func(t *testing.T) {
			if err := validator.ValidateDepartmentPlan(context.Background(), 7, "ingenieria_ia",
				"ingenieria_ia/orquestador", workerPlan(roleID)); err != nil {
				t.Fatalf("%s was rejected: %v", roleID, err)
			}
		})
	}
}

// 6: authorization cannot rescue the topology. Even with task.assign_worker
// granted -- allowAuthz allows everything -- the refusal stands, because it is
// evaluated before authorization is consulted.
func TestAuthorizationCannotMakeAnExecutionServiceAWorker(t *testing.T) {
	validator := executionServiceValidator(t)
	err := validator.ValidateDepartmentPlan(context.Background(), 7, "ingenieria_ia",
		"ingenieria_ia/orquestador", workerPlan("ingenieria_ia/code-runner"))
	if !errors.Is(err, ErrRoleNotAssignable) {
		t.Fatalf("a permissive authorizer changed the outcome: %v", err)
	}
}

// 5: follow-up tasks proposed by a department review travel the same
// validation path, so they inherit the rule.
func TestReviewFollowupsCannotTargetAnExecutionService(t *testing.T) {
	validator := executionServiceValidator(t)
	plan := workerPlan("ingenieria_ia/code-runner")
	// A review's proposed_followup_tasks are validated as department plan
	// tasks; assert through the same entry point they reach.
	if err := validator.ValidateDepartmentPlan(context.Background(), 7, "ingenieria_ia",
		"ingenieria_ia/orquestador", plan); !errors.Is(err, ErrRoleNotAssignable) {
		t.Fatalf("followup path accepted an execution service: %v", err)
	}
	// And the same proposal with a cognitive role is still accepted, so the
	// rule is not rejecting followups wholesale.
	if err := validator.ValidateDepartmentPlan(context.Background(), 7, "ingenieria_ia",
		"ingenieria_ia/orquestador", workerPlan("ingenieria_ia/qa")); err != nil {
		t.Fatalf("followup path rejected a cognitive worker: %v", err)
	}
}

// The guidance delivered to the model states the rule too, so a leader does
// not spend an inference producing a plan the host will refuse. The host check
// remains the boundary regardless of what the prompt says.
func TestTaskClassGuidanceStatesTheExecutionServiceRule(t *testing.T) {
	if !strings.Contains(taskClassGuidance, "execution_service") {
		t.Fatal("the delivered guidance does not mention the execution_service rule")
	}
	if !strings.Contains(taskClassGuidance, "Do not assign worker tasks") {
		t.Fatal("the guidance does not state the prohibition")
	}
}
