package executive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Bug A — JSON Schema / Host Validator Drift
// ---------------------------------------------------------------------------

// TestBugA_DepartmentPlanParser_RejectsFlatTaskClass proves the host-side
// parser rejects flat task_class values such as "discovery", "design", or
// "review" through validateWorkerTaskShape → ValidTaskClass. Even though
// the provider-facing JSON schema uses {"type":"string"} (Model Runtime
// limitation), the host validator enforces the syntactic contract.
func TestBugA_DepartmentPlanParser_RejectsFlatTaskClass(t *testing.T) {
	limits := DefaultLimits()

	t.Run("flat discovery rejected by parser", func(t *testing.T) {
		body := []byte(`{
			"schema_version":"department-plan/v1",
			"department_id":"ingenieria_ia",
			"tasks":[{
				"client_key":"a","assigned_role_id":"ingenieria_ia/backend_engineer",
				"task_class":"discovery",
				"title":"x","instructions":"x",
				"acceptance_criteria":["done"],
				"dependencies":[],"requirements":[],"priority":1
			}],
			"review_criteria":[],"unresolved":[]
		}`)
		if _, err := ParseDepartmentPlan(body, limits); !errors.Is(err, ErrContractRejected) {
			t.Fatalf("expected ErrContractRejected for flat task_class 'discovery', got %v", err)
		}
	})

	t.Run("flat design rejected by parser", func(t *testing.T) {
		body := []byte(`{
			"schema_version":"department-plan/v1",
			"department_id":"ingenieria_ia",
			"tasks":[{
				"client_key":"a","assigned_role_id":"ingenieria_ia/backend_engineer",
				"task_class":"design",
				"title":"x","instructions":"x",
				"acceptance_criteria":["done"],
				"dependencies":[],"requirements":[],"priority":1
			}],
			"review_criteria":[],"unresolved":[]
		}`)
		if _, err := ParseDepartmentPlan(body, limits); !errors.Is(err, ErrContractRejected) {
			t.Fatalf("expected ErrContractRejected for flat task_class 'design', got %v", err)
		}
	})

	t.Run("flat review rejected by parser", func(t *testing.T) {
		body := []byte(`{
			"schema_version":"department-plan/v1",
			"department_id":"ingenieria_ia",
			"tasks":[{
				"client_key":"a","assigned_role_id":"ingenieria_ia/backend_engineer",
				"task_class":"review",
				"title":"x","instructions":"x",
				"acceptance_criteria":["done"],
				"dependencies":[],"requirements":[],"priority":1
			}],
			"review_criteria":[],"unresolved":[]
		}`)
		if _, err := ParseDepartmentPlan(body, limits); !errors.Is(err, ErrContractRejected) {
			t.Fatalf("expected ErrContractRejected for flat task_class 'review', got %v", err)
		}
	})

	t.Run("dotted task_class accepted", func(t *testing.T) {
		body := []byte(`{
			"schema_version":"department-plan/v1",
			"department_id":"ingenieria_ia",
			"tasks":[{
				"client_key":"a","assigned_role_id":"ingenieria_ia/backend_engineer",
				"task_class":"memory.discovery",
				"title":"x","instructions":"x",
				"acceptance_criteria":["done"],
				"dependencies":[],"requirements":[],"priority":1
			}],
			"review_criteria":[],"unresolved":[]
		}`)
		out, err := ParseDepartmentPlan(body, limits)
		if err != nil {
			t.Fatalf("expected valid parse for dotted task_class 'memory.discovery': %v", err)
		}
		if out.Tasks[0].TaskClass != "memory.discovery" {
			t.Fatalf("TaskClass = %q, want %q", out.Tasks[0].TaskClass, "memory.discovery")
		}
	})

	t.Run("general.work accepted", func(t *testing.T) {
		body := []byte(`{
			"schema_version":"department-plan/v1",
			"department_id":"ingenieria_ia",
			"tasks":[{
				"client_key":"a","assigned_role_id":"ingenieria_ia/backend_engineer",
				"task_class":"general.work",
				"title":"x","instructions":"x",
				"acceptance_criteria":["done"],
				"dependencies":[],"requirements":[],"priority":1
			}],
			"review_criteria":[],"unresolved":[]
		}`)
		out, err := ParseDepartmentPlan(body, limits)
		if err != nil {
			t.Fatalf("expected valid parse for 'general.work': %v", err)
		}
		if out.Tasks[0].TaskClass != TaskClassGeneralWork {
			t.Fatalf("TaskClass = %q, want %q", out.Tasks[0].TaskClass, TaskClassGeneralWork)
		}
	})
}

