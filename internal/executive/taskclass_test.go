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

// TestDepartmentReviewOutputSchema_TaskClassRequiredForFreshOutput is the
// P3 evidence gap independent review round 2 flagged: proves the FRESH
// provider output schema (departmentReviewOutputSchema, what the provider
// is dispatch-time constrained against) requires task_class on every
// proposed_followup_tasks item, exactly like departmentPlanOutputSchema's
// tasks already did -- not merely that the Go parser happens to default a
// missing one after the fact.
func TestDepartmentReviewOutputSchema_TaskClassRequiredForFreshOutput(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(departmentReviewOutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	defs, ok := schema["$defs"].(map[string]any)
	if !ok {
		t.Fatal("departmentReviewOutputSchema has no $defs")
	}
	task, ok := defs["task"].(map[string]any)
	if !ok {
		t.Fatal("departmentReviewOutputSchema's $defs has no task definition")
	}
	required, ok := task["required"].([]any)
	if !ok {
		t.Fatal("departmentReviewOutputSchema's task definition has no required list")
	}
	var hasTaskClass bool
	for _, field := range required {
		if field == "task_class" {
			hasTaskClass = true
		}
	}
	if !hasTaskClass {
		t.Fatal("departmentReviewOutputSchema's task definition does not require task_class -- a fresh provider response could omit it")
	}
	properties, ok := task["properties"].(map[string]any)
	if !ok || properties["task_class"] == nil {
		t.Fatal("departmentReviewOutputSchema's task definition does not declare a task_class property")
	}
	proposedFollowupTasks, ok := schema["properties"].(map[string]any)["proposed_followup_tasks"].(map[string]any)
	if !ok {
		t.Fatal("departmentReviewOutputSchema has no proposed_followup_tasks property")
	}
	items, ok := proposedFollowupTasks["items"].(map[string]any)
	if !ok || items["$ref"] != "#/$defs/task" {
		t.Fatalf("proposed_followup_tasks items must $ref the same strict task schema departmentPlanOutputSchema uses, got %+v", items)
	}
}

// TestParseDepartmentReview_HistoricalRecoveryStillDefaultsTaskClass is
// HISTORICAL_REVIEW_RECOVERY_PROOF: durable output recovered from BEFORE
// task_class existed (never validated against the current strict schema
// at dispatch time -- it predates it) must still parse successfully,
// defaulting the missing field to TaskClassGeneralWork, never rejected
// and never re-derived from role.
func TestParseDepartmentReview_HistoricalRecoveryStillDefaultsTaskClass(t *testing.T) {
	historicalJSON := []byte(`{
		"schema_version":"department-review/v1","verdict":"needs_replan",
		"findings":[],"unsatisfied_criteria":[],"evidence_refs":[],
		"proposed_followup_tasks":[{
			"client_key":"a","assigned_role_id":"ingenieria_ia/backend_engineer",
			"title":"x","instructions":"x","acceptance_criteria":["done"],
			"dependencies":[],"requirements":[],"priority":1
		}]
	}`)
	out, err := ParseDepartmentReview(historicalJSON, DefaultLimits())
	if err != nil {
		t.Fatalf("pre-M1.3 durable output (no task_class field at all) must remain recoverable: %v", err)
	}
	if out.ProposedFollowupTasks[0].TaskClass != TaskClassGeneralWork {
		t.Fatalf("recovered TaskClass = %q, want %q", out.ProposedFollowupTasks[0].TaskClass, TaskClassGeneralWork)
	}
}

func TestValidTaskClass(t *testing.T) {
	for _, s := range []string{"general.work", "owner.goal", "coordination.ceo_plan", "research.corpus_curate"} {
		if !ValidTaskClass(s) {
			t.Errorf("ValidTaskClass(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "NotDotted", "has space.here", "notdotted", TaskClassLegacyUnspecified} {
		if ValidTaskClass(s) {
			t.Errorf("ValidTaskClass(%q) = true, want false", s)
		}
	}
}
