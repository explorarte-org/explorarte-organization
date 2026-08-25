package executive

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
)

// The wiring under test is the contract's journey through a real campaign:
// obligations are loaded from durable state per round, retrieval searches
// their subjects before anything the goal's prose happens to contain, supply
// is checked before a worker is ever invoked, and the worker's artifact is
// checked structurally on the way back. AUTONOMY-SMOKE-017-R5 is the campaign
// this wiring exists to make impossible.

const (
	wiringDefRef  = "repository://explorarte-organization@" + targetSHA + "/internal/executive/types.go#L157-L165"
	wiringAppRef  = "repository://explorarte-organization@" + targetSHA + "/internal/executive/design_freeze_phase.go#L227-L236"
	replansAppRef = "repository://explorarte-organization@" + targetSHA + "/internal/executive/orchestrator.go#L792-L801"
	wiringBogus   = "repository://explorarte-organization@" + targetSHA + "/internal/executive/nowhere.go#L1-L2"
)

// Excerpts whose shape the host classifier can judge: a declaration opens a
// line with the symbol and no trailing colon; a use carries it mid-line.
const (
	wiringDecl = "\n// MaxDesignRounds bounds how many times a design may be sent back.\n" +
		"MaxDesignRounds int\n"
	wiringUse  = "\tif round > o.limits.MaxDesignRounds {\n\t\treturn Run{}\n\t}\n"
	replansUse = "replans := o.limits.MaxDepartmentReplans\n"
)

func wiringSource(reference, content string) SnapshotSource {
	return SnapshotSource{Kind: "repository_evidence", Reference: reference, Version: targetSHA, Included: true, Content: content}
}

// fullSupply is the world where every slot any of these goals demands has at
// least one citable excerpt in front of the worker.
func fullSupply() []SnapshotSource {
	return []SnapshotSource{
		wiringSource(wiringDefRef, wiringDecl),
		wiringSource(wiringAppRef, wiringUse),
		wiringSource(replansAppRef, replansUse),
	}
}

// partialSupply drops the definition excerpt entirely -- the R5 world, and
// the R6 ablation: the application site survives, the declaration does not.
func partialSupply() []SnapshotSource {
	return []SnapshotSource{wiringSource(wiringAppRef, wiringUse)}
}

type wiringFixture struct {
	*freezeFixture
	// Checkpoint E variation knobs: the ownership table a round-2 plan
	// states, the revision outcomes its review returns, that review's
	// verdict, and its proposed follow-ups.
	eOwnership     string
	eOutcomes      string
	eReviewVerdict string
	eFollowups     string
	// eFollowupOwnership declares, per open required change, which proposed
	// follow-up owns redoing it (checkpoint E1).
	eFollowupOwnership string
}