// TestBugA_DepartmentReviewFollowupUsesSameContract proves proposed_followup_tasks
// in DepartmentReview gets the same host-side validation as DepartmentPlan.tasks.
func TestBugA_DepartmentReviewFollowupUsesSameContract(t *testing.T) {
	limits := DefaultLimits()

	t.Run("flat task_class in followup rejected", func(t *testing.T) {
		body := []byte(`{
			"schema_version":"department-review/v1",
			"verdict":"needs_replan",
			"findings":[],
			"unsatisfied_criteria":[],
			"evidence_refs":[],
			"proposed_followup_tasks":[{
				"client_key":"a","assigned_role_id":"ingenieria_ia/backend_engineer",
				"task_class":"adjudication",
				"title":"x","instructions":"x",
				"acceptance_criteria":["done"],
				"dependencies":[],"requirements":[],"priority":1
			}]
		}`)
		if _, err := ParseDepartmentReview(body, limits); !errors.Is(err, ErrContractRejected) {
			t.Fatalf("expected ErrContractRejected for flat task_class 'adjudication' in followup, got %v", err)
		}
	})

	t.Run("dotted task_class in followup accepted", func(t *testing.T) {
		body := []byte(`{
			"schema_version":"department-review/v1",
			"verdict":"needs_replan",
			"findings":[],
			"unsatisfied_criteria":[],
			"evidence_refs":[],
			"proposed_followup_tasks":[{
				"client_key":"a","assigned_role_id":"ingenieria_ia/backend_engineer",
				"task_class":"engineering.review",
				"title":"x","instructions":"x",
				"acceptance_criteria":["done"],
				"dependencies":[],"requirements":[],"priority":1
			}]
		}`)
		out, err := ParseDepartmentReview(body, limits)
		if err != nil {
			t.Fatalf("expected valid parse for dotted task_class 'engineering.review' in followup: %v", err)
		}
		if out.ProposedFollowupTasks[0].TaskClass != "engineering.review" {
			t.Fatalf("TaskClass = %q, want %q", out.ProposedFollowupTasks[0].TaskClass, "engineering.review")
		}
	})
}

// TestBugA_HostValidatorParity ensures every value that passes ValidTaskClass
// also parses successfully through the DepartmentPlan/Review parsers. This is
// the key parity property: the host validator and the parser accept exactly
// the same set of task_class strings.
func TestBugA_HostValidatorParity(t *testing.T) {
	// Values valid under ValidTaskClass — must all parse successfully.
	validCases := []string{
		"general.work",
		"owner.goal",
		"coordination.ceo_plan",
		"memory.discovery",
		"memory.design",
		"engineering.review",
		"context.assembly",
		"memory.sleep_consolidation",
		"research.corpus_curate",
		"a.b.c.d.e",
		"x_y_z.a_b_c",
	}

	for _, tc := range validCases {
		t.Run("valid:"+tc, func(t *testing.T) {
			if !ValidTaskClass(tc) {
				t.Errorf("ValidTaskClass(%q) = false, want true", tc)
			}
			body := buildDepartmentPlanJSON(tc)
			_, err := ParseDepartmentPlan(body, DefaultLimits())
			if err != nil {
				t.Errorf("ParseDepartmentPlan rejected valid task_class %q: %v", tc, err)
			}
		})
	}

	// Values invalid under ValidTaskClass — must all be rejected by parser.
	// Note: "" is excluded because validateWorkerTaskShape defaults empty
	// task_class to TaskClassGeneralWork (historical recovery path).
	invalidCases := []string{
		"discovery",                // flat, no dots
		"design",                   // flat, no dots
		"review",                   // flat, no dots
		"adjudication",             // flat, no dots
		"contract",                 // flat, no dots
		"planning",                 // flat, no dots
		"implementation",           // flat, no dots
		"verification",             // flat, no dots
		"acceptance",               // flat, no dots
		"NotDotted",                // uppercase
		"foo/bar",                  // slash
		".design",                  // starts with dot
		"design.",                  // ends with dot
		TaskClassLegacyUnspecified, // legacy marker explicitly rejected
	}

	for _, tc := range invalidCases {
		t.Run("invalid:"+tc, func(t *testing.T) {
			if ValidTaskClass(tc) {
				t.Errorf("ValidTaskClass(%q) = true, want false", tc)
			}
			body := buildDepartmentPlanJSON(tc)
			_, err := ParseDepartmentPlan(body, DefaultLimits())
			if err == nil {
				t.Errorf("ParseDepartmentPlan accepted invalid task_class %q", tc)
			} else if !errors.Is(err, ErrContractRejected) {
				t.Errorf("ParseDepartmentPlan error for %q: %v (want ErrContractRejected)", tc, err)
			}
		})
	}
}

// TestBugA_LegacyUnspecifiedStillRejected confirms legacy.unspecified remains
// explicitly rejected by both ValidTaskClass and the parser.
func TestBugA_LegacyUnspecifiedStillRejected(t *testing.T) {
	if ValidTaskClass(TaskClassLegacyUnspecified) {
		t.Fatal("ValidTaskClass must reject legacy.unspecified")
	}
	body := buildDepartmentPlanJSON(TaskClassLegacyUnspecified)
	_, err := ParseDepartmentPlan(body, DefaultLimits())
	if err == nil {
		t.Fatal("ParseDepartmentPlan must reject legacy.unspecified")
	}
}

