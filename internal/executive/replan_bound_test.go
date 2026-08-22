package executive

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
)

// AUTONOMY-SMOKE-014 ended at the departmental replan bound, which is the
// bound doing its job. What it wrote down about that was false in three ways
// at once: the failure was attributed to the model as
// model_result_contract_rejected, it was marked retryable, and the provider
// was paid twice more to rediscover a limit the host already knew.
//
// The cause was a callback answering two different questions. driveTypedTask's
// validate callback exists to ask "did the MODEL produce a valid result?", and
// everything it returns is recorded against the attempt and retried. The
// department review's callback also decided whether the host would GRANT
// another replan -- a decision about a perfectly valid answer -- so a governed
// refusal came out wearing the provider's name.
const replanReviewNeedsReplan = `{"schema_version":"department-review/v1","verdict":"needs_replan",` +
	`"findings":["one more pass is needed"],"unsatisfied_criteria":["coverage"],"evidence_refs":["task:1:context"],` +
	`"proposed_followup_tasks":[{"client_key":"followup-1","assigned_role_id":"ingenieria_ia/qa",` +
	`"task_class":"engineering.review","title":"Rework","instructions":"Rework the design.",` +
	`"acceptance_criteria":["reworked"],"dependencies":[],"requirements":[],"priority":50}]}`