// newWiringFixture builds the freeze campaign WITH the two things the evidence
// contract needs to be live: a program target (so workers can see the
// repository at all) and a snapshot reader that answers what was shown.
func newWiringFixture(t *testing.T, verdict string, sources []SnapshotSource, goalReqs []EvidenceRequirementProposal, opts ...OrchestratorOption) *wiringFixture {
	t.Helper()
	tasksPort := newMemoryTasks()
	acceptance := newMemoryAcceptance()
	models := newFakeModels()
	harness := &scriptedHarness{models: models, tasks: tasksPort, bodies: freezeBodies(), adjudicationVerdict: verdict}
	ctxPort := &fakeContexts{}
	harness.contexts = ctxPort

	leader := RoleRef{ID: "ingenieria_ia/orquestador", UnitID: "ingenieria_ia", Enabled: true, Executable: true, CanonicalLeader: true}
	worker := RoleRef{ID: "ingenieria_ia/qa", UnitID: "ingenieria_ia", Enabled: true, Executable: true}
	reviewer := RoleRef{ID: AdversarialReviewerRoleID, UnitID: "investigacion", Enabled: true, Executable: true}
	ceo := RoleRef{ID: CEORoleID, UnitID: "empresa", Enabled: true, Executable: true}
	registry := fakeRegistry{
		rev:     RevisionRef{ID: 7},
		units:   map[string]UnitRef{"ingenieria_ia": {ID: "ingenieria_ia", Operational: true, LeaderRoleID: leader.ID}},
		roles:   map[string]RoleRef{leader.ID: leader, worker.ID: worker, reviewer.ID: reviewer, ceo.ID: ceo},
		leaders: map[string]RoleRef{"ingenieria_ia": leader},
	}
	options := append([]OrchestratorOption{
		WithMissionProvisioning(&fakeProgramTarget{sha: targetSHA}, newFakeMissionProvisioner()),
		WithSnapshotSources(stubSnapshotSources{sources: sources}),
	}, opts...)
	orchestrator, err := NewOrchestrator(Dependencies{Acceptance: acceptance,
		OrganizationID: "explorarte", Registry: registry, Tasks: tasksPort, Contexts: ctxPort,
		Assignments: fakeAssignments{}, Principals: newFakePrincipals(), Models: models, Harness: harness,
		Budget: &countingBudget{}, Completion: &fakeCompletion{verdict: CompletionPass},
		Decisions: &fakeDecisionRecorder{}, Authorization: allowAuthz{}, Limits: DefaultLimits(),
		Clock: ClockFunc(func() time.Time { return time.Unix(1000, 0) }),
	}, options...)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := orchestrator.Submit(context.Background(), SubmitRequest{
		ActorRoleID: OwnerRoleID, IdempotencyKey: "m2-1-evidence-wiring",
		Goal: OwnerGoal{
			Goal:                 "M2.1 -- design first, review adversarially, then freeze.",
			AcceptanceCriteria:   []AcceptanceCriterion{{Text: "Design before implementation", Phase: AcceptanceDesign}},
			EvidenceRequirements: goalReqs,
			Requirements: []RequirementProposal{{
				Key: designfreeze.RequirementKey, Type: "approval",
				Description: "Design frozen by executive adjudication", Required: true,
			}},
		},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	return &wiringFixture{freezeFixture: &freezeFixture{
		orchestrator: orchestrator, tasks: tasksPort, harness: harness,
		root: run.RootTaskID, acceptance: acceptance,
	}}
}

// driveUntilStopped resumes until the run blocks or terminates, passing
// contract rejections through: each one is an attempt failing durably so the
// engine can retry it, not a verdict about the run.
func (f *wiringFixture) driveUntilStopped(t *testing.T, maxPasses int) (Run, error) {
	t.Helper()
	var last Run
	var lastErr error
	for i := 0; i < maxPasses; i++ {
		run, err := f.orchestrator.Resume(context.Background(), f.root)
		last, lastErr = run, err
		switch {
		case err == nil:
		case errors.Is(err, ErrRunBlocked), errors.Is(err, ErrEvidenceInsufficient):
			return last, err
		case errors.Is(err, ErrModelResultContractRejected):
			// Keep driving; the next pass retries the closed attempt.
		default:
			t.Fatalf("resume pass %d: %v", i, err)
		}
		if run.State.Terminal() || run.State == StateBlocked {
			break
		}
	}
	return last, lastErr
}

func designWorkerTask(t *testing.T, f *wiringFixture) (TaskRecord, bool) {
	t.Helper()
	all, err := f.tasks.ListByCorrelation(context.Background(), f.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range all {
		if task.TaskClass == "engineering.design" {
			return task, true
		}
	}
	return TaskRecord{}, false
}

// ---------------------------------------------------------------- the wiring

// THE R6 CRITERION, end to end: take the definition out of what the world can
// supply for one limit, and NO worker invocation may exist afterwards. The
// host knew, from its own snapshot, that it could not put up what its own
// contract demands -- asking the model anything at all would have been buying
// an answer to a question the host had already decided was unfair.
func TestR6InsufficientSupplyStopsTheCampaignBeforeAnyWorkerInvocation(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", partialSupply(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition", "application"}},
	})
	run, err := fixture.driveUntilStopped(t, 12)

	if !errors.Is(err, ErrEvidenceInsufficient) {
		t.Fatalf("the run did not stop on host insufficiency: run=%+v err=%v", run, err)
	}
	if run.State != StateBlocked || run.ReasonCode != ReasonEvidenceInsufficient {
		t.Fatalf("run=%+v, want blocked with %s", run, ReasonEvidenceInsufficient)
	}
	for _, purpose := range fixture.purposes() {
		if purpose == PurposeDepartmentWorker {
			t.Fatal("a worker invocation was created despite unsuppliable evidence")
		}
	}
	// The preflight sits at the worker stage: planning still happened, because
	// guarding must not cost more than what it guards.
	if _, ok := fixture.commandFor(PurposeDepartmentPlan); !ok {
		t.Fatal("the preflight fired before department planning")
	}
	// Nothing about the design loop moved: no review ran, so no round was
	// consumed by a failure that was never the design's.
	all, listErr := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	if listErr != nil {
		t.Fatal(listErr)
	}
	for _, task := range all {
		if strings.Contains(task.IdempotencyKey, ":design-review:round:") {
			t.Fatalf("insufficient supply opened design work: %s", task.IdempotencyKey)
		}
	}
	// And the block stays closed: resuming must not walk into the same wall.
	before := len(fixture.purposes())
	if _, err := fixture.orchestrator.Resume(context.Background(), fixture.root); !errors.Is(err, ErrRunBlocked) {
		t.Fatalf("a supply-blocked run reopened itself on resume: %v", err)
	}
	if after := len(fixture.purposes()); after != before {
		t.Fatalf("resume executed %d model calls on a supply-blocked run", after-before)
	}
	// The attempt the host had already claimed and started must be closed
	// durably, by an explicit host transition -- never stranded RUNNING
	// behind the blocked root until its lease expired, and never recorded as
	// a provider or contract failure, because no provider was asked anything.
	task, ok := designWorkerTask(t, fixture)
	if !ok {
		t.Fatal("no department worker task exists")
	}
	if task.Status != "failed" {
		t.Fatalf("worker task status=%q, want failed with its attempt closed", task.Status)
	}
	if task.ReasonCode != "host_evidence_insufficient" {
		t.Fatalf("attempt closed as %q, want host_evidence_insufficient", task.ReasonCode)
	}
	if len(task.Attempts) != 1 {
		t.Fatalf("attempts=%d, want exactly the one the host opened and closed", len(task.Attempts))
	}
	if task.ActiveLease != nil {
		t.Fatal("the lease outlived the attempt it was issued to")
	}
	if invocations := fixture.harness.models.invocationCount(task.ID, task.Attempts[0].ID); invocations != 0 {
		t.Fatalf("%d model calls exist for an attempt that was closed before any call", invocations)
	}
}