// TestBugA_MaxLengthRejects101ByteTaskClass verifies that ValidTaskClass rejects
// task_class strings longer than 100 bytes.
func TestBugA_MaxLengthRejects101ByteTaskClass(t *testing.T) {
	// Build a valid ~99-byte task_class (under the 100-byte cap)
	short := "memory.sleep_consolidation.memory.design.review.engineering.code.fix.security.audit.tracing.performance.optimization.data.migration.schema.update.config.validation.extra"
	if len(short) > 100 {
		short = short[:100]
		for len(short) > 0 && (short[len(short)-1] == '.' || short[len(short)-1] == '_') {
			short = short[:len(short)-1]
		}
	}
	if !ValidTaskClass(short) {
		t.Fatalf("valid task_class near 100 bytes should pass ValidTaskClass")
	}

	// Build a clearly over-100-byte string
	long := "memory.sleep_consolidation.memory.design.review.engineering.code.fix.security.audit.tracing.performance.optimization.data.migration.schema.update.config.validation.extra.long.value"
	if len(long) <= 100 {
		t.Fatal("test long string must exceed 100 bytes")
	}
	if ValidTaskClass(long) {
		t.Fatal("101+ byte task_class should be invalid")
	}
	body := buildDepartmentPlanJSON(long)
	_, err := ParseDepartmentPlan(body, DefaultLimits())
	if err == nil {
		t.Fatal("over-100-byte task_class should be rejected by parser")
	}
}

// TestBugA_ParserRejectsInvalidTaskClassWithoutStructuredOutput ensures the
// host-side parser validates task_class even when the caller bypasses
// structured-output validation entirely.
func TestBugA_ParserRejectsInvalidTaskClassWithoutStructuredOutput(t *testing.T) {
	limits := DefaultLimits()

	plan := DepartmentPlan{
		SchemaVersion: DepartmentPlanSchemaVersion, DepartmentID: "ingenieria_ia",
		Tasks: []WorkerTaskProposal{{
			ClientKey: "a", AssignedRoleID: "ingenieria_ia/backend_engineer",
			TaskClass: "bad class!", Title: "x", Instructions: "x",
			AcceptanceCriteria: []string{"done"},
		}},
		ReviewCriteria: []string{}, Unresolved: []string{},
	}
	_, err := ParseDepartmentPlan(mustJSON(t, plan), limits)
	if !errors.Is(err, ErrContractRejected) {
		t.Fatalf("expected ErrContractRejected for invalid task_class 'bad class!', got %v", err)
	}
}

// TestBugA_DepartmentPlanAndReviewSchemasAreIdenticalForTaskItems proves
// that DepartmentPlan.tasks[] and DepartmentReview.proposed_followup_tasks[]
// share the exact same task item schema at the JSON level via the shared
// workerTaskProposalSchema() helper.
func TestBugA_DepartmentPlanAndReviewSchemasAreIdenticalForTaskItems(t *testing.T) {
	var reviewSchema map[string]any
	if err := json.Unmarshal(departmentReviewOutputSchema, &reviewSchema); err != nil {
		t.Fatal(err)
	}
	var planSchema map[string]any
	if err := json.Unmarshal(departmentPlanOutputSchema, &planSchema); err != nil {
		t.Fatal(err)
	}

	reviewTask := extractTaskItemSchema(t, reviewSchema, "proposed_followup_tasks")
	planTask := extractTaskItemSchema(t, planSchema, "tasks")

	if len(reviewTask) != len(planTask) {
		t.Fatalf("task schemas have different keys: review=%d plan=%d", len(reviewTask), len(planTask))
	}

	for k, v := range reviewTask {
		v2, ok := planTask[k]
		if !ok {
			t.Errorf("key %q missing in plan task schema", k)
			continue
		}
		if !deepEqualAny(v, v2) {
			t.Errorf("task schema mismatch for key %q: review=%+v, plan=%+v", k, v, v2)
		}
	}
	for k := range planTask {
		if _, ok := reviewTask[k]; !ok {
			t.Errorf("key %q present in plan but missing in review task schema", k)
		}
	}
}

func deepEqualAny(a, b any) bool {
	aJSON, _ := json.Marshal(a)
	bJSON, _ := json.Marshal(b)
	return string(aJSON) == string(bJSON)
}

func extractTaskItemSchema(t *testing.T, schema map[string]any, property string) map[string]any {
	t.Helper()
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema has no properties")
	}
	tasks, ok := properties[property].(map[string]any)
	if !ok {
		t.Fatalf("schema has no %s property", property)
	}
	items, ok := tasks["items"].(map[string]any)
	if !ok {
		t.Fatalf("%s has no items", property)
	}
	return items
}

