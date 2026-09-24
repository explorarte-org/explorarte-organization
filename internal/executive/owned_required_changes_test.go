package executive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The host knows exactly which required change each worker owns (RevisionOwnership). Until now
// the words of that change reached the worker only if the leader chose to copy them into the
// instructions, and a copy is a paraphrase: root 1265's round-2 worker had the test's name in its
// context three times and still wrote "the test table". These pin the property that closes it:
// every worker receives, deterministically and exclusively, the exact text of the required
// changes it owns.

const (
	ownedChangeName    = `State that the edit adds exactly one case to the table of TestExtractDigitRunsCoreCases named "digits adjacent to letters".`
	ownedChangeLiteral = `State the input as abc123def45 and the expected output as want []string{"123", "45"}.`
)

// ownedWorkerInstructions returns the instructions of the design-round worker with the given
// client-key prefix, whatever its replan ordinal.
func ownedWorkerInstructions(t *testing.T, f *wiringFixture, round int, key string) (string, bool) {
	t.Helper()
	root := f.rootRecord(t)
	all, err := f.tasks.ListByCorrelation(context.Background(), root.CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	prefix := fmt.Sprintf("executive:%s:worker:ingenieria_ia%s:%s", itoa64(root.ID), designRoundSuffix(round), key)
	for _, task := range all {
		if strings.HasPrefix(task.IdempotencyKey, prefix) {
			return task.Instructions, true
		}
	}
	return "", false
}

// A fixture whose round-1 adjudication demands two changes and whose round-2 plan gives one to
// each of its two workers: the owner takes the name, the support task takes the literals.
func ownedSplitFixture(t *testing.T) *wiringFixture {
	t.Helper()
	fixture := eFixture(t)
	fixture.harness.adjudicationRequiredChanges = []string{ownedChangeName, ownedChangeLiteral}
	fixture.eOwnership = ownerEntry("RC:1:1", eOwnerKey) + "," + ownerEntry("RC:1:2", eSupportKey)
	fixture.eOutcomes = `[` +
		`{"required_change_id":"RC:1:1","status":"resolved","canonical_resolution":"stated","conflicting_task_refs":[]},` +
		`{"required_change_id":"RC:1:2","status":"resolved","canonical_resolution":"stated","conflicting_task_refs":[]}]`
	driveCapability(t, fixture, 40)
	return fixture
}

// Regression A: the owner receives the adjudicator's exact words, including the test's name.
func TestTheOwnerWorkerReceivesItsRequiredChangeVerbatim(t *testing.T) {
	fixture := ownedSplitFixture(t)

	instructions, ok := ownedWorkerInstructions(t, fixture, 2, eOwnerKey)
	if !ok {
		t.Fatal("the round-2 owner worker was never materialized")
	}
	for _, want := range []string{
		ownedRequiredChangesHeader,
		"RC:1:1 " + ownedChangeName,
		"TestExtractDigitRunsCoreCases",
		ownedRequiredChangesRule,
	} {
		if !strings.Contains(instructions, want) {
			t.Errorf("owner instructions missing %q:\n%s", want, instructions)
		}
	}
	// The planner's own words and the department constraints are untouched: the owned block is
	// appended, never substituted.
	if !strings.Contains(instructions, "Address the assigned required changes.") {
		t.Errorf("the planner's instruction was displaced:\n%s", instructions)
	}
}

// Regression B: literal values arrive intact -- braces, quotes, and the digits.
func TestLiteralValuesInAnOwnedChangeArriveIntact(t *testing.T) {
	fixture := ownedSplitFixture(t)

	instructions, ok := ownedWorkerInstructions(t, fixture, 2, eSupportKey)
	if !ok {
		t.Fatal("the round-2 support worker was never materialized")
	}
	for _, want := range []string{"abc123def45", `want []string{"123", "45"}`, "RC:1:2 " + ownedChangeLiteral} {
		if !strings.Contains(instructions, want) {
			t.Errorf("literal %q did not arrive intact:\n%s", want, instructions)
		}
	}
}

// Regression C: authority stays exclusive. A worker never receives another worker's change.
func TestAWorkerDoesNotReceiveAChangeItDoesNotOwn(t *testing.T) {
	fixture := ownedSplitFixture(t)

	owner, ok := ownedWorkerInstructions(t, fixture, 2, eOwnerKey)
	if !ok {
		t.Fatal("owner worker missing")
	}
	support, ok := ownedWorkerInstructions(t, fixture, 2, eSupportKey)
	if !ok {
		t.Fatal("support worker missing")
	}
	if strings.Contains(owner, "RC:1:2") || strings.Contains(owner, "abc123def45") {
		t.Errorf("the owner of RC:1:1 received RC:1:2:\n%s", owner)
	}
	if strings.Contains(support, "RC:1:1") || strings.Contains(support, "TestExtractDigitRunsCoreCases") {
		t.Errorf("the owner of RC:1:2 received RC:1:1:\n%s", support)
	}
}

// A worker that owns nothing gets no owned block at all.
func TestAWorkerThatOwnsNothingGetsNoOwnedBlock(t *testing.T) {
	fixture := eFixture(t)
	fixture.harness.adjudicationRequiredChanges = []string{ownedChangeName}
	fixture.eOwnership = ownerEntry("RC:1:1", eOwnerKey)
	fixture.eOutcomes = `[{"required_change_id":"RC:1:1","status":"resolved","canonical_resolution":"stated","conflicting_task_refs":[]}]`
	driveCapability(t, fixture, 40)

	support, ok := ownedWorkerInstructions(t, fixture, 2, eSupportKey)
	if !ok {
		t.Fatal("support worker missing")
	}
	if strings.Contains(support, ownedRequiredChangesHeader) {
		t.Errorf("a worker with no owned change received the owned block:\n%s", support)
	}
	// Round 1 has no roster: nothing is owned and nothing is attached.
	first, ok := ownedWorkerInstructions(t, fixture, 1, eOwnerKey)
	if !ok {
		t.Fatal("round-1 worker missing")
	}
	if strings.Contains(first, ownedRequiredChangesHeader) {
		t.Errorf("a round-1 worker received an owned block:\n%s", first)
	}
}

// Regression D: a needs_replan redo inherits the words of what it redoes.
func TestAFollowupWorkerReceivesTheChangesItInherits(t *testing.T) {
	fixture := eFixture(t)
	fixture.harness.adjudicationRequiredChanges = []string{ownedChangeName, ownedChangeLiteral}
	fixture.eOwnership = ownerEntry("RC:1:1", eOwnerKey) + "," + ownerEntry("RC:1:2", eOwnerKey)
	fixture.eReviewVerdict = "needs_replan"
	fixture.eOutcomes = `[` +
		`{"required_change_id":"RC:1:1","status":"conflicted","canonical_resolution":"","conflicting_task_refs":["task:a","task:b"]},` +
		`{"required_change_id":"RC:1:2","status":"resolved","canonical_resolution":"cited","conflicting_task_refs":[]}]`
	fixture.eFollowups = `[{"client_key":"reconcile","assigned_role_id":"ingenieria_ia/qa","task_class":"engineering.review",` +
		`"title":"Reconcile","instructions":"One falsifiable claim.","acceptance_criteria":["Cite"],"dependencies":[]}]`
	fixture.eFollowupOwnership = "[" + ownerEntry("RC:1:1", "reconcile") + "," + ownerEntry("RC:1:2", "reconcile") + "]"
	base := fixture.harness.departmentReviewBody
	fixture.harness.departmentReviewBody = func(task TaskRecord) string {
		if strings.Contains(task.IdempotencyKey, ":replan:") {
			return eReplanReviewBody([]string{"RC:1:1", "RC:1:2"})
		}
		return base(task)
	}
	driveCapability(t, fixture, 40)

	instructions, ok := ownedWorkerInstructions(t, fixture, 2, "reconcile-replan:1")
	if !ok {
		t.Fatal("the follow-up worker was never materialized")
	}
	for _, want := range []string{ownedRequiredChangesHeader, "RC:1:1 " + ownedChangeName, "RC:1:2 " + ownedChangeLiteral, "abc123def45"} {
		if !strings.Contains(instructions, want) {
			t.Errorf("follow-up instructions missing %q:\n%s", want, instructions)
		}
	}
	if !strings.Contains(instructions, "One falsifiable claim.") {
		t.Errorf("the reviewer's follow-up instruction was displaced:\n%s", instructions)
	}
}

// Regression F: across three design rounds each round's owner receives THAT round's change,
// without degradation and without an earlier round's change leaking forward.
func TestEachRoundsOwnerReceivesThatRoundsChangeVerbatim(t *testing.T) {
	fixture := newWiringFixture(t, "revise", eSources(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition"}},
	}, WithRepositoryEvidenceSource("explorarte-organization", eWorld()), WithLimits(func() Limits {
		bounds := DefaultLimits()
		bounds.MaxDesignRounds = 3
		return bounds
	}()))
	h := fixture.harness
	h.bodies[PurposeDepartmentWorker] = eWorkerBody()
	h.adjudicationEvidence = `[{"subject":"driveDesignFreeze","relations":["application"]}]`
	h.adjudicationVerdictByRound = map[int]string{3: "freeze"}
	round1 := "Round one demand naming TestExtractDigitRunsCoreCases."
	round2 := `Round two demand with the literal want []string{"123", "45"}.`
	h.adjudicationRequiredChanges = []string{round1}
	h.adjudicationRequiredChangesByRound = map[int][]string{2: {round2}}

	worker := `{"client_key":"` + eOwnerKey + `","assigned_role_id":"ingenieria_ia/qa","task_class":"engineering.review",` +
		`"title":"Resolve","instructions":"Address the assigned required changes.","acceptance_criteria":["Cite"],"dependencies":[]}`
	h.departmentPlanBody = func(task TaskRecord) string {
		round := designRoundOf(task.IdempotencyKey)
		ownership := ""
		if round >= 2 {
			ownership = ownerEntry(fmt.Sprintf("RC:%d:1", round-1), eOwnerKey)
		}
		return `{"schema_version":"department-plan/v2","department_id":"ingenieria_ia","tasks":[` + worker + `],` +
			`"review_criteria":["Consistent"],"unresolved":[],"revision_ownership":[` + ownership + `]}`
	}
	h.departmentReviewBody = func(task TaskRecord) string {
		round := designRoundOf(task.IdempotencyKey)
		if round < 2 {
			return ""
		}
		return `{"schema_version":"department-review/v2","verdict":"accept","findings":["reviewed"],` +
			`"unsatisfied_criteria":[],"evidence_refs":[],"proposed_followup_tasks":[],"followup_ownership":[],` +
			`"revision_outcomes":[{"required_change_id":"RC:` + fmt.Sprint(round-1) + `:1","status":"resolved",` +
			`"canonical_resolution":"stated","conflicting_task_refs":[]}]}`
	}

	driveCapability(t, fixture, 80)

	two, ok := ownedWorkerInstructions(t, fixture, 2, eOwnerKey)
	if !ok {
		t.Fatal("the round-2 worker was never materialized")
	}
	three, ok := ownedWorkerInstructions(t, fixture, 3, eOwnerKey)
	if !ok {
		t.Fatal("the round-3 worker was never materialized")
	}
	if !strings.Contains(two, "RC:1:1 "+round1) || strings.Contains(two, round2) {
		t.Errorf("round 2 did not receive exactly round 1's change:\n%s", two)
	}
	if !strings.Contains(three, "RC:2:1 "+round2) || strings.Contains(three, round1) {
		t.Errorf("round 3 did not receive exactly round 2's change:\n%s", three)
	}
}

// Regression E (unit): the host fails closed, never silently short-handing a worker.
func TestOwnedChangesFailClosed(t *testing.T) {
	roster := []RequiredChange{{ID: "RC:1:1", Text: ownedChangeName}}
	orchestrator := &Orchestrator{limits: DefaultLimits()}
	proposals := []WorkerTaskProposal{{ClientKey: "w", Instructions: "Do the work."}}

	if _, err := ownedChangesByClientKey([]RevisionOwnership{{RequiredChangeID: "RC:9:9", OwnerClientKey: "w"}}, roster); !errors.Is(err, ErrContractRejected) {
		t.Errorf("a binding to a change outside the roster resolved: %v", err)
	}
	if _, err := orchestrator.carryOwnedRequiredChanges(proposals, map[string][]RequiredChange{"ghost": roster}); !errors.Is(err, ErrContractRejected) {
		t.Errorf("an owner the plan does not propose was accepted: %v", err)
	}
	huge := []RequiredChange{{ID: "RC:1:1", Text: strings.Repeat("x", orchestrator.limits.MaxInstructionsBytes)}}
	if _, err := orchestrator.carryOwnedRequiredChanges(proposals, map[string][]RequiredChange{"w": huge}); !errors.Is(err, ErrContractRejected) {
		t.Errorf("a block over the instruction budget was truncated or dropped instead of refused: %v", err)
	}
	got, err := orchestrator.carryOwnedRequiredChanges(proposals, map[string][]RequiredChange{"w": roster})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got[0].Instructions, ownedChangeName) || proposals[0].Instructions != "Do the work." {
		t.Errorf("carrying mutated its input or lost the text: %q / %q", got[0].Instructions, proposals[0].Instructions)
	}
}