// When the host CAN supply every slot, the worker runs, cites exactly what it
// was shown, and the campaign reaches its freeze. This is the ablation above
// with one file restored -- the whole difference is one excerpt.
func TestSuppliedEvidenceLetsTheWorkerCiteWhatItWasShownAndFreeze(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition", "application"}},
	})
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Grounded.",` +
			`"evidence_refs":["` + wiringDefRef + `","` + wiringAppRef + `"],` +
			`"evidence":[` +
			`{"claim":"the bound is declared here","subject":"MaxDesignRounds","relation":"definition","ref":"` + wiringDefRef + `"},` +
			`{"claim":"the bound is applied here","subject":"MaxDesignRounds","relation":"application","ref":"` + wiringAppRef + `"}]}`

	run, err := fixture.driveUntilStopped(t, 24)
	if err != nil {
		t.Fatalf("a fully supplied world failed: %v", err)
	}
	if run.State == StateBlocked {
		t.Fatalf("a fully supplied world blocked: %+v", run)
	}
	if _, ok := fixture.commandFor(PurposeDepartmentWorker); !ok {
		t.Fatal("the worker never ran")
	}
	if status := requirementStatus(fixture.rootRecord(t), designfreeze.RequirementKey); status != "satisfied" {
		t.Fatalf("design-freeze requirement status=%q", status)
	}
}