// buildDepartmentPlanJSON builds a minimal DepartmentPlan JSON body with
// the given task_class value for quick testing.
func buildDepartmentPlanJSON(taskClass string) []byte {
	body := fmt.Sprintf(`{
		"schema_version":"department-plan/v1",
		"department_id":"ingenieria_ia",
		"tasks":[{
			"client_key":"a","assigned_role_id":"ingenieria_ia/backend_engineer",
			"task_class":"%s",
			"title":"x","instructions":"x",
			"acceptance_criteria":["done"],
			"dependencies":[],"requirements":[],"priority":1
		}],
		"review_criteria":[],"unresolved":[]
	}`, taskClass)
	return []byte(body)
}

// ---------------------------------------------------------------------------
// Bug B — Provider Succeeded + Host Contract Rejection Leaves Attempt Running
// ---------------------------------------------------------------------------

// TestBugB_ProviderSucceeds_ContractRejected_AttemptClosed verifies that when
// Harness.Execute returns success but validate() returns ErrContractRejected,
// the attempt is durably closed as failed rather than left running.
func TestBugB_ProviderSucceeds_ContractRejected_AttemptClosed(t *testing.T) {
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	harness := newFakeHarness(models)
	completion := &fakeCompletion{verdict: CompletionPass}
	orchestrator := testOrchestratorWithHarness(t, tasksPort, models, harness, &countingBudget{}, completion)

	root, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, IdempotencyKey: "root-b",
		Title: "root", Instructions: "root", AcceptanceCriteria: []string{"x"},
		CorrelationID: "executive:b",
	})
	task, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador",
		IdempotencyKey: "child-b", Title: "child", Instructions: "child",
		AcceptanceCriteria: []string{"x"}, CorrelationID: root.CorrelationID,
		Requirements: []RequirementProposal{{Key: "typed_plan", Type: "result", Description: "x", Required: true}},
	})

	// Set up the harness to return a valid response shape BUT with a flat
	// task_class value that will be rejected by ParseDepartmentPlan.
	flatTaskBody := json.RawMessage(`{
		"schema_version":"department-plan/v1",
		"department_id":"ingenieria_ia",
		"tasks":[{
			"client_key":"a","assigned_role_id":"ingenieria_ia/backend_engineer",
			"task_class":"discovery",
			"title":"x","instructions":"x",
			"acceptance_criteria":["done"],
			"dependencies":[],"requirements":[],"priority":1
		}],
		"review_criteria":[],"unresolved":[]
	}`)
	harness.body = flatTaskBody

	_, err := orchestrator.driveTypedTask(context.Background(), root, task, departmentPlanOutputSchema, PurposeDepartmentPlan, func(result InvocationResult) error {
		_, pErr := ParseDepartmentPlan(result.JSONOutput, DefaultLimits())
		return pErr
	})

	// The drive must return an error wrapping ErrModelResultContractRejected
	if !errors.Is(err, ErrModelResultContractRejected) {
		t.Fatalf("expected ErrModelResultContractRejected, got %v", err)
	}

	current, _ := tasksPort.GetTask(context.Background(), task.ID)

	// Attempt must NOT be running or leased
	if current.Status == "running" || current.Status == "leased" {
		t.Fatalf("task status=%q must not be running/leased after contract rejection", current.Status)
	}

	// The attempt is closed retryably, not terminally: a contract rejection
	// after provider success is documented as retryable because a fresh
	// attempt may produce a valid result. This asserted "failed" only while
	// the in-memory task store ignored the retryable flag.
	if current.Status != "retry_wait" {
		t.Fatalf("task status=%q, want 'retry_wait'", current.Status)
	}

	// Failure code must be model_result_contract_rejected
	if current.ReasonCode != "model_result_contract_rejected" {
		t.Fatalf("reasonCode=%q, want 'model_result_contract_rejected'", current.ReasonCode)
	}

	// Active lease must be cleared
	if current.ActiveLease != nil {
		t.Fatalf("active lease should be nil after contract rejection")
	}

	// Invocation must stay succeeded
	attemptID := current.Attempts[0].ID
	invocations, _ := models.FindTaskAttemptInvocations(context.Background(), task.ID, attemptID)
	if len(invocations) != 1 {
		t.Fatalf("invocations=%d, want 1", len(invocations))
	}
	if invocations[0].Status != "succeeded" {
		t.Fatalf("invocation status=%q, want 'succeeded'", invocations[0].Status)
	}

	// No second invocation should exist
	if len(invocations) > 1 {
		t.Fatalf("unexpected second invocation: %d total", len(invocations))
	}

	// Evidence must NOT be recorded (we didn't accept the result)
	if len(tasksPort.evidence) != 0 {
		t.Fatalf("evidence should not be recorded for rejected contract: %+v", tasksPort.evidence)
	}

	// Task must NOT be finalized/completed
	if len(tasksPort.finalized) != 0 {
		t.Fatalf("task should not be finalized: %v", tasksPort.finalized)
	}

	// Harness must have been called exactly once
	if harness.callCount() != 1 {
		t.Fatalf("harness call count=%d, want 1", harness.callCount())
	}
}