// Regression E (campaign): a round whose owned text cannot be carried never creates a worker and
// never spends a model call on one. The planner's own instruction is near the budget, so the
// owned block does not fit; the host refuses instead of dropping it.
func TestARoundWhoseOwnedTextCannotBeCarriedCreatesNoWorker(t *testing.T) {
	fixture := eFixture(t)
	fixture.harness.adjudicationRequiredChanges = []string{ownedChangeName}
	fixture.eOwnership = ownerEntry("RC:1:1", eOwnerKey)
	fixture.eOutcomes = `[{"required_change_id":"RC:1:1","status":"resolved","canonical_resolution":"stated","conflicting_task_refs":[]}]`
	long := strings.Repeat("z", DefaultLimits().MaxInstructionsBytes-100)
	fixture.harness.departmentPlanBody = func(task TaskRecord) string {
		ownership := ""
		instructions := "Address the assigned required changes."
		if designRoundOf(task.IdempotencyKey) >= 2 {
			ownership = ownerEntry("RC:1:1", eOwnerKey)
			instructions = long
		}
		return `{"schema_version":"department-plan/v2","department_id":"ingenieria_ia","tasks":[` +
			`{"client_key":"` + eOwnerKey + `","assigned_role_id":"ingenieria_ia/qa","task_class":"engineering.review",` +
			`"title":"Resolve","instructions":"` + instructions + `","acceptance_criteria":["Cite"],"dependencies":[]}],` +
			`"review_criteria":["Consistent"],"unresolved":[],"revision_ownership":[` + ownership + `]}`
	}

	driveCapability(t, fixture, 40)

	if eRoundTwoWorkersExist(t, fixture) {
		t.Fatal("a worker was created without the text it owns")
	}
	// The refusal is retryable feedback to the planner, recorded on its plan task.
	refused := false
	all, err := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range all {
		if designRoundOf(task.IdempotencyKey) >= 2 && strings.Contains(task.Reason, "do not fit its instruction budget") {
			refused = true
		}
	}
	if !refused {
		t.Fatal("the planner was never told its instructions leave no room for the owned text")
	}
	planned := false
	for _, command := range fixture.harness.commands {
		task, err := fixture.tasks.GetTask(context.Background(), command.TaskID)
		if err != nil || designRoundOf(task.IdempotencyKey) < 2 {
			continue
		}
		if command.Purpose == PurposeDepartmentPlan {
			planned = true
		}
		if command.Purpose == PurposeDepartmentWorker {
			t.Fatalf("a round-2 worker ran without its owned text: %s", task.IdempotencyKey)
		}
	}
	if !planned {
		t.Fatal("the round-2 plan never ran; the refusal was not exercised")
	}
}
