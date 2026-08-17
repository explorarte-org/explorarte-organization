package executive

import (
	"encoding/json"
	"testing"
)

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// TestParseDepartmentPlan_TaskClassHostValidatedAndDefaulted is M1.3
// section 5/18.A: a Leader may PROPOSE task_class, but the host validates
// it -- an empty proposal (including recovered pre-M1.3 durable output,
// which never had this field) defaults to TaskClassGeneralWork, never
// TaskClassOf(role); an invalid syntax is rejected outright; a valid
// proposal passes through unchanged.
func TestParseDepartmentPlan_TaskClassHostValidatedAndDefaulted(t *testing.T) {
	limits := DefaultLimits()

	t.Run("missing task_class defaults to general.work", func(t *testing.T) {
		plan := DepartmentPlan{
			SchemaVersion: DepartmentPlanSchemaVersion, DepartmentID: "ingenieria_ia",
			Tasks: []WorkerTaskProposal{{ClientKey: "a", AssignedRoleID: "ingenieria_ia/backend_engineer", Title: "x", Instructions: "x", AcceptanceCriteria: []string{"done"}}},
		}
		out, err := ParseDepartmentPlan(mustJSON(t, plan), limits)
		if err != nil {
			t.Fatal(err)
		}
		if out.Tasks[0].TaskClass != TaskClassGeneralWork {
			t.Fatalf("TaskClass = %q, want %q", out.Tasks[0].TaskClass, TaskClassGeneralWork)
		}
	})

	t.Run("valid proposed task_class passes through", func(t *testing.T) {
		plan := DepartmentPlan{
			SchemaVersion: DepartmentPlanSchemaVersion, DepartmentID: "ingenieria_ia",
			Tasks: []WorkerTaskProposal{{ClientKey: "a", AssignedRoleID: "ingenieria_ia/backend_engineer", TaskClass: "engineering.bugfix", Title: "x", Instructions: "x", AcceptanceCriteria: []string{"done"}}},
		}
		out, err := ParseDepartmentPlan(mustJSON(t, plan), limits)
		if err != nil {
			t.Fatal(err)
		}
		if out.Tasks[0].TaskClass != "engineering.bugfix" {
			t.Fatalf("TaskClass = %q, want %q", out.Tasks[0].TaskClass, "engineering.bugfix")
		}
	})

	t.Run("invalid proposed task_class is rejected", func(t *testing.T) {
		plan := DepartmentPlan{
			SchemaVersion: DepartmentPlanSchemaVersion, DepartmentID: "ingenieria_ia",
			Tasks: []WorkerTaskProposal{{ClientKey: "a", AssignedRoleID: "ingenieria_ia/backend_engineer", TaskClass: "Not A Valid Class!", Title: "x", Instructions: "x", AcceptanceCriteria: []string{"done"}}},
		}
		if _, err := ParseDepartmentPlan(mustJSON(t, plan), limits); err == nil {
			t.Fatal("expected an invalid task_class to be rejected")
		}
	})

	t.Run("followup proposals get the same host validation", func(t *testing.T) {
		review := DepartmentReview{
			SchemaVersion: DepartmentReviewSchemaVersion, Verdict: ReviewNeedsReplan,
			ProposedFollowupTasks: []WorkerTaskProposal{{ClientKey: "a", AssignedRoleID: "ingenieria_ia/backend_engineer", Title: "x", Instructions: "x", AcceptanceCriteria: []string{"done"}}},
		}
		out, err := ParseDepartmentReview(mustJSON(t, review), limits)
		if err != nil {
			t.Fatal(err)
		}
		if out.ProposedFollowupTasks[0].TaskClass != TaskClassGeneralWork {
			t.Fatalf("followup TaskClass = %q, want %q", out.ProposedFollowupTasks[0].TaskClass, TaskClassGeneralWork)
		}
	})
}

func TestValidTaskClass(t *testing.T) {
	for _, s := range []string{"general.work", "owner.goal", "coordination.ceo_plan", "research.corpus_curate"} {
		if !ValidTaskClass(s) {
			t.Errorf("ValidTaskClass(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "NotDotted", "has space.here", "notdotted"} {
		if ValidTaskClass(s) {
			t.Errorf("ValidTaskClass(%q) = true, want false", s)
		}
	}
}
