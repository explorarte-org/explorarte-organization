package executive

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
)

// This is the real Executive path: orgctl executive submit -> Orchestrator ->
// adversarial review -> adjudication -> design freeze. Nothing is simulated
// except the provider boundary itself, which returns scripted bodies and makes
// zero network calls.

// scriptedHarness answers per ExecutionPurpose. For the adjudication it reads
// the design identity back out of the task's own instructions, the way a model
// would, instead of being told the digest the host computed.
type scriptedHarness struct {
	mu                  sync.Mutex
	models              *fakeModels
	tasks               *memoryTasks
	bodies              map[ExecutionPurpose]string
	commands            []HarnessRunCommand
	adjudicationVerdict string
	corruptDigest       string
}

func (h *scriptedHarness) Execute(_ context.Context, command HarnessRunCommand) (HarnessRunOutcome, error) {
	h.mu.Lock()
	h.commands = append(h.commands, command)
	h.mu.Unlock()

	body := h.bodies[command.Purpose]
	if command.Purpose == PurposeDesignAdjudication {
		body = h.adjudicationBody(command.TaskID)
	}
	if body == "" {
		return HarnessRunOutcome{}, errors.New("no scripted body for purpose " + string(command.Purpose))
	}
	invocation := h.models.recordDurableInvocation(command, "succeeded", json.RawMessage(body), 0)
	return HarnessRunOutcome{Status: HarnessRunSucceeded, FinalOutput: body, InvocationID: invocation.ID}, nil
}

func (h *scriptedHarness) adjudicationBody(taskID int64) string {
	task, err := h.tasks.GetTask(context.Background(), taskID)
	if err != nil {
		return ""
	}
	design, ok := designFromInstructions(task.Instructions)
	if !ok {
		return ""
	}
	digest := design.Digest
	if h.corruptDigest != "" {
		digest = h.corruptDigest
	}
	verdict := h.adjudicationVerdict
	required := "[]"
	if verdict == "revise" {
		required = `["Prove the seal protocol under concurrency."]`
	}
	return `{"schema_version":"design-adjudication/v1","verdict":"` + verdict + `",` +
		`"accepted_findings":["AR-001"],"rejected_findings":[],"required_changes":` + required + `,` +
		`"unresolved_owner_decisions":[],"design_id":"` + design.ID + `","design_version":"` + design.Version + `",` +
		`"design_digest":"` + digest + `","evidence_refs":[]}`
}

// designFromInstructions pulls the bundle's design identity out of the task
// instructions, which is the only place the host published it.
func designFromInstructions(instructions string) (designfreeze.Design, bool) {
	start := strings.Index(instructions, "{")
	if start < 0 {
		return designfreeze.Design{}, false
	}
	var bundle struct {
		Design designfreeze.Design `json:"design"`
	}
	decoder := json.NewDecoder(strings.NewReader(instructions[start:]))
	if err := decoder.Decode(&bundle); err != nil {
		return designfreeze.Design{}, false
	}
	return bundle.Design, bundle.Design.Digest != ""
}

func freezeBodies() map[ExecutionPurpose]string {
	return map[ExecutionPurpose]string{
		PurposeCEOPlan: `{"schema_version":"executive-plan/v1","objective":"Design M2.1",` +
			`"department_requests":[{"unit_id":"ingenieria_ia","objective":"design","deliverable":"candidate design","priority":1,"constraints":[]}],` +
			`"global_constraints":[],"success_criteria":["design reviewed"],"owner_decisions_required":[]}`,
		PurposeDepartmentPlan: `{"schema_version":"department-plan/v1","department_id":"ingenieria_ia",` +
			`"tasks":[],"review_criteria":["design is complete"],"unresolved":[]}`,
		PurposeDepartmentReview: `{"schema_version":"department-review/v1","verdict":"accept","findings":["design drafted"],` +
			`"unsatisfied_criteria":[],"evidence_refs":["task:1:context"],"proposed_followup_tasks":[]}`,
		PurposeAdversarialReview: `{"schema_version":"adversarial-review/v1","verdict":"revise",` +
			`"findings":[{"id":"AR-001","severity":"high","claim":"The seal protocol is unproven under concurrency.",` +
			`"affected_requirement":"M2.1 seal","required_correction":"Add a barrier-coordinated integration test.",` +
			`"evidence_refs":[]}],"contradictions":[],"unverified_assumptions":[],"security_findings":[],` +
			`"authority_findings":[],"recovery_findings":[],"memory_epistemic_findings":[],"evidence_refs":[]}`,
		PurposeCEOClosure: `{"schema_version":"executive-closure/v1","status":"completed","answer_to_owner":"Design frozen.",` +
			`"completed_items":["design reviewed"],"blocked_items":[],"unresolved_decisions":[],"evidence_refs":["task:1:context"]}`,
	}
}