func newReplanFixture(t *testing.T, maxReplans int, reviewBody string) *freezeFixture {
	t.Helper()
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	bodies := freezeBodies()
	bodies[PurposeDepartmentReview] = reviewBody
	harness := &scriptedHarness{models: models, tasks: tasksPort, bodies: bodies, adjudicationVerdict: "freeze"}

	leader := RoleRef{ID: "ingenieria_ia/orquestador", UnitID: "ingenieria_ia", Enabled: true, Executable: true, CanonicalLeader: true}
	workerRole := RoleRef{ID: "ingenieria_ia/qa", UnitID: "ingenieria_ia", Enabled: true, Executable: true}
	reviewer := RoleRef{ID: AdversarialReviewerRoleID, UnitID: "investigacion", Enabled: true, Executable: true}
	ceo := RoleRef{ID: CEORoleID, UnitID: "empresa", Enabled: true, Executable: true}

	limits := DefaultLimits()
	limits.MaxDepartmentReplans = maxReplans

	orchestrator, err := NewOrchestrator(Dependencies{
		Acceptance: newMemoryAcceptance(), OrganizationID: "explorarte",
		Registry: fakeRegistry{
			rev:     RevisionRef{ID: 7},
			units:   map[string]UnitRef{"ingenieria_ia": {ID: "ingenieria_ia", Operational: true, LeaderRoleID: leader.ID}},
			roles:   map[string]RoleRef{leader.ID: leader, workerRole.ID: workerRole, reviewer.ID: reviewer, ceo.ID: ceo},
			leaders: map[string]RoleRef{"ingenieria_ia": leader},
		},
		Tasks: tasksPort, Contexts: &fakeContexts{}, Assignments: fakeAssignments{},
		Principals: newFakePrincipals(), Models: models, Harness: harness,
		Budget: &countingBudget{}, Completion: &fakeCompletion{verdict: CompletionPass},
		Decisions: &fakeDecisionRecorder{}, Authorization: allowAuthz{}, Limits: limits,
		Clock: ClockFunc(func() time.Time { return time.Unix(1000, 0) }),
	})
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := orchestrator.Submit(context.Background(), SubmitRequest{
		ActorRoleID: OwnerRoleID, IdempotencyKey: "replan-bound",
		Goal: OwnerGoal{
			Goal:               "AUTONOMY-SMOKE: exercise the departmental replan bound.",
			AcceptanceCriteria: []AcceptanceCriterion{{Text: "design is reviewed", Phase: AcceptanceDesign}},
			Requirements: []RequirementProposal{
				{Key: designfreeze.RequirementKey, Type: "approval", Description: "Design frozen", Required: true},
			},
		},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	return &freezeFixture{orchestrator: orchestrator, tasks: tasksPort, harness: harness, root: run.RootTaskID}
}

// reviewInvocations counts how many times the provider was actually paid to
// review, which is the difference between a bound that reads an answer and a
// bound that keeps buying the same one.
func (f *freezeFixture) reviewInvocations() int {
	n := 0
	for _, purpose := range f.purposes() {
		if purpose == PurposeDepartmentReview {
			n++
		}
	}
	return n
}

// recordedFailureCodes returns what was actually written against attempts.
//
// It reads the fake's own record of RecordAttemptFailed rather than
// TaskRecord.Attempts, because that call is where the classification becomes
// durable and TaskRecord.Attempts is not what this fake populates. The
// property under test is what the system WRITES DOWN about a failure, so the
// measurement has to come from the same place the write does.
func (f *freezeFixture) recordedFailureCodes() []string {
	f.tasks.mu.Lock()
	defer f.tasks.mu.Unlock()
	return append([]string(nil), f.tasks.failed...)
}

func (f *freezeFixture) countRecorded(code string) int {
	n := 0
	for _, recorded := range f.recordedFailureCodes() {
		if recorded == code {
			n++
		}
	}
	return n
}

func (f *freezeFixture) contractRejections(t *testing.T) int {
	t.Helper()
	return f.countRecorded("model_result_contract_rejected")
}

// A: the bound is reached on a VALID needs_replan.
func TestTheReplanBoundStopsTheRunWithoutBlamingTheModel(t *testing.T) {
	fixture := newReplanFixture(t, 0, replanReviewNeedsReplan)
	fixture.drive(t)

	root := fixture.rootRecord(t)
	if root.Status != "blocked" {
		t.Fatalf("root is %q, want blocked", root.Status)
	}
	if root.ReasonCode != ReasonDepartmentReplansExhausted {
		t.Fatalf("reason_code=%q, want %q -- nothing failed, the host declined", root.ReasonCode, ReasonDepartmentReplansExhausted)
	}
	// F: the reason attributes the decision to the host, never to the provider.
	if !strings.Contains(root.Reason, "replan") || !strings.Contains(root.Reason, "valid") {
		t.Fatalf("the reason must say the review was valid and the host declined, got %q", root.Reason)
	}
	// The review it decided on is COMPLETED, not failed.
	var reviewStatus string
	for _, task := range fixture.tasks.tasks {
		if strings.Contains(task.IdempotencyKey, ":leader-review:") {
			reviewStatus = task.Status
		}
	}
	if reviewStatus != "completed" {
		t.Fatalf("the review is %q: a valid answer must not be recorded as a failure", reviewStatus)
	}
	if got := fixture.contractRejections(t); got != 0 {
		t.Fatalf("%d attempts blamed the model for a host budget decision", got)
	}
	// The economy: one review was bought, not three.
	if got := fixture.reviewInvocations(); got != 1 {
		t.Fatalf("the provider was paid %d times to review; the bound must read one answer, not re-buy it", got)
	}
	// No follow-up work was materialized past the bound.
	for _, task := range fixture.tasks.tasks {
		if strings.Contains(task.IdempotencyKey, "followup-1") {
			t.Fatal("no follow-up may be created once the bound is reached")
		}
	}
}

// B: the bound must NOT pre-empt the review. Even with no replans left, the
// department can still APPROVE the work it already has -- checking the limit
// before the call would throw that away.
func TestTheLastReviewCanStillApprove(t *testing.T) {
	fixture := newReplanFixture(t, 0, freezeBodies()[PurposeDepartmentReview]) // verdict: accept
	fixture.drive(t)

	root := fixture.rootRecord(t)
	if root.ReasonCode == ReasonDepartmentReplansExhausted {
		t.Fatal("an accepting review must not be refused by a bound on replans it never asked for")
	}
	if got := fixture.reviewInvocations(); got < 1 {
		t.Fatal("the review must still be bought: its verdict is unknown until it answers")
	}
}

// C: a malformed answer is still the model's problem, with its retry policy.
func TestAMalformedReviewIsStillAContractRejection(t *testing.T) {
	// Follow-up tasks under a non-replan verdict: the model's own output
	// contradicting itself, which no host policy can excuse.
	contradictory := strings.Replace(replanReviewNeedsReplan, `"verdict":"needs_replan"`, `"verdict":"accept"`, 1)
	fixture := newReplanFixture(t, 3, contradictory)
	// A contract rejection surfaces as an error from Resume, which is the
	// point: it is the model's problem and the caller hears about it. The
	// shared drive helper treats any such error as fatal, so this one runs
	// its own loop.
	for i := 0; i < 12; i++ {
		// Every error is tolerated here on purpose: what this test asserts
		// is what was WRITTEN DOWN against the attempt, not how the error
		// travelled back to the caller.
		run, _ := fixture.orchestrator.Resume(context.Background(), fixture.root)
		if run.State.Terminal() || run.State == StateBlocked {
			break
		}
	}

	if got := fixture.contractRejections(t); got == 0 {
		t.Fatal("a self-contradictory model result must still be recorded against the model")
	}
	if root := fixture.rootRecord(t); root.ReasonCode == ReasonDepartmentReplansExhausted {
		t.Fatal("a malformed answer is not a replan bound")
	}
}

// D: a stopped run stays stopped, and buys nothing more.
func TestTheReplanBoundIsNotReopened(t *testing.T) {
	fixture := newReplanFixture(t, 0, replanReviewNeedsReplan)
	fixture.drive(t)
	before := fixture.reviewInvocations()

	if _, err := fixture.orchestrator.Resume(context.Background(), fixture.root); !errors.Is(err, ErrRunBlocked) {
		t.Fatalf("a run stopped at its bound must stay stopped, got %v", err)
	}
	if after := fixture.reviewInvocations(); after != before {
		t.Fatalf("a second pass bought %d more reviews; a bound that re-purchases is not a bound", after-before)
	}
	if got := fixture.rootRecord(t).ReasonCode; got != ReasonDepartmentReplansExhausted {
		t.Fatalf("reason_code=%q after a second pass", got)
	}
}

// E: with budget remaining, nothing changes -- exactly one more replan.
func TestAReplanWithinTheBoundStillMaterializes(t *testing.T) {
	fixture := newReplanFixture(t, 1, replanReviewNeedsReplan)
	fixture.drive(t)

	followups := 0
	for _, task := range fixture.tasks.tasks {
		if strings.Contains(task.IdempotencyKey, "followup-1") {
			followups++
		}
	}
	if followups != 1 {
		t.Fatalf("expected exactly the next replan's work to be created, got %d", followups)
	}
	if got := fixture.contractRejections(t); got != 0 {
		t.Fatalf("a granted replan must not be recorded as a contract rejection, got %d", got)
	}
}

// The frontier this change draws, stated as its own property: a host budget
// decision and a model contract violation are different kinds of thing and
// must never be written down as the same one.
//
// Before this, MaxDepartmentReplans returned ErrBudgetExceeded from inside the
// validate callback, and every error from that callback became
// model_result_contract_rejected with retryable=true -- so a governed refusal
// wore the provider's name and was re-purchased twice.
func TestABudgetRefusalIsNeverRecordedAsAContractRejection(t *testing.T) {
	fixture := newReplanFixture(t, 0, replanReviewNeedsReplan)
	fixture.drive(t)

	for _, recorded := range fixture.recordedFailureCodes() {
		if recorded == "model_result_contract_rejected" {
			t.Fatalf("the replan bound was written down as %q; nothing about the model was wrong", recorded)
		}
	}
	// And the converse: the bound is not recorded against an attempt at all.
	// It is the host's decision about the run, so it lives on the root.
	if got := fixture.countRecorded(ReasonDepartmentReplansExhausted); got != 0 {
		t.Fatalf("the bound was recorded as an attempt failure %d times; it belongs on the root, not on the work", got)
	}
	if got := fixture.rootRecord(t).ReasonCode; got != ReasonDepartmentReplansExhausted {
		t.Fatalf("root reason_code=%q, want the bound", got)
	}
}