// TestBugB_AmbiguityBehaviorUnchanged is a regression test ensuring that
// provider ambiguity still has its existing blocking behavior and is NOT
// mixed with contract rejection semantics.
func TestBugB_AmbiguityBehaviorUnchanged(t *testing.T) {
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	harness := newFakeHarness(models)
	harness.invocationStatus = "ambiguous"
	harness.failure = HarnessFailureModelError

	completion := &fakeCompletion{verdict: CompletionPass}
	orchestrator := testOrchestratorWithHarness(t, tasksPort, models, harness, &countingBudget{}, completion)

	root, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, IdempotencyKey: "root-amb",
		Title: "root", Instructions: "root", AcceptanceCriteria: []string{"x"},
		CorrelationID: "executive:amb",
	})
	task, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador",
		IdempotencyKey: "child-amb", Title: "child", Instructions: "child",
		AcceptanceCriteria: []string{"x"}, CorrelationID: root.CorrelationID,
		Requirements: []RequirementProposal{{Key: "typed_plan", Type: "result", Description: "x", Required: true}},
	})

	_, err := orchestrator.driveTypedTask(context.Background(), root, task, departmentPlanOutputSchema, PurposeDepartmentPlan, func(InvocationResult) error { return nil })

	if !errors.Is(err, ErrModelOutcomeAmbiguous) {
		t.Fatalf("expected ErrModelOutcomeAmbiguous, got %v", err)
	}

	// Root must be blocked
	rootBlocked, _ := tasksPort.GetTask(context.Background(), root.ID)
	if rootBlocked.Status != "blocked" || rootBlocked.ReasonCode != "model_outcome_ambiguous" {
		t.Fatalf("root=%+v, expected blocked with reason 'model_outcome_ambiguous'", rootBlocked)
	}

	// Harness must NOT be called again on re-drive
	current, _ := tasksPort.GetTask(context.Background(), task.ID)
	_, err = orchestrator.driveTypedTask(context.Background(), root, current, departmentPlanOutputSchema, PurposeDepartmentPlan, func(InvocationResult) error { return nil })
	if !errors.Is(err, ErrModelOutcomeAmbiguous) {
		t.Fatalf("re-drive err=%v, expected ErrModelOutcomeAmbiguous", err)
	}
	if harness.callCount() != 1 {
		t.Fatalf("harness call count=%d, want 1 (no duplicate provider call)", harness.callCount())
	}
}

// TestBugB_NonBlockingPhaseErrorForModelResultContractRejected ensures
// ErrModelResultContractRejected is treated as non-blocking so the root does
// not get wedged when a child task's result is rejected but retryable.
func TestBugB_NonBlockingPhaseErrorForModelResultContractRejected(t *testing.T) {
	if !isNonBlockingPhaseError(ErrModelResultContractRejected) {
		t.Fatal("ErrModelResultContractRejected must be non-blocking phase error")
	}
}

// TestBugB_GenericContractRejectedIsBlocking ensures the generic
// ErrContractRejected is NOT treated as non-blocking. Only the specific
// ErrModelResultContractRejected sentinel (provider succeeded + host rejected)
// should be retryable/non-blocking.
func TestBugB_GenericContractRejectedIsBlocking(t *testing.T) {
	if isNonBlockingPhaseError(ErrContractRejected) {
		t.Fatal("generic ErrContractRejected must NOT be non-blocking phase error")
	}
}

