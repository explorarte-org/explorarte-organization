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
	// adjudicationEvidence is the raw JSON array of evidence_requirements the
	// adjudicator proposes alongside a revise. Empty means the body carries
	// none, which is what every pre-existing test exercises.
	adjudicationEvidence string
	// adjudicationRequiredChanges overrides the revise body's
	// required_changes when non-empty, so tests can pin how free-form change
	// prescriptions interact with the evidence-requirements contract.
	adjudicationRequiredChanges []string

	// Round-aware contract hooks (checkpoint E): when set, they replace the
	// static body for that purpose, receiving the task being driven -- its
	// key carries the design round, the department and the replan ordinal,
	// which is everything a body needs to vary per scenario. Returning an
	// empty string falls back to the static body.
	departmentPlanBody   func(task TaskRecord) string
	departmentWorkerBody func(task TaskRecord) string
	departmentReviewBody func(task TaskRecord) string

	// adjudicationVerdictByRound overrides adjudicationVerdict for specific
	// design rounds (round 1 revises, round 2 freezes, ...). Absent rounds
	// keep the static verdict.
	adjudicationVerdictByRound map[int]string

	// adjudicationRequiredChangesByRound overrides the demanded changes for
	// a specific round, so a campaign can revise twice with different
	// demands (RC:1:x from round 1, RC:2:x from round 2, ...). Absent
	// rounds keep the static list.
	adjudicationRequiredChangesByRound map[int][]string

	// contexts, when set, lets the harness answer what retrieval was seeded
	// with for each executed command.
	contexts *fakeContexts

	// Round-2 worker observation: what the world looked like AT THE MOMENT
	// the round's first worker executed -- not after the round advanced.
	r2WorkerRan        bool
	r2DurableBeforeRun bool
	r2SeededSubjects   []string
}

func (h *scriptedHarness) Execute(_ context.Context, command HarnessRunCommand) (HarnessRunOutcome, error) {
	h.mu.Lock()
	h.commands = append(h.commands, command)
	if h.observeRoundTwo(command) {
		h.r2WorkerRan = true
		h.r2DurableBeforeRun = h.roundTwoObligationsRecorded()
		if h.contexts != nil {
			h.r2SeededSubjects = h.contexts.subjectsFor(command.Context.ID)
		}
	}
	h.mu.Unlock()

	body := h.bodies[command.Purpose]
	switch command.Purpose {
	case PurposeDepartmentPlan:
		if h.departmentPlanBody != nil {
			if task, taskErr := h.tasks.GetTask(context.Background(), command.TaskID); taskErr == nil {
				if dynamic := h.departmentPlanBody(task); dynamic != "" {
					body = dynamic
				}
			}
		}
	case PurposeDepartmentWorker:
		if h.departmentWorkerBody != nil {
			if task, taskErr := h.tasks.GetTask(context.Background(), command.TaskID); taskErr == nil {
				if dynamic := h.departmentWorkerBody(task); dynamic != "" {
					body = dynamic
				}
			}
		}
	case PurposeDepartmentReview:
		if h.departmentReviewBody != nil {
			if task, taskErr := h.tasks.GetTask(context.Background(), command.TaskID); taskErr == nil {
				if dynamic := h.departmentReviewBody(task); dynamic != "" {
					body = dynamic
				}
			}
		}
	}
	if command.Purpose == PurposeDesignAdjudication {
		body = h.adjudicationBody(command.TaskID)
	}
	if body == "" {
		return HarnessRunOutcome{}, errors.New("no scripted body for purpose " + string(command.Purpose))
	}
	invocation := h.models.recordDurableInvocation(command, "succeeded", json.RawMessage(body), 0)
	return HarnessRunOutcome{Status: HarnessRunSucceeded, FinalOutput: body, InvocationID: invocation.ID}, nil
}

// observeRoundTwo reports whether this command is a round-2 department worker
// execution, which is exactly the moment an obligation adopted "too late"
// would still be missing.
func (h *scriptedHarness) observeRoundTwo(command HarnessRunCommand) bool {
	if command.Purpose != PurposeDepartmentWorker || h.contexts == nil {
		return false
	}
	task, err := h.tasks.GetTask(context.Background(), command.TaskID)
	if err != nil {
		return false
	}
	return designRoundOf(task.IdempotencyKey) == 2 && !h.r2WorkerRan
}

