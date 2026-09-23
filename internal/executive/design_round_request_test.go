package executive

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Production root 1159 (2026-09-22): the CEO plan's request for the department
// carried the campaign's literal values in its CONSTRAINTS ("name \"digits adjacent
// to letters\", text \"abc123def45\", want ..."). The first design round handed the
// planner the whole request; every later round rebuilt its instructions from the
// request's objective alone -- the CEO's paraphrase, which has none of them. The
// department was then told what SHAPE its deliverable needed and never what the
// values were, the adjudicator asked for them twice, and the rounds ran out.
//
// A round must not be able to narrow the request. These tests hold every stage that
// plans or proposes work for the department to the same one rendering of it.
const roundRequestLiteral = `The proposed case must have name "digits adjacent to letters", text "abc123def45", and want []string{"123", "45"}.`

func newRoundRequestFixture(t *testing.T) *freezeFixture {
	t.Helper()
	fixture := newFreezeFixture(t, "revise", true)
	quoted, err := json.Marshal(roundRequestLiteral)
	if err != nil {
		t.Fatal(err)
	}
	plan := freezeBodies()[PurposeCEOPlan]
	withConstraint := strings.Replace(plan, `"constraints":[]}]`, `"constraints":[`+string(quoted)+`]}]`, 1)
	if withConstraint == plan {
		t.Fatal("the fixture CEO plan has no constraints to extend")
	}
	fixture.harness.bodies[PurposeCEOPlan] = withConstraint
	return fixture
}

func TestEveryDesignRoundPlansAgainstTheWholeDepartmentRequest(t *testing.T) {
	fixture := newRoundRequestFixture(t)
	fixture.drive(t)
	all, err := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	// The exact rendering, not merely "mentions the value": a later round may not
	// narrow the request by rendering a subset of it.
	brief := fixture.orchestrator.departmentRequestBrief(DepartmentRequest{
		UnitID: "ingenieria_ia", Objective: "design", Deliverable: "candidate design", Priority: 1,
		Constraints: []string{roundRequestLiteral},
	})
	for _, key := range []string{
		"leader-plan:ingenieria_ia",                  // round 1
		"leader-plan:ingenieria_ia:design-round:2",   // the revision round that lost it
		"leader-review:ingenieria_ia",                // proposes follow-ups in round 1
		"leader-review:ingenieria_ia:design-round:2", // and in round 2
	} {
		task, ok := findTaskByKey(all, childKey(fixture.root, key))
		if !ok {
			t.Fatalf("no task %s: the fixture did not reach it", key)
		}
		if !strings.Contains(task.Instructions, brief) {
			t.Errorf("%s does not carry the department's whole request.\nwant it to contain: %s\ngot: %s", key, brief, task.Instructions)
		}
		for _, literal := range []string{"digits adjacent to letters", "abc123def45"} {
			if !strings.Contains(task.Instructions, literal) {
				t.Errorf("%s lost the campaign's literal %q", key, literal)
			}
		}
	}
}

// The revision round is told what to do with the constraints, not only handed them:
// the planner writes the acceptance criteria of the tasks it proposes, and root 1159's
// round-2 plan wrote them from the adjudicator's complaint about form instead.
func TestARevisionRoundIsToldToCarryTheConstraintsIntoItsTasks(t *testing.T) {
	fixture := newRoundRequestFixture(t)
	fixture.drive(t)
	all, err := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	plan, ok := findTaskByKey(all, childKey(fixture.root, "leader-plan:ingenieria_ia:design-round:2"))
	if !ok {
		t.Fatal("round 2 has no planning task")
	}
	for _, want := range []string{"ORIGINAL REQUEST", "unchanged since the first round", "carry the literal values", "REQUIRED CHANGES"} {
		if !strings.Contains(plan.Instructions, want) {
			t.Errorf("the revision plan lacks %q:\n%s", want, plan.Instructions)
		}
	}
	// The old paraphrase-only section is gone: nothing may read as the whole objective.
	if strings.Contains(plan.Instructions, "ORIGINAL OBJECTIVE") {
		t.Error("the revision plan still labels a subset of the request as the original objective")
	}
}