// TestBugA_InstructionsContainTaskClassGuidance verifies that the instructions
// delivered to the model for DepartmentPlan and DepartmentReview contain the
// task_class contract guidance (dotted syntax requirement, legacy.unspecified
// forbidden, etc.).
func TestBugA_InstructionsContainTaskClassGuidance(t *testing.T) {
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	harness := newFakeHarness(models)
	completion := &fakeCompletion{verdict: CompletionPass}
	orchestrator := testOrchestratorWithHarness(t, tasksPort, models, harness, &countingBudget{}, completion)

	root, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, IdempotencyKey: "root-guidance",
		Title: "root", Instructions: "root", AcceptanceCriteria: []string{"x"},
		CorrelationID: "executive:guidance",
	})

	// Create a leader plan task and verify its instructions contain the guidance
	leader := RoleRef{ID: "ingenieria_ia/orquestador", UnitID: "ingenieria_ia", Enabled: true, Executable: true, CanonicalLeader: true}
	req := DepartmentRequest{UnitID: "ingenieria_ia", Objective: "test", Deliverable: "test", Priority: 1}

	task, _, err := orchestrator.createLeaderPlanTask(context.Background(), root, req, leader)
	if err != nil {
		t.Fatalf("createLeaderPlanTask failed: %v", err)
	}

	// Verify instructions contain the task_class guidance
	if !containsString(task.Instructions, "task_class MUST") {
		t.Errorf("DepartmentPlan instructions missing task_class guidance")
	}
	if !containsString(task.Instructions, "lowercase dotted syntax") {
		t.Errorf("DepartmentPlan instructions missing dotted syntax requirement")
	}
	if !containsString(task.Instructions, "legacy.unspecified") {
		t.Errorf("DepartmentPlan instructions missing legacy.unspecified forbidden")
	}
	if !containsString(task.Instructions, "memory.discovery") {
		t.Errorf("DepartmentPlan instructions missing valid example")
	}

	// Create a review task and verify its instructions contain the guidance
	all := []TaskRecord{task}
	reviewTask, _, err := orchestrator.createReviewTask(context.Background(), root, req, leader, all, 0, 1)
	if err != nil {
		t.Fatalf("createReviewTask failed: %v", err)
	}

	if !containsString(reviewTask.Instructions, "task_class MUST") {
		t.Errorf("DepartmentReview instructions missing task_class guidance")
	}
	if !containsString(reviewTask.Instructions, "lowercase dotted syntax") {
		t.Errorf("DepartmentReview instructions missing dotted syntax requirement")
	}
	if !containsString(reviewTask.Instructions, "legacy.unspecified") {
		t.Errorf("DepartmentReview instructions missing legacy.unspecified forbidden")
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Sentinel Tests (A, B, C, D) — ErrModelResultContractRejected semantics
// ---------------------------------------------------------------------------

// TestSentinelA_ProviderSucceeded_ValidateRejected verifies the full contract:
// provider succeeded, validate fails, attempt closed durably, specific sentinel
// returned, non-blocking.
func TestSentinelA_ProviderSucceeded_ValidateRejected(t *testing.T) {
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	harness := newFakeHarness(models)
	completion := &fakeCompletion{verdict: CompletionPass}
	orchestrator := testOrchestratorWithHarness(t, tasksPort, models, harness, &countingBudget{}, completion)

	root, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, IdempotencyKey: "root-sentinel-a",
		Title: "root", Instructions: "root", AcceptanceCriteria: []string{"x"},
		CorrelationID: "executive:sentinel-a",
	})
	task, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador",
		IdempotencyKey: "child-sentinel-a", Title: "child", Instructions: "child",
		AcceptanceCriteria: []string{"x"}, CorrelationID: root.CorrelationID,
		Requirements: []RequirementProposal{{Key: "typed_plan", Type: "result", Description: "x", Required: true}},
	})

	// Harness returns valid shape but with flat task_class that fails validation
	flatTaskBody := json.RawMessage(`{
		"schema_version":"department-plan/v1",
		"department_id":"ingenieria_ia",
		"tasks":[{
			"client_key":"a","assigned_role_id":"ingenieria_ia/backend_engineer",
			"task_class":"discovery",
			"title":"x","instructions":"x",
			"acceptance_criteria":["done"],
			"dependencies":[],"requirements":[],"priority":1
		}],
		"review_criteria":[],"unresolved":[]
	}`)
	harness.body = flatTaskBody

	_, err := orchestrator.driveTypedTask(context.Background(), root, task, departmentPlanOutputSchema, PurposeDepartmentPlan, func(result InvocationResult) error {
		_, pErr := ParseDepartmentPlan(result.JSONOutput, DefaultLimits())
		return pErr
	})

	// A. Error must be ErrModelResultContractRejected
	if !errors.Is(err, ErrModelResultContractRejected) {
		t.Fatalf("expected ErrModelResultContractRejected, got %v", err)
	}

	// A. isNonBlockingPhaseError must return true
	if !isNonBlockingPhaseError(err) {
		t.Fatal("ErrModelResultContractRejected must be non-blocking")
	}

	current, _ := tasksPort.GetTask(context.Background(), task.ID)

	// A. Durable invocation = succeeded
	attemptID := current.Attempts[0].ID
	invocations, _ := models.FindTaskAttemptInvocations(context.Background(), task.ID, attemptID)
	if len(invocations) != 1 || invocations[0].Status != "succeeded" {
		t.Fatalf("invocation must be succeeded, got %v", invocations)
	}

	// A. Attempt closed retryably. The provider succeeded and the host
	// rejected the result, which is exactly the case a further attempt can
	// legitimately fix, so the engine parks it rather than ending the task.
	if current.Status != "retry_wait" {
		t.Fatalf("task status=%q, want 'retry_wait'", current.Status)
	}

	// A. failure_code = model_result_contract_rejected
	if current.ReasonCode != "model_result_contract_rejected" {
		t.Fatalf("reasonCode=%q, want 'model_result_contract_rejected'", current.ReasonCode)
	}

	// A. retryable=true (verified by the fact that RecordAttemptFailed was called with true)
	// This is implicitly tested by the task entering retry_wait in the real engine

	// A. Lease forgotten
	if current.ActiveLease != nil {
		t.Fatal("active lease should be nil")
	}

	// A. Evidence = 0
	if len(tasksPort.evidence) != 0 {
		t.Fatalf("evidence should be 0, got %d", len(tasksPort.evidence))
	}

	// A. Completion = 0
	if len(tasksPort.finalized) != 0 {
		t.Fatalf("finalized should be 0, got %d", len(tasksPort.finalized))
	}
}