// A worker that cites something it was NEVER shown has broken its contract --
// but only when the slot WAS fillable. The failure must say the citation does
// not match what was offered, and must never say the host lacked the
// material, because this host did not.
func TestAnUnfilledSlotIsAContractRejectionNotAHostFailure(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition", "application"}},
	})
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Grounded.",` +
			`"evidence_refs":["` + wiringBogus + `"],` +
			`"evidence":[{"claim":"invented","subject":"MaxDesignRounds","relation":"definition","ref":"` + wiringBogus + `"}]}`

	sawRejection := false
	for i := 0; i < 10; i++ {
		_, err := fixture.orchestrator.Resume(context.Background(), fixture.root)
		if err != nil && errors.Is(err, ErrModelResultContractRejected) {
			sawRejection = true
			if strings.Contains(err.Error(), "nothing was supplied") {
				t.Fatalf("an unfilled slot was misreported as host insufficiency: %v", err)
			}
			if !strings.Contains(err.Error(), "evidence is missing") {
				t.Fatalf("the rejection does not name the unfilled slots: %v", err)
			}
		} else if err != nil && !errors.Is(err, ErrRunBlocked) {
			t.Fatalf("unexpected resume error: %v", err)
		}
		task, ok := designWorkerTask(t, fixture)
		if ok && task.Status == "failed" {
			break
		}
	}
	if !sawRejection {
		t.Fatal("no attempt was rejected for citing what was never supplied")
	}
	// Every attempt paid exactly once: the structural check lives on the
	// result path, after the single invocation an attempt is allowed. And the
	// durable record blames the citation, never the host.
	task, ok := designWorkerTask(t, fixture)
	if !ok {
		t.Fatal("no department worker task exists")
	}
	if len(fixture.tasks.failed) == 0 {
		t.Fatal("no failure was recorded")
	}
	for _, code := range fixture.tasks.failed {
		if code != "model_result_contract_rejected" {
			t.Fatalf("failure=%q recorded for a citation mismatch", code)
		}
	}
	if !strings.Contains(task.Reason, "evidence is missing") || strings.Contains(task.Reason, "nothing was supplied") {
		t.Fatalf("the durable rejection misattributes the failure: %q", task.Reason)
	}
	fixture.harness.mu.Lock()
	workerCommands := 0
	for _, command := range fixture.harness.commands {
		if command.Purpose == PurposeDepartmentWorker && command.TaskID == task.ID {
			workerCommands++
		}
	}
	fixture.harness.mu.Unlock()
	if workerCommands != len(task.Attempts) {
		t.Fatalf("worker invocations=%d do not match attempts=%d", workerCommands, len(task.Attempts))
	}
}

// An adjudicator restating what the owner already imposed must not become a
// second authority over the same slot. The novel obligation binds; the
// restatement is redundant.
func TestARestatedSlotKeepsItsOriginalAuthority(t *testing.T) {
	fixture := newWiringFixture(t, "revise", fullSupply(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition"}},
	})
	fixture.harness.adjudicationEvidence = `[` +
		`{"subject":"MaxDesignRounds","relations":["definition"]},` +
		`{"subject":"MaxDepartmentReplans","relations":["application"]}]`
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Grounded.",` +
			`"evidence_refs":["` + wiringDefRef + `","` + wiringAppRef + `","` + replansAppRef + `"],` +
			`"evidence":[` +
			`{"claim":"declared","subject":"MaxDesignRounds","relation":"definition","ref":"` + wiringDefRef + `"},` +
			`{"claim":"applied","subject":"MaxDesignRounds","relation":"application","ref":"` + wiringAppRef + `"},` +
			`{"claim":"replan bound applied","subject":"MaxDepartmentReplans","relation":"application","ref":"` + replansAppRef + `"}]}`

	fixture.driveUntilStopped(t, 24)

	rootID := fixture.rootRecord(t).ID
	required, err := fixture.orchestrator.evidenceRequirementsForRound(context.Background(), rootID, 2)
	if err != nil {
		t.Fatal(err)
	}
	type slotKey struct {
		subject  string
		relation string
	}
	authorities := map[slotKey][]EvidenceRequirementSource{}
	for _, requirement := range required {
		for _, relation := range requirement.Relations {
			key := slotKey{requirement.Subject, relation}
			authorities[key] = append(authorities[key], requirement.Source)
		}
	}
	if got := authorities[slotKey{"MaxDesignRounds", "definition"}]; len(got) != 1 || got[0] != EvidenceFromOwnerAcceptance {
		t.Fatalf("the owner's slot changed hands or split: %v", got)
	}
	if got := authorities[slotKey{"MaxDepartmentReplans", "application"}]; len(got) != 1 || got[0] != EvidenceFromAdjudication {
		t.Fatalf("the adjudicator's novel obligation did not bind alone: %v", got)
	}
}