// One renderer, so the two cannot drift: the request the first plan sees is byte for
// byte the request every later stage sees.
func TestTheDepartmentRequestHasOneRenderer(t *testing.T) {
	fixture := newRoundRequestFixture(t)
	req := DepartmentRequest{UnitID: "ingenieria_ia", Objective: "o", Deliverable: "d", Priority: 3, Constraints: []string{"c1", "c2"}}
	got := fixture.orchestrator.departmentRequestBrief(req)
	var decoded map[string]any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("the brief is not JSON: %v\n%s", err, got)
	}
	for _, key := range []string{"department_id", "objective", "deliverable", "constraints", "priority"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("the brief dropped %q: %s", key, got)
		}
	}
}

// The values reach the WORKER, not only the planner: the host attaches the department's
// constraints to every task it materializes, so a round whose planner rewrote its criteria
// from the adjudicator's feedback (root 1159) still hands the worker what it must state.
func TestEveryWorkerTaskCarriesTheDepartmentConstraints(t *testing.T) {
	fixture := newRoundRequestFixture(t)
	fixture.drive(t)
	all, err := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	workers := 0
	for _, task := range all {
		if !strings.Contains(task.IdempotencyKey, ":worker:") {
			continue
		}
		workers++
		// The planner's own words are kept as written and the block follows them.
		planned := strings.Index(task.Instructions, "Write the design for M2.1.")
		header := strings.Index(task.Instructions, carriedConstraintsHeader)
		if planned < 0 || header < planned {
			t.Errorf("worker %s: the planner's instruction must come first and the constraints after it:\n%s", task.IdempotencyKey, task.Instructions)
		}
		if !strings.Contains(task.Instructions, carriedConstraintsHeader) || !strings.Contains(task.Instructions, "abc123def45") {
			t.Errorf("worker %s does not carry the department constraints:\n%s", task.IdempotencyKey, task.Instructions)
		}
	}
	if workers < 2 {
		t.Fatalf("the fixture materialized %d worker task(s); want a round-1 and a round-2 worker at least", workers)
	}
}

func TestConstraintsThatDoNotFitAreNamedAsOmittedNeverDroppedSilently(t *testing.T) {
	long := strings.Repeat("x", 200)
	block := constraintsBlock([]string{"first constraint", long, long, "last constraint"}, len(carriedConstraintsHeader)+120)
	if !strings.Contains(block, "first constraint") {
		t.Fatalf("the first constraint fits and must be shown:\n%s", block)
	}
	if !strings.Contains(block, "further constraint(s) not shown") {
		t.Fatalf("constraints were dropped without saying so:\n%s", block)
	}
	if constraintsBlock(nil, 1000) != "" || constraintsBlock([]string{"  ", ""}, 1000) != "" {
		t.Fatal("a request with no constraints must add nothing to a worker's instructions")
	}
}

// A follow-up task the department REVIEWER proposes in a replan is a worker task like
// any other: the constraints reach it too.
func TestAReplanFollowupCarriesTheDepartmentConstraints(t *testing.T) {
	fixture := eFixture(t)
	quoted, err := json.Marshal(roundRequestLiteral)
	if err != nil {
		t.Fatal(err)
	}
	ceo := fixture.harness.bodies[PurposeCEOPlan]
	extended := strings.Replace(ceo, `"constraints":[]`, `"constraints":[`+string(quoted)+`]`, 1)
	if extended == ceo {
		t.Fatalf("the fixture CEO plan has no constraints to extend: %s", ceo)
	}
	fixture.harness.bodies[PurposeCEOPlan] = extended
	fixture.eOwnership = ownerEntry("RC:1:1", eOwnerKey) + "," + ownerEntry("RC:1:2", eOwnerKey)
	fixture.eReviewVerdict = "needs_replan"
	fixture.eOutcomes = `[` +
		`{"required_change_id":"RC:1:1","status":"conflicted","canonical_resolution":"","conflicting_task_refs":["task:a","task:b"]},` +
		`{"required_change_id":"RC:1:2","status":"resolved","canonical_resolution":"cited","conflicting_task_refs":[]}]`
	fixture.eFollowups = `[{"client_key":"reconcile_mdr","assigned_role_id":"ingenieria_ia/qa","task_class":"engineering.review",` +
		`"title":"Reconcile MDR granularity","instructions":"One falsifiable claim.","acceptance_criteria":["Cite"],"dependencies":[]}]`
	fixture.eFollowupOwnership =
		"[" + ownerEntry("RC:1:1", "reconcile_mdr") + "," + ownerEntry("RC:1:2", "reconcile_mdr") + "]"
	base := fixture.harness.departmentReviewBody
	fixture.harness.departmentReviewBody = func(task TaskRecord) string {
		if strings.Contains(task.IdempotencyKey, ":replan:") {
			return eReplanReviewBody([]string{"RC:1:1", "RC:1:2"})
		}
		return base(task)
	}
	driveCapability(t, fixture, 30)

	all, err := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	followups := 0
	for _, task := range all {
		if !strings.Contains(task.IdempotencyKey, ":worker:") || !strings.Contains(task.IdempotencyKey, "-replan:") {
			continue
		}
		followups++
		if !strings.Contains(task.Instructions, "One falsifiable claim.") || !strings.Contains(task.Instructions, carriedConstraintsHeader) || !strings.Contains(task.Instructions, "abc123def45") {
			t.Errorf("follow-up %s does not carry the department constraints after its own instruction:\n%s", task.IdempotencyKey, task.Instructions)
		}
	}
	if followups == 0 {
		t.Fatal("no replan follow-up worker was materialized; the fixture did not reach the path under test")
	}
}