// roundTwoObligationsRecorded reads durable evidence as it stands RIGHT NOW:
// has any row been recorded under a round-2 obligations reference?
func (h *scriptedHarness) roundTwoObligationsRecorded() bool {
	for _, row := range h.tasks.evidence {
		if strings.HasPrefix(row.Reference, EvidenceRequirementsReference) && strings.HasSuffix(row.Reference, "/round/2") {
			return true
		}
	}
	return false
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
	round := eAdjudicationRoundOf(task)
	if v, ok := h.adjudicationVerdictByRound[round]; ok {
		verdict = v
	}
	required := "[]"
	if verdict == "revise" {
		changes := h.adjudicationRequiredChanges
		if perRound, ok := h.adjudicationRequiredChangesByRound[round]; ok {
			changes = perRound
		}
		if len(changes) > 0 {
			encoded, err := json.Marshal(changes)
			if err != nil {
				return ""
			}
			required = string(encoded)
		} else {
			required = `["Prove the seal protocol under concurrency."]`
		}
	}
	evidence := ""
	if h.adjudicationEvidence != "" && verdict == "revise" {
		evidence = `"evidence_requirements":` + h.adjudicationEvidence + `,`
	}
	return `{"schema_version":"design-adjudication/v1","verdict":"` + verdict + `",` +
		`"accepted_findings":["AR-001"],"rejected_findings":[],"required_changes":` + required + `,` +
		evidence +
		`"unresolved_owner_decisions":[],"design_id":"` + design.ID + `","design_version":"` + design.Version + `",` +
		`"design_digest":"` + digest + `","evidence_refs":[]}`
}

// eAdjudicationRoundOf recovers the design round an adjudication task was
// opened for, from its key (design-adjudication:round:N).
func eAdjudicationRoundOf(task TaskRecord) int {
	marker := "design-adjudication:round:"
	index := strings.LastIndex(task.IdempotencyKey, marker)
	if index < 0 {
		return 1
	}
	rest := task.IdempotencyKey[index+len(marker):]
	if end := strings.Index(rest, ":"); end >= 0 {
		rest = rest[:end]
	}
	n := 0
	for _, r := range rest {
		if r < '0' || r > '9' {
			return 1
		}
		n = n*10 + int(r-'0')
	}
	if n < 1 {
		return 1
	}
	return n
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
		// The department must actually produce something. A plan with no
		// tasks models a department that delivers nothing, and for a long
		// time that was why nobody noticed the candidate design was the
		// leader's review verdict: with no deliverable, the verdict was
		// the only durable text there was.
		PurposeDepartmentPlan: `{"schema_version":"department-plan/v1","department_id":"ingenieria_ia",` +
			`"tasks":[{"client_key":"design-1","assigned_role_id":"ingenieria_ia/qa","task_class":"engineering.design",` +
			`"title":"Draft the candidate design","instructions":"Write the design for M2.1.",` +
			`"acceptance_criteria":["names what changes"],"dependencies":[],"requirements":[],"priority":50}],` +
			`"review_criteria":["design is complete"],"unresolved":[]}`,
		PurposeDepartmentWorker: `{"schema_version":"worker-result/v1",` +
			`"summary":"M2.1 seals the design before implementation and names every file it will touch.",` +
			`"evidence_refs":[]}`,
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
	// acceptance belongs to the fixture, not the orchestrator, because the
	// real one is a table. A restart test that gave the new process its own
	// empty recorder would be modelling a store that forgets, which is the
	// opposite of the property under test.
	acceptance *memoryAcceptance
}