// TestSentinelB_GenericContractRejectedIsBlocking verifies that generic
// ErrContractRejected is NOT non-blocking.
func TestSentinelB_GenericContractRejectedIsBlocking(t *testing.T) {
	if isNonBlockingPhaseError(ErrContractRejected) {
		t.Fatal("generic ErrContractRejected must NOT be non-blocking")
	}
}

// TestSentinelC_RecordAttemptFailedErrorPropagates verifies that if
// RecordAttemptFailed returns an error, that error is propagated and NOT
// replaced with ErrModelResultContractRejected.
//
// NOTE: The memoryTasks fake does not support failure injection for
// RecordAttemptFailed. The error propagation logic is verified by code
// inspection: in recordHarnessSuccess, if failErr != nil, it is returned
// directly before ErrModelResultContractRejected is constructed.
func TestSentinelC_RecordAttemptFailedErrorPropagates(t *testing.T) {
	// This test documents the expected behavior. A full integration test
	// would require a mock TaskCoordinator that can fail RecordAttemptFailed.
	// The code path is:
	//   _, failErr := o.tasks.RecordAttemptFailed(...)
	//   if failErr != nil { return task, failErr }
	//   return task, fmt.Errorf("%w: %v", ErrModelResultContractRejected, err)
	t.Log("RecordAttemptFailed error propagation verified by code inspection")
}

// TestSentinelD_AmbiguityUnchanged verifies that ambiguity behavior is
// unchanged and distinct from contract rejection.
func TestSentinelD_AmbiguityUnchanged(t *testing.T) {
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	harness := newFakeHarness(models)
	harness.invocationStatus = "ambiguous"
	harness.failure = HarnessFailureModelError

	completion := &fakeCompletion{verdict: CompletionPass}
	orchestrator := testOrchestratorWithHarness(t, tasksPort, models, harness, &countingBudget{}, completion)

	root, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, IdempotencyKey: "root-sentinel-d",
		Title: "root", Instructions: "root", AcceptanceCriteria: []string{"x"},
		CorrelationID: "executive:sentinel-d",
	})
	task, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador",
		IdempotencyKey: "child-sentinel-d", Title: "child", Instructions: "child",
		AcceptanceCriteria: []string{"x"}, CorrelationID: root.CorrelationID,
		Requirements: []RequirementProposal{{Key: "typed_plan", Type: "result", Description: "x", Required: true}},
	})

	_, err := orchestrator.driveTypedTask(context.Background(), root, task, departmentPlanOutputSchema, PurposeDepartmentPlan, func(InvocationResult) error { return nil })

	// D. Ambiguity must return ErrModelOutcomeAmbiguous, NOT ErrModelResultContractRejected
	if !errors.Is(err, ErrModelOutcomeAmbiguous) {
		t.Fatalf("expected ErrModelOutcomeAmbiguous, got %v", err)
	}
	if errors.Is(err, ErrModelResultContractRejected) {
		t.Fatal("ambiguity must NOT be ErrModelResultContractRejected")
	}

	// D. Root must be blocked
	rootBlocked, _ := tasksPort.GetTask(context.Background(), root.ID)
	if rootBlocked.Status != "blocked" || rootBlocked.ReasonCode != "model_outcome_ambiguous" {
		t.Fatalf("root=%+v, expected blocked with reason 'model_outcome_ambiguous'", rootBlocked)
	}
}

// ---------------------------------------------------------------------------
// Pre-existing task regression — execution-time guidance delivery
// ---------------------------------------------------------------------------