// The obligation a revise names for round N+1 must be durable, seeded into
// retrieval, and visible to the preflight BEFORE any of that round's workers
// execute. Resume drives departments before the freeze phase, and
// activeDesignRound counts the successor as open as soon as the adjudication
// completed -- so adoption that waits for the freeze phase is too late by one
// whole department. This test observes the world from inside the worker's own
// execution, which is where "eventually durable" would already have failed.
func TestRoundTwoWorkerCannotRunBeforeItsObligationsAreDurable(t *testing.T) {
	fixture := newWiringFixture(t, "revise", fullSupply(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition"}},
	})
	fixture.harness.adjudicationEvidence = `[{"subject":"MaxDepartmentReplans","relations":["application"]}]`
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Grounded.",` +
			`"evidence_refs":["` + wiringDefRef + `","` + wiringAppRef + `","` + replansAppRef + `"],` +
			`"evidence":[` +
			`{"claim":"declared","subject":"MaxDesignRounds","relation":"definition","ref":"` + wiringDefRef + `"},` +
			`{"claim":"applied","subject":"MaxDesignRounds","relation":"application","ref":"` + wiringAppRef + `"},` +
			`{"claim":"replan bound applied","subject":"MaxDepartmentReplans","relation":"application","ref":"` + replansAppRef + `"}]}`

	fixture.driveUntilStopped(t, 24)

	if !fixture.harness.r2WorkerRan {
		t.Fatal("round 2 never ran a worker; the ordering question never came up")
	}
	if !fixture.harness.r2DurableBeforeRun {
		t.Fatal("the round-2 worker executed while its obligations were still missing from durable state")
	}
	want := map[string]bool{"MaxDepartmentReplans": false, "MaxDesignRounds": false}
	for _, subject := range fixture.harness.r2SeededSubjects {
		if _, obligated := want[subject]; obligated {
			want[subject] = true
		}
	}
	for subject, seeded := range want {
		if !seeded {
			t.Fatalf("retrieval for the round-2 worker was not seeded with %s: %v", subject, fixture.harness.r2SeededSubjects)
		}
	}
}

// Resuming re-drives the freeze phase many times, and adoption sits on that
// path. The decision must be recorded exactly once per round: the engine
// appends evidence rows without uniqueness, so the guard -- not the store --
// is the idempotency.
func TestAResumedRunAdoptsTheSameDecisionOnce(t *testing.T) {
	fixture := newWiringFixture(t, "revise", fullSupply(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition"}},
	})
	fixture.harness.adjudicationEvidence = `[{"subject":"MaxDepartmentReplans","relations":["application"]}]`
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Grounded.",` +
			`"evidence_refs":["` + wiringDefRef + `","` + wiringAppRef + `","` + replansAppRef + `"],` +
			`"evidence":[` +
			`{"claim":"declared","subject":"MaxDesignRounds","relation":"definition","ref":"` + wiringDefRef + `"},` +
			`{"claim":"applied","subject":"MaxDesignRounds","relation":"application","ref":"` + wiringAppRef + `"},` +
			`{"claim":"replan bound applied","subject":"MaxDepartmentReplans","relation":"application","ref":"` + replansAppRef + `"}]}`

	fixture.driveUntilStopped(t, 24)

	rootID := fixture.rootRecord(t).ID
	counts := map[string]int{}
	for _, row := range fixture.tasks.evidence {
		if strings.HasPrefix(row.Reference, EvidenceRequirementsReference) {
			counts[row.Reference]++
		}
	}
	for reference, count := range counts {
		if count != 1 {
			t.Fatalf("obligations for %s were recorded %d times", reference, count)
		}
	}
	if counts[evidenceRequirementsReference(rootID, 2)] != 1 {
		t.Fatalf("round 2 obligations missing or duplicated: %v", counts)
	}
}