// Carrying the constraints never rewrites the proposals it was given: applying it twice to the
// same plan must not stack the block, and the durable plan the tasks came from stays as parsed.
func TestCarryingConstraintsLeavesTheProposalsUntouched(t *testing.T) {
	orchestrator := &Orchestrator{limits: DefaultLimits()}
	proposals := []WorkerTaskProposal{{ClientKey: "a", Instructions: "Do the thing."}}
	req := DepartmentRequest{UnitID: "u", Constraints: []string{"keep the literal"}}

	once := orchestrator.carryDepartmentConstraints(req, proposals)
	twice := orchestrator.carryDepartmentConstraints(req, proposals)

	if proposals[0].Instructions != "Do the thing." {
		t.Fatalf("the caller's proposal was rewritten: %q", proposals[0].Instructions)
	}
	if once[0].Instructions != twice[0].Instructions || strings.Count(twice[0].Instructions, carriedConstraintsHeader) != 1 {
		t.Fatalf("carrying is not repeatable: %q vs %q", once[0].Instructions, twice[0].Instructions)
	}
	// No constraints, no change at all -- including the very same slice.
	if got := orchestrator.carryDepartmentConstraints(DepartmentRequest{UnitID: "u"}, proposals); &got[0] != &proposals[0] {
		t.Fatal("a request without constraints must hand the proposals back unchanged")
	}
}

// A request over the instruction budget keeps what fits and says how many constraints it left
// out, instead of being replaced whole by a stub that names none of them.
func TestAnOversizedRequestKeepsWhatFitsAndCountsWhatDoesNot(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxInstructionsBytes = 500
	orchestrator := &Orchestrator{limits: limits}
	constraints := []string{"keep this literal: abc123def45", strings.Repeat("y", 400), strings.Repeat("z", 400), "and this one: digits adjacent to letters"}
	brief := orchestrator.departmentRequestBrief(DepartmentRequest{UnitID: "u", Objective: "o", Deliverable: "d", Priority: 1, Constraints: constraints})

	if len(brief) > limits.MaxInstructionsBytes {
		t.Fatalf("the brief is %d bytes, over its %d budget", len(brief), limits.MaxInstructionsBytes)
	}
	var decoded struct {
		Constraints        []string `json:"constraints"`
		ConstraintsOmitted int      `json:"constraints_omitted"`
	}
	if err := json.Unmarshal([]byte(brief), &decoded); err != nil {
		t.Fatalf("the brief is not the request any more: %v\n%s", err, brief)
	}
	if decoded.ConstraintsOmitted != 2 || len(decoded.Constraints) != 2 {
		t.Fatalf("kept %d, omitted %d; want the two that fit kept and the two that do not counted: %s", len(decoded.Constraints), decoded.ConstraintsOmitted, brief)
	}
	if !strings.Contains(brief, "abc123def45") || !strings.Contains(brief, "digits adjacent to letters") {
		t.Fatalf("a constraint that fits was dropped: %s", brief)
	}
	// A request that fits is rendered exactly as it always was: no marker, no reshaping.
	small := orchestrator.departmentRequestBrief(DepartmentRequest{UnitID: "u", Objective: "o", Deliverable: "d", Priority: 1, Constraints: []string{"c"}})
	if strings.Contains(small, "constraints_omitted") {
		t.Fatalf("a request within budget was reshaped: %s", small)
	}
}