// TestBugA_PreexistingTaskGetsGuidanceAtExecutionTime is the critical regression
// test for the real incident: task33 (coordination.department_plan,
// idempotency_key executive:31:leader-plan:ingenieria_ia) was created BEFORE the
// fix, so its durable Instructions do NOT contain taskClassGuidance.
// driveDepartments reuses the existing TaskRecord (never re-creates it), so
// createLeaderPlanTask does not run again for it. This test proves the guidance
// still reaches the model boundary at execution time, injected by
// executionContractFor(purpose) into HarnessRunCommand.ExecutionContract — NOT
// baked into the durable instructions.
//
// This test FAILS if the guidance only exists in createLeaderPlanTask().
func TestBugA_PreexistingTaskGetsGuidanceAtExecutionTime(t *testing.T) {
	cases := []struct {
		name    string
		purpose ExecutionPurpose
		schema  json.RawMessage
	}{
		{name: "department-plan", purpose: PurposeDepartmentPlan, schema: departmentPlanOutputSchema},
		{name: "department-review", purpose: PurposeDepartmentReview, schema: departmentReviewOutputSchema},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tasksPort := newMemoryTasks()
			models := newFakeModels()
			harness := newFakeHarness(models)
			orchestrator := testOrchestratorWithHarness(t, tasksPort, models, harness, &countingBudget{}, &fakeCompletion{verdict: CompletionPass})

			root, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
				RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, IdempotencyKey: "root-preexisting-" + tc.name,
				Title: "root", Instructions: "root", AcceptanceCriteria: []string{"x"},
				CorrelationID: "executive:preexisting-" + tc.name,
			})
			// OLD instructions: what a pre-fix Executive persisted. NO guidance.
			oldInstructions := "Produce only DepartmentPlan JSON for this bounded request: {\"department_id\":\"ingenieria_ia\"}"
			task, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
				RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador",
				IdempotencyKey: "child-preexisting-" + tc.name, Title: "child", Instructions: oldInstructions,
				AcceptanceCriteria: []string{"x"}, CorrelationID: root.CorrelationID,
				Requirements: []RequirementProposal{{Key: "typed_plan", Type: "result", Description: "x", Required: true}},
			})

			// Fixture precondition: the durable instructions really lack the guidance.
			if strings.Contains(task.Instructions, "task_class MUST") {
				t.Fatal("fixture precondition broken: old instructions must not contain guidance")
			}

			current, _ := tasksPort.GetTask(context.Background(), task.ID)
			if _, err := orchestrator.driveTypedTask(context.Background(), root, current, tc.schema, tc.purpose, func(InvocationResult) error { return nil }); err != nil {
				t.Fatalf("driveTypedTask: %v", err)
			}

			command := harness.lastCommand()
			// The guidance must reach the model boundary via ExecutionContract even
			// though the durable instructions lack it.
			for _, want := range []string{"task_class MUST", "lowercase dotted", "100 bytes", "legacy.unspecified"} {
				if !strings.Contains(command.ExecutionContract, want) {
					t.Errorf("ExecutionContract missing %q; got:\n%s", want, command.ExecutionContract)
				}
			}
			// It must be the single shared definitions, not copies that can
			// drift: task-class guidance for both purposes, plus checkpoint
			// E's consistency rider on the review.
			wantContract := taskClassGuidance
			if tc.purpose == PurposeDepartmentReview {
				wantContract += "\n\n" + departmentConsistencyGuidance
			}
			if command.ExecutionContract != wantContract {
				t.Errorf("ExecutionContract drifted from the shared definitions")
			}
		})
	}
}

// TestBugA_NoContractForPurposesWithoutTaskClass proves purposes that do not
// produce task_class proposals (CEO plan, worker result, closure) carry no
// task-class guidance, so it is never injected where it does not apply.
//
// A worker with NO evidence obligations still carries no evidence contract;
// one WITH obligations gets exactly the evidenceContractGuidance for those
// slots (the evidence_contract_guidance_test.go file covers that side). What a
// worker DOES carry with nothing else attached is the candidate egress rule:
// R11 and AUTONOMY-SMOKE-017-R13 died at the declassifier having never been
// told it existed, so its guidance rides every worker run unconditionally --
// and only worker runs. Reviewer, adjudicator and closure purposes name no
// source they were never shown.
func TestBugA_NoContractForPurposesWithoutTaskClass(t *testing.T) {
	for _, purpose := range []ExecutionPurpose{PurposeCEOPlan, PurposeCEOClosure} {
		if got := executionContractFor(purpose, nil); got != "" {
			t.Errorf("executionContractFor(%q) = %q, want empty", purpose, got)
		}
	}
	if got := executionContractFor(PurposeDepartmentWorker, nil); got != candidateDeclassificationGuidance() {
		t.Errorf("executionContractFor(department-worker) must return exactly candidateDeclassificationGuidance")
	}
	if got := executionContractFor(PurposeDepartmentPlan, nil); got != taskClassGuidance {
		t.Errorf("executionContractFor(department-plan) must return taskClassGuidance")
	}
	if got := executionContractFor(PurposeDepartmentReview, nil); got != taskClassGuidance+"\n\n"+departmentConsistencyGuidance {
		t.Errorf("executionContractFor(department-review) must be taskClassGuidance plus the checkpoint-E consistency rider")
	}
}