// A restatement padded with whitespace is still a restatement. Validation
// accepts outer whitespace and adoption trims it, so the dedup must compare
// canonical subjects -- otherwise " MaxDesignRounds " walks past the guard
// and the same slot ends up under two authorities.
func TestARestatedSlotInNonCanonicalFormIsStillRedundant(t *testing.T) {
	inForce := AdoptEvidenceRequirements([]EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition"}},
	}, EvidenceFromOwnerAcceptance)
	novel := withoutSlotsAlreadyInForce([]EvidenceRequirementProposal{
		{Subject: " MaxDesignRounds ", Relations: []string{"definition"}},
		{Subject: "\tMaxDepartmentReplans", Relations: []string{"application", "definition"}},
	}, inForce)
	if len(novel) != 1 {
		t.Fatalf("padded restatements were not deduplicated: %+v", novel)
	}
	if novel[0].Subject != "MaxDepartmentReplans" {
		t.Fatalf("the surviving proposal kept the padded form: %q", novel[0].Subject)
	}
	if len(novel[0].Relations) != 2 {
		t.Fatalf("novel relations were dropped: %v", novel[0].Relations)
	}
}

// Only a revise proposes obligations through the adoption path. A verdict that
// opens no round must not bind one, even if its body carries proposals: the
// parser refuses the combination outright, and the adoption reader
// independently returns nothing for it.
func TestOnlyAReviseProposesThroughAdoption(t *testing.T) {
	freeze := []byte(`{"schema_version":"design-adjudication/v1","verdict":"freeze",` +
		`"evidence_requirements":[{"subject":"MaxDesignRounds","relations":["definition"]}]}`)
	proposals, err := adjudicationEvidenceRequirementsOf(freeze)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 0 {
		t.Fatalf("a freeze proposed obligations: %+v", proposals)
	}
	revise := []byte(`{"schema_version":"design-adjudication/v1","verdict":"revise",` +
		`"evidence_requirements":[{"subject":"MaxDesignRounds","relations":["definition"]}]}`)
	proposals, err = adjudicationEvidenceRequirementsOf(revise)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 1 || proposals[0].Subject != "MaxDesignRounds" {
		t.Fatalf("a revise's proposals were lost: %+v", proposals)
	}
}

// Retrieval seeds: subjects go first, deduplicated, blank-free, sorted;
// whatever retrieval would have found on its own comes after them. What must
// be grounded outranks what the goal happened to also say.
func TestEvidenceSubjectsAreCanonicalAndFirst(t *testing.T) {
	subjects := evidenceSubjects([]EvidenceRequirement{
		{Subject: "MaxDesignRounds", Relations: []string{"definition"}},
		{Subject: "MaxDesignRounds", Relations: []string{"application"}, Source: EvidenceFromAdjudication},
		{Subject: "  ", Relations: []string{"context"}},
		{Subject: "AlphaChannel", Relations: []string{"test"}},
	})
	want := []string{"AlphaChannel", "MaxDesignRounds"}
	if len(subjects) != len(want) {
		t.Fatalf("subjects=%v, want %v", subjects, want)
	}
	for i := range want {
		if subjects[i] != want[i] {
			t.Fatalf("subjects=%v, want %v", subjects, want)
		}
	}
}