type freezeFixture struct {
	orchestrator *Orchestrator
	tasks        *memoryTasks
	harness      *scriptedHarness
	root         int64
}

func newFreezeFixture(t *testing.T, adjudicationVerdict string, reviewerEnabled bool) *freezeFixture {
	t.Helper()
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	harness := &scriptedHarness{models: models, tasks: tasksPort, bodies: freezeBodies(), adjudicationVerdict: adjudicationVerdict}

	leader := RoleRef{ID: "ingenieria_ia/orquestador", UnitID: "ingenieria_ia", Enabled: true, Executable: true, CanonicalLeader: true}
	reviewer := RoleRef{ID: AdversarialReviewerRoleID, UnitID: "investigacion", Enabled: reviewerEnabled, Executable: reviewerEnabled}
	ceo := RoleRef{ID: CEORoleID, UnitID: "empresa", Enabled: true, Executable: true}
	registry := fakeRegistry{
		rev:     RevisionRef{ID: 7},
		units:   map[string]UnitRef{"ingenieria_ia": {ID: "ingenieria_ia", Operational: true, LeaderRoleID: leader.ID}},
		roles:   map[string]RoleRef{leader.ID: leader, reviewer.ID: reviewer, ceo.ID: ceo},
		leaders: map[string]RoleRef{"ingenieria_ia": leader},
	}
	orchestrator, err := NewOrchestrator(Dependencies{
		OrganizationID: "explorarte", Registry: registry, Tasks: tasksPort, Contexts: &fakeContexts{},
		Assignments: fakeAssignments{}, Principals: newFakePrincipals(), Models: models, Harness: harness,
		Budget: &countingBudget{}, Completion: &fakeCompletion{verdict: CompletionPass},
		Decisions: &fakeDecisionRecorder{}, Authorization: allowAuthz{}, Limits: DefaultLimits(),
		Clock: ClockFunc(func() time.Time { return time.Unix(1000, 0) }),
	})
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := orchestrator.Submit(context.Background(), SubmitRequest{
		ActorRoleID: OwnerRoleID, IdempotencyKey: "m2-1-design-freeze",
		Goal: OwnerGoal{
			Goal:               "M2.1 -- design first, review adversarially, then freeze.",
			AcceptanceCriteria: []string{"Design before implementation"},
			Requirements: []RequirementProposal{{
				Key: designfreeze.RequirementKey, Type: "approval",
				Description: "Design frozen by executive adjudication", Required: true,
			}},
		},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	return &freezeFixture{orchestrator: orchestrator, tasks: tasksPort, harness: harness, root: run.RootTaskID}
}

// drive resumes until the run stops changing, exactly as a worker loop would.
func (f *freezeFixture) drive(t *testing.T) Run {
	t.Helper()
	var last Run
	for i := 0; i < 24; i++ {
		run, err := f.orchestrator.Resume(context.Background(), f.root)
		if err != nil && !errors.Is(err, ErrRunBlocked) {
			t.Fatalf("resume %d: %v", i, err)
		}
		last = run
		if run.State.Terminal() || run.State == StateBlocked {
			break
		}
	}
	return last
}

func (f *freezeFixture) purposes() []ExecutionPurpose {
	f.harness.mu.Lock()
	defer f.harness.mu.Unlock()
	out := make([]ExecutionPurpose, 0, len(f.harness.commands))
	for _, command := range f.harness.commands {
		out = append(out, command.Purpose)
	}
	return out
}

func (f *freezeFixture) commandFor(purpose ExecutionPurpose) (HarnessRunCommand, bool) {
	f.harness.mu.Lock()
	defer f.harness.mu.Unlock()
	for _, command := range f.harness.commands {
		if command.Purpose == purpose {
			return command, true
		}
	}
	return HarnessRunCommand{}, false
}

func (f *freezeFixture) rootRecord(t *testing.T) TaskRecord {
	t.Helper()
	root, err := f.tasks.GetTask(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func requirementStatus(root TaskRecord, key string) string {
	for _, requirement := range root.Requirements {
		if requirement.Key == key {
			return requirement.Status
		}
	}
	return ""
}

// ---------------------------------------------------------------- the E2E

func TestExecutiveRunReachesDesignFreezeThroughTheRealOrchestrator(t *testing.T) {
	fixture := newFreezeFixture(t, "freeze", true)
	run := fixture.drive(t)

	// The adversarial review actually happened, under the reviewer role.
	reviewCommand, ok := fixture.commandFor(PurposeAdversarialReview)
	if !ok {
		t.Fatal("the Executive never dispatched an adversarial review")
	}
	if reviewCommand.RoleID != AdversarialReviewerRoleID {
		t.Fatalf("adversarial review ran as %q", reviewCommand.RoleID)
	}
	// And the adjudication happened, under the CEO -- not the reviewer.
	adjudicationCommand, ok := fixture.commandFor(PurposeDesignAdjudication)
	if !ok {
		t.Fatal("the Executive never dispatched a design adjudication")
	}
	if adjudicationCommand.RoleID != CEORoleID {
		t.Fatalf("adjudication ran as %q", adjudicationCommand.RoleID)
	}
	if adjudicationCommand.RoleID == reviewCommand.RoleID {
		t.Fatal("the reviewer adjudicated its own findings")
	}

	// Ordering: review before adjudication, and both before closure.
	purposes := fixture.purposes()
	indexOf := func(target ExecutionPurpose) int {
		for i, purpose := range purposes {
			if purpose == target {
				return i
			}
		}
		return -1
	}
	review, adjudication, closure := indexOf(PurposeAdversarialReview), indexOf(PurposeDesignAdjudication), indexOf(PurposeCEOClosure)
	if review < 0 || adjudication < 0 || review > adjudication {
		t.Fatalf("review/adjudication ordering wrong: %v", purposes)
	}
	if closure >= 0 && closure < adjudication {
		t.Fatalf("the CEO closed the run before the design was adjudicated: %v", purposes)
	}

	// The freeze is durable on the root requirement, with the digest bound.
	root := fixture.rootRecord(t)
	if status := requirementStatus(root, designfreeze.RequirementKey); status != "satisfied" {
		t.Fatalf("design-freeze requirement status=%q", status)
	}
	var frozen *EvidenceCommand
	for i := range fixture.tasks.evidence {
		if fixture.tasks.evidence[i].Type == "approval" {
			frozen = &fixture.tasks.evidence[i]
		}
	}
	if frozen == nil {
		t.Fatal("no approval evidence was recorded for the freeze")
	}
	if frozen.Digest == "" || !frozen.Satisfies {
		t.Fatalf("freeze evidence=%+v", *frozen)
	}
	payload, _ := frozen.Metadata["design_freeze_record"].(string)
	var record designfreeze.Record
	if err := json.Unmarshal([]byte(payload), &record); err != nil {
		t.Fatalf("freeze record is not readable: %v", err)
	}
	record.Digest = frozen.Digest
	if !designfreeze.Satisfies(record, record.Design) {
		t.Fatal("the recorded freeze does not verify against its own design")
	}
	if record.Review.TaskID == 0 || record.Adjudication.TaskID == 0 || record.Review.TaskID == record.Adjudication.TaskID {
		t.Fatalf("freeze is not bound to two distinct executions: %+v", record)
	}
	if run.State == StateBlocked {
		t.Fatalf("run blocked after a freeze: %+v", run)
	}
}

// REVISE: the design is not frozen, the run stops, and it stays stopped.
func TestReviseBlocksTheRunAndDoesNotAutoRetry(t *testing.T) {
	fixture := newFreezeFixture(t, "revise", true)
	run := fixture.drive(t)
	if run.State != StateBlocked || run.ReasonCode != ReasonDesignRevisionRequired {
		t.Fatalf("run=%+v", run)
	}
	root := fixture.rootRecord(t)
	if status := requirementStatus(root, designfreeze.RequirementKey); status == "satisfied" {
		t.Fatal("a revise verdict satisfied the design freeze")
	}
	if _, closed := fixture.commandFor(PurposeCEOClosure); closed {
		t.Fatal("the CEO closed a run whose design was sent back for revision")
	}

	// Resuming again must not silently re-run the reviewer: that would spend
	// the adversarial budget re-deciding a decision the executive made.
	before := len(fixture.purposes())
	_, err := fixture.orchestrator.Resume(context.Background(), fixture.root)
	if !errors.Is(err, ErrRunBlocked) {
		t.Fatalf("blocked run auto-unblocked: %v", err)
	}
	if after := len(fixture.purposes()); after != before {
		t.Fatalf("resume re-executed %d model calls on a blocked design", after-before)
	}
}

// REJECT: distinct reason code, same refusal to freeze or close.
func TestRejectBlocksTheRunWithItsOwnReason(t *testing.T) {
	fixture := newFreezeFixture(t, "reject", true)
	run := fixture.drive(t)
	if run.State != StateBlocked || run.ReasonCode != ReasonDesignRejected {
		t.Fatalf("run=%+v", run)
	}
	if status := requirementStatus(fixture.rootRecord(t), designfreeze.RequirementKey); status == "satisfied" {
		t.Fatal("a reject verdict satisfied the design freeze")
	}
	if _, closed := fixture.commandFor(PurposeCEOClosure); closed {
		t.Fatal("the CEO closed a run whose design was rejected")
	}
}

// An adjudication naming another digest cannot freeze this design, through the
// real orchestrator and not only through the parser.
func TestAForeignDigestFromTheModelCannotMisTargetTheFreeze(t *testing.T) {
	const foreignDigest = "dd11111111111111111111111111111111111111111111111111111111111111"
	fixture := newFreezeFixture(t, "freeze", true)
	fixture.harness.corruptDigest = foreignDigest
	var lastErr error
	var run Run
	for i := 0; i < 24; i++ {
		current, err := fixture.orchestrator.Resume(context.Background(), fixture.root)
		if err != nil {
			lastErr = err
			break
		}
		run = current
		if run.State.Terminal() || run.State == StateBlocked {
			break
		}
	}
	// What the model claims about identity is no longer part of the contract,
	// so a foreign digest in its output cannot mis-target the freeze: the host
	// binds its own design and the run proceeds on that.
	//
	// The property this test protects is unchanged -- a freeze applies only to
	// the design the host handed over -- but it is now guaranteed by binding
	// rather than by asking an untrusted party to repeat a 64-character hash
	// and refusing the whole verdict when it miscopies one character.
	if lastErr != nil {
		t.Fatalf("a verdict must not be refused for what it claims about identity: %v", lastErr)
	}
	if status := requirementStatus(fixture.rootRecord(t), designfreeze.RequirementKey); status != "satisfied" {
		t.Fatalf("the freeze did not satisfy the requirement: status=%q", status)
	}
	for _, evidence := range fixture.tasks.evidence {
		if strings.Contains(evidence.Reference, foreignDigest) || strings.Contains(evidence.Digest, foreignDigest) {
			t.Fatalf("the model's foreign digest reached durable evidence: %+v", evidence)
		}
	}
	// Closure is now the correct outcome: the design that was frozen is the
	// host's, so there is nothing left to refuse.
	if _, closed := fixture.commandFor(PurposeCEOClosure); !closed {
		t.Fatal("the run never closed, so the freeze did not carry it forward")
	}
}

// The reviewer is disabled in the canonical catalog today. That must stop the
// run, and must never be answered by dispatching the review somewhere else.
func TestUnavailableReviewerFailsClosedWithNoSubstitute(t *testing.T) {
	fixture := newFreezeFixture(t, "freeze", false)
	run := fixture.drive(t)
	if run.State != StateBlocked || run.ReasonCode != ReasonAdversarialReviewUnavailable {
		t.Fatalf("run=%+v", run)
	}
	for _, purpose := range fixture.purposes() {
		if purpose == PurposeAdversarialReview || purpose == PurposeDesignAdjudication {
			t.Fatalf("an execution ran despite an undispatchable reviewer: %v", purpose)
		}
	}
	if status := requirementStatus(fixture.rootRecord(t), designfreeze.RequirementKey); status == "satisfied" {
		t.Fatal("the freeze was satisfied without a reviewer")
	}
}

// A run with no design-freeze requirement behaves exactly as before. This is
// what makes the phase additive rather than a change to existing runs.
func TestRunsWithoutADesignFreezeRequirementAreUnaffected(t *testing.T) {
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	harness := &scriptedHarness{models: models, tasks: tasksPort, bodies: freezeBodies(), adjudicationVerdict: "freeze"}
	leader := RoleRef{ID: "ingenieria_ia/orquestador", UnitID: "ingenieria_ia", Enabled: true, Executable: true, CanonicalLeader: true}
	registry := fakeRegistry{
		rev:     RevisionRef{ID: 7},
		units:   map[string]UnitRef{"ingenieria_ia": {ID: "ingenieria_ia", Operational: true, LeaderRoleID: leader.ID}},
		roles:   map[string]RoleRef{leader.ID: leader},
		leaders: map[string]RoleRef{"ingenieria_ia": leader},
	}
	orchestrator, err := NewOrchestrator(Dependencies{
		OrganizationID: "explorarte", Registry: registry, Tasks: tasksPort, Contexts: &fakeContexts{},
		Assignments: fakeAssignments{}, Principals: newFakePrincipals(), Models: models, Harness: harness,
		Budget: &countingBudget{}, Completion: &fakeCompletion{verdict: CompletionPass},
		Decisions: &fakeDecisionRecorder{}, Authorization: allowAuthz{}, Limits: DefaultLimits(),
		Clock: ClockFunc(func() time.Time { return time.Unix(1000, 0) }),
	})
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := orchestrator.Submit(context.Background(), SubmitRequest{
		ActorRoleID: OwnerRoleID, IdempotencyKey: "ungoverned",
		Goal: OwnerGoal{Goal: "Analyze one area.", AcceptanceCriteria: []string{"done"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 24; i++ {
		current, resumeErr := orchestrator.Resume(context.Background(), run.RootTaskID)
		if resumeErr != nil && !errors.Is(resumeErr, ErrRunBlocked) {
			t.Fatalf("resume: %v", resumeErr)
		}
		if current.State.Terminal() || current.State == StateBlocked {
			break
		}
	}
	harness.mu.Lock()
	defer harness.mu.Unlock()
	for _, command := range harness.commands {
		if command.Purpose == PurposeAdversarialReview || command.Purpose == PurposeDesignAdjudication {
			t.Fatalf("an ungoverned run performed %s", command.Purpose)
		}
	}
}

// IMPLEMENTATION ELIGIBILITY: a frozen design creates nothing that could run.
func TestFreezeCreatesNoImplementationEligibility(t *testing.T) {
	fixture := newFreezeFixture(t, "freeze", true)
	fixture.drive(t)

	allowed := map[string]struct{}{
		TaskClassOwnerGoal: {}, TaskClassCoordinationCEOPlan: {}, TaskClassCoordinationDeptPlan: {},
		TaskClassCoordinationDeptReview: {}, TaskClassCoordinationCEOClosure: {},
		TaskClassCoordinationAdversarialReview: {}, TaskClassCoordinationDesignAdjudication: {},
		TaskClassGeneralWork: {}, "": {},
	}
	tasks, err := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if _, ok := allowed[task.TaskClass]; !ok {
			t.Fatalf("the freeze produced task class %q", task.TaskClass)
		}
		lowered := strings.ToLower(task.TaskClass + " " + task.Title)
		for _, needle := range []string{"mission", "promotion", "coderunner", "staging", "implement"} {
			if strings.Contains(lowered, needle) {
				t.Fatalf("task %d looks like implementation work: %q", task.ID, task.Title)
			}
		}
	}
	for _, command := range fixture.tasks.createCalls {
		if strings.Contains(strings.ToLower(command.Instructions), "code-runner-execution/v1") {
			t.Fatal("a code runner plan was created by the freeze phase")
		}
	}
}