func newFreezeFixture(t *testing.T, adjudicationVerdict string, reviewerEnabled bool) *freezeFixture {
	t.Helper()
	tasksPort := newMemoryTasks()
	acceptance := newMemoryAcceptance()
	models := newFakeModels()
	harness := &scriptedHarness{models: models, tasks: tasksPort, bodies: freezeBodies(), adjudicationVerdict: adjudicationVerdict}

	leader := RoleRef{ID: "ingenieria_ia/orquestador", UnitID: "ingenieria_ia", Enabled: true, Executable: true, CanonicalLeader: true}
	// A worker distinct from the leader, because the deliverable and the
	// verdict about it come from different roles. Collapsing them is how
	// the two got confused for as long as they did.
	worker := RoleRef{ID: "ingenieria_ia/qa", UnitID: "ingenieria_ia", Enabled: true, Executable: true}
	reviewer := RoleRef{ID: AdversarialReviewerRoleID, UnitID: "investigacion", Enabled: reviewerEnabled, Executable: reviewerEnabled}
	ceo := RoleRef{ID: CEORoleID, UnitID: "empresa", Enabled: true, Executable: true}
	registry := fakeRegistry{
		rev:     RevisionRef{ID: 7},
		units:   map[string]UnitRef{"ingenieria_ia": {ID: "ingenieria_ia", Operational: true, LeaderRoleID: leader.ID}},
		roles:   map[string]RoleRef{leader.ID: leader, worker.ID: worker, reviewer.ID: reviewer, ceo.ID: ceo},
		leaders: map[string]RoleRef{"ingenieria_ia": leader},
	}
	orchestrator, err := NewOrchestrator(Dependencies{Acceptance: acceptance,
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
			AcceptanceCriteria: []AcceptanceCriterion{{Text: "Design before implementation", Phase: AcceptanceDesign}},
			Requirements: []RequirementProposal{{
				Key: designfreeze.RequirementKey, Type: "result",
				Description: "Design frozen by executive adjudication", Required: true,
			}},
		},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	return &freezeFixture{orchestrator: orchestrator, tasks: tasksPort, harness: harness, root: run.RootTaskID, acceptance: acceptance}
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
		candidate := &fixture.tasks.evidence[i]
		if candidate.Type != "result" {
			continue
		}
		if _, ok := candidate.Metadata["design_freeze_record"]; ok {
			frozen = candidate
	}
	}
	if frozen == nil {
		t.Fatal("no result evidence was recorded for the freeze")
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
// REVISE opens the next design round instead of stopping the run.
//
// A design sent back for changes used to block and wait for a human, which
// made a correct judgement the end of the work: the adjudicator could say
// "this must change" and nothing changed. The round that follows is a
// successor, not a revision -- round N keeps its keys, its deliverables and
// its verdict exactly as they were judged.
func TestReviseOpensTheNextRoundAndNeverReopensTheLast(t *testing.T) {
	fixture := newFreezeFixture(t, "revise", true)
	fixture.drive(t)
	all, err := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}

	// Round 1 ran and was judged.
	for _, key := range []string{"design-review:round:1", "design-adjudication:round:1"} {
		task, ok := findTaskByKey(all, childKey(fixture.root, key))
		if !ok || task.Status != "completed" {
			t.Fatalf("round 1 is incomplete: %s", key)
		}
	}
	// Round 2 exists, with its own department work and its own review.
	for _, key := range []string{
		"leader-plan:ingenieria_ia:design-round:2",
		"leader-review:ingenieria_ia:design-round:2",
		"design-review:round:2",
		"design-adjudication:round:2",
	} {
		if _, ok := findTaskByKey(all, childKey(fixture.root, key)); !ok {
			t.Fatalf("the revision round did not produce %s", key)
		}
	}
	// And exactly one successor: a revise opens round N+1, never N+2.
	if _, ok := findTaskByKey(all, childKey(fixture.root, "design-review:round:3")); ok {
		t.Fatal("a single revise opened more than one round")
	}
	// Round 1's own tasks were never re-keyed or reused for round 2's work.
	if reviewOne, ok := findTaskByKey(all, childKey(fixture.root, "leader-review:ingenieria_ia")); !ok || reviewOne.Status != "completed" {
		t.Fatal("round 1's department review was disturbed by the revision")
	}
}

// What the adjudicator demanded is what the next round is planned against.
// Planning it from the original goal would produce the same design again and
// spend the reviewer's budget re-deciding something already decided.
func TestTheRequiredChangesReachTheNextRound(t *testing.T) {
	fixture := newFreezeFixture(t, "revise", true)
	fixture.drive(t)
	all, err := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	plan, ok := findTaskByKey(all, childKey(fixture.root, "leader-plan:ingenieria_ia:design-round:2"))
	if !ok {
		t.Fatal("round 2 has no planning task")
	}
	if !strings.Contains(plan.Instructions, "Prove the seal protocol under concurrency.") {
		t.Fatalf("the required change never reached the planner: %q", plan.Instructions)
	}
	if !strings.Contains(plan.Instructions, "REQUIRED CHANGES") {
		t.Error("the planner must be told these are the required changes, not fresh objectives")
	}
}

// The loop is bounded. A design sent back the allowed number of times and
// still unsettled is waiting on a human, not on another round.
func TestTheRevisionLoopStopsAtItsBound(t *testing.T) {
	fixture := newFreezeFixture(t, "revise", true)
	run := fixture.drive(t)
	if run.State != StateBlocked || run.ReasonCode != ReasonDesignRoundsExhausted {
		t.Fatalf("the loop must stop at its bound, got %+v", run)
	}
	all, err := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	rounds := 0
	for _, task := range all {
		if strings.Contains(task.IdempotencyKey, ":design-review:round:") {
			rounds++
		}
	}
	if rounds > DefaultLimits().MaxDesignRounds {
		t.Fatalf("ran %d rounds against a bound of %d", rounds, DefaultLimits().MaxDesignRounds)
	}
	if status := requirementStatus(fixture.rootRecord(t), designfreeze.RequirementKey); status == "satisfied" {
		t.Fatal("an exhausted revision loop satisfied the design freeze")
	}
	if _, closed := fixture.commandFor(PurposeCEOClosure); closed {
		t.Fatal("the CEO closed a run whose design never settled")
	}
}

// A blocked run still stays blocked: the bound stops the loop, and after it
// nothing re-runs the reviewer.
func TestAnExhaustedRunDoesNotAutoUnblock(t *testing.T) {
	fixture := newFreezeFixture(t, "revise", true)
	fixture.drive(t)
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
		roles:   map[string]RoleRef{leader.ID: leader, RoleRef{ID: "ingenieria_ia/qa"}.ID: {ID: "ingenieria_ia/qa", UnitID: "ingenieria_ia", Enabled: true, Executable: true}},
		leaders: map[string]RoleRef{"ingenieria_ia": leader},
	}
	orchestrator, err := NewOrchestrator(Dependencies{Acceptance: newMemoryAcceptance(),
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
		Goal: OwnerGoal{Goal: "Analyze one area.", AcceptanceCriteria: []AcceptanceCriterion{{Text: "done", Phase: AcceptanceDesign}}},
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
		// A department producing a design is what this phase is FOR. The
		// guard is about implementation work leaking in early -- missions,
		// promotions, staging -- and the needle check below is what
		// actually enforces that. A design deliverable is the opposite of
		// the thing being guarded against.
		"engineering.design": {},
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
