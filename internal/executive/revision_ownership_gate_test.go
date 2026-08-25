package executive

import (
	"context"
	"strings"
	"testing"
)

// Checkpoint E -- Revision Ownership + Department Consistency Gate, pinned
// against R16's exact death: two parallel workers resolved the same central
// question with opposite claims, and the department review voted accept over
// internally contradictory deliverables. Under E that shape cannot reach the
// adversarial reviewer: the plan must bind every required change to exactly one owner, and
// the review must answer per change whether the deliverables collectively
// resolved it -- accept is only representable when they all say resolved.
//
// The guards drive a full round-1 -> revise -> round-2 campaign whose
// adjudication demands exactly two changes (RC:1:1 granularity, RC:1:2
// round-advancement grounding), then vary ONLY what checkpoint E governs:
// the ownership table and the revision outcomes.

const (
	eOwnerKey   = "resolve_ar"
	eSupportKey = "verify_claims"
)

func eWorld() *probeWorldSource {
	return &probeWorldSource{worlds: map[string]map[string]string{targetSHA: {
		// Owner-goal symbols: joint admission prices the CUMULATIVE
		// contract, so the world must supply what round 1 already grounded.
		"internal/executive/types.go": `package executive

type Limits struct {
	MaxDesignRounds int
}
`,
		"internal/executive/budget.go": `package executive

func check(l Limits) bool {
	return l.MaxDesignRounds > 0
}
`,
		"internal/executive/freeze.go": `package executive

func (o *Orchestrator) driveDesignFreeze(ctx context.Context) (bool, error) {
	return false, nil
}
`,
		"internal/executive/orchestrator.go": `package executive

func step(o *Orchestrator) {
	done, err := o.driveDesignFreeze(context.Background())
	_ = done
	_ = err
}
`,
	}}}
}

func eSources() []SnapshotSource {
	freezeApp := "repository://explorarte-organization@" + targetSHA + "/internal/executive/orchestrator.go#L1-L9"
	return append(fullSupply(), SnapshotSource{Kind: "repository_evidence", Reference: freezeApp,
		Version: targetSHA, Included: true,
		Content: "\nfunc step(o *Orchestrator) {\n\tdone, err := o.driveDesignFreeze(context.Background())\n\t_ = done\n\t_ = err\n}\n"})
}

func eWorkerBody() string {
	freezeApp := "repository://explorarte-organization@" + targetSHA + "/internal/executive/orchestrator.go#L1-L9"
	items := []string{
		`{"claim":"declared","subject":"MaxDesignRounds","relation":"definition","ref":"` + wiringDefRef + `"}`,
		`{"claim":"applied","subject":"MaxDesignRounds","relation":"application","ref":"` + wiringAppRef + `"}`,
		`{"claim":"applied","subject":"driveDesignFreeze","relation":"application","ref":"` + freezeApp + `"}`,
	}
	refs := []string{wiringDefRef, wiringAppRef, freezeApp}
	return `{"schema_version":"worker-result/v2","summary":"Grounded.",` +
		`"evidence_refs":[` + refsJSON(refs) + `],"evidence":[` + strings.Join(items, ",") + `]}`
}

// eFixture builds the campaign through round-1 adjudication (two required
// changes, jointly admitted against eWorld) and installs round-aware v2
// contracts for whatever the guard varies.
func eFixture(t *testing.T) *wiringFixture {
	t.Helper()
	fixture := newWiringFixture(t, "revise", eSources(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition"}},
	}, WithRepositoryEvidenceSource("explorarte-organization", eWorld()))
	fixture.harness.bodies[PurposeDepartmentWorker] = eWorkerBody()
	fixture.harness.adjudicationRequiredChanges = []string{
		"Clarify MaxDepartmentReplans granularity: aggregate ceiling or per-department cap.",
		"Ground the revise-to-next-round transition in activeDesignRound citations.",
	}
	fixture.harness.adjudicationEvidence =
		`[{"subject":"driveDesignFreeze","relations":["application"]}]`

	v2Plan := func(ownership string) string {
		return `{"schema_version":"department-plan/v2","department_id":"ingenieria_ia",` +
			`"tasks":[` +
			`{"client_key":"` + eOwnerKey + `","assigned_role_id":"ingenieria_ia/qa","task_class":"engineering.review",` +
			`"title":"Resolve required changes","instructions":"Address the assigned required changes.","acceptance_criteria":["Cite the pin"],"dependencies":[]},` +
			`{"client_key":"` + eSupportKey + `","assigned_role_id":"ingenieria_ia/qa","task_class":"qa.testing",` +
			`"title":"Verify claims","instructions":"Verify.","acceptance_criteria":["Check"],"dependencies":[]}` +
			`],"review_criteria":["Consistent"],"unresolved":[],"revision_ownership":[` + ownership + `]}`
	}
	fixture.harness.departmentPlanBody = func(round int) string {
		if round < 2 {
			return v2Plan("")
		}
		return v2Plan(fixture.eOwnership)
	}
	fixture.harness.departmentReviewBody = func(round int) string {
		// Round 1 has no ownership table to answer: fall back to the plain
		// accept body. The needs_replan/consistency variations belong to the
		// round whose adjudication created required changes.
		if round < 2 {
			return ""
		}
		outcomes := "[]"
		if fixture.eOutcomes != "" {
			outcomes = fixture.eOutcomes
		}
		followupOwnership := "[]"
		if fixture.eFollowupOwnership != "" {
			followupOwnership = fixture.eFollowupOwnership
		}
		return `{"schema_version":"department-review/v2","verdict":"` + fixture.eReviewVerdict + `",` +
			`"findings":["reviewed"],"unsatisfied_criteria":[],"evidence_refs":[],` +
			`"proposed_followup_tasks":` + fixture.eFollowups + `,` +
			`"followup_ownership":` + followupOwnership + `,` +
			`"revision_outcomes":` + outcomes + `}`
	}
	fixture.eReviewVerdict = "accept"
	fixture.eFollowups = "[]"
	return fixture
}

func eDepartmentReviewTask(t *testing.T, f *wiringFixture) (TaskRecord, bool) {
	t.Helper()
	all, err := f.tasks.ListByCorrelation(context.Background(), f.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	var latest TaskRecord
	found := false
	for _, task := range all {
		if task.TaskClass == TaskClassCoordinationDeptReview && (!found || task.ID > latest.ID) {
			latest, found = task, true
		}
	}
	return latest, found
}

func ownerEntry(id, key string) string {
	return `{"required_change_id":"` + id + `","owner_client_key":"` + key + `"}`
}

func eRoundTwoWorkersExist(t *testing.T, f *wiringFixture) bool {
	t.Helper()
	all, err := f.tasks.ListByCorrelation(context.Background(), f.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, task := range all {
		if strings.HasPrefix(task.IdempotencyKey, "executive:"+itoa64(f.rootRecord(t).ID)+":worker:ingenieria_ia:design-round:2:") {
			found = true
		}
	}
	return found
}

func itoa64(v int64) string {
	digits := ""
	negative := v < 0
	if negative {
		v = -v
	}
	for v > 0 {
		digits = string(rune('0'+v%10)) + digits
		v /= 10
	}
	if negative {
		digits = "-" + digits
	}
	if digits == "" {
		digits = "0"
	}
	return digits
}

// GUARD: two owners for one required change are refused at plan-contract
// time -- before any worker of the round exists. This is R16's exact wound.
func TestEDuplicateOwnersForOneChangeAreRefusedBeforeWorkers(t *testing.T) {
	fixture := eFixture(t)
	fixture.eOwnership =
		ownerEntry("RC:1:1", eOwnerKey) + "," +
			ownerEntry("RC:1:1", eSupportKey) + "," +
			ownerEntry("RC:1:2", eOwnerKey)

	driveCapability(t, fixture, 30)

	sawRejection := false
	for _, code := range fixture.tasks.failed {
		if code == "model_result_contract_rejected" {
			sawRejection = true
		}
	}
	if !sawRejection {
		t.Fatal("dual ownership never hit the plan contract")
	}
	if eRoundTwoWorkersExist(t, fixture) {
		t.Fatal("workers were materialized beside a dual-owned required change")
	}
	allTasks, _ := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	for _, task := range allTasks {
		if strings.Contains(task.IdempotencyKey, "design-round:2") &&
			strings.Contains(task.Reason, "two owners") {
			return // the specific refusal is on the record
		}
	}
	t.Fatal("the two-owner refusal was not recorded")
}

// GUARD: an unowned required change and an invented id are both refused; a
// worker owning TWO changes plus an unowned support task is legal, and the
// clean accept path continues to the adversarial reviewer unchanged.
func TestEOwnershipCoverageAndLegalShapes(t *testing.T) {
	t.Run("unowned change refused", func(t *testing.T) {
		fixture := eFixture(t)
		fixture.eOwnership = ownerEntry("RC:1:2", eOwnerKey) // RC:1:1 left unowned
		driveCapability(t, fixture, 30)
		allTasks, _ := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
		for _, task := range allTasks {
			if strings.Contains(task.Reason, "without exactly one owner") &&
				strings.Contains(task.Reason, "RC:1:1") {
				return
			}
		}
		t.Fatal("the unowned-change refusal was not recorded")
	})

	t.Run("invented id refused", func(t *testing.T) {
		fixture := eFixture(t)
		fixture.eOwnership = ownerEntry("RC:1:1", eOwnerKey) + "," + ownerEntry("RC:9:9", eOwnerKey)
		driveCapability(t, fixture, 30)
		allTasks, _ := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
		for _, task := range allTasks {
			if strings.Contains(task.Reason, "not a required change of this design round") {
				return
			}
		}
		t.Fatal("an invented id was not refused")
	})

	t.Run("one worker may own several changes and support tasks own nothing", func(t *testing.T) {
		fixture := eFixture(t)
		fixture.eOwnership = ownerEntry("RC:1:1", eOwnerKey) + "," + ownerEntry("RC:1:2", eOwnerKey)
		fixture.eReviewVerdict = "needs_replan"
		fixture.eOutcomes = `[` +
			`{"required_change_id":"RC:1:1","status":"conflicted","canonical_resolution":"","conflicting_task_refs":["task:a","task:b"]},` +
			`{"required_change_id":"RC:1:2","status":"resolved","canonical_resolution":"cited","conflicting_task_refs":[]}]`
		fixture.eFollowups = `[{"client_key":"fix_up","assigned_role_id":"ingenieria_ia/qa","task_class":"engineering.review",` +
			`"title":"Redo","instructions":"Redo the resolution.","acceptance_criteria":["Cite"],"dependencies":[]}]`
		fixture.eFollowupOwnership = `[{"required_change_id":"RC:1:1","owner_client_key":"fix_up"}]`
		driveCapability(t, fixture, 30)
		if !eRoundTwoWorkersExist(t, fixture) {
			allTasks, _ := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
			for _, task := range allTasks {
				t.Logf("task %d %s %s %.70s", task.ID, task.Status, task.TaskClass, task.IdempotencyKey)
			}
			t.Fatal("the legal multi-ownership plan never materialized its workers")
		}
	})
}

func eOwnerRef() string { return "900001" }

// GUARD: the reviewer sees the ownership table and the consistency rider
// before answering.
func TestEReviewerSeesOwnershipTableAndConsistencyRule(t *testing.T) {
	fixture := eFixture(t)
	fixture.eOwnership = ownerEntry("RC:1:1", eOwnerKey) + "," + ownerEntry("RC:1:2", eOwnerKey)
	fixture.eOutcomes = `[` +
		`{"required_change_id":"RC:1:1","status":"resolved","canonical_resolution":"aggregate ceiling derived from per-department allowance","conflicting_task_refs":[]},` +
		`{"required_change_id":"RC:1:2","status":"resolved","canonical_resolution":"activeDesignRound transition cited","conflicting_task_refs":[]}]`
	driveCapability(t, fixture, 24)

	command, ok := fixture.commandFor(PurposeDepartmentReview)
	if !ok {
		t.Fatal("no department review ran")
	}
	reviewTask, ok := eDepartmentReviewTask(t, fixture)
	if !ok {
		t.Fatal("no department review task exists")
	}
	if !strings.Contains(reviewTask.Instructions, "REVISION OWNERSHIP TABLE") ||
		!strings.Contains(reviewTask.Instructions, "RC:1:1") || !strings.Contains(reviewTask.Instructions, "RC:1:2") {
		t.Fatalf("review instructions missing the ownership table:\n%s", reviewTask.Instructions)
	}
	if !strings.Contains(command.ExecutionContract, departmentConsistencyGuidance) {
		t.Fatalf("review ExecutionContract missing the consistency rule:\n%s", command.ExecutionContract)
	}
	// And retrieval stayed exactly what obligations dictate: the guidance is
	// execution-time text only.
	if strings.Contains(command.Context.Content, departmentConsistencyGuidance) {
		t.Fatal("the consistency rule leaked into the context snapshot content")
	}
}

// GUARDS: accept + conflicted, and accept + unresolved, are both contract
// rejections; conflicted under needs_replan routes to the DEPARTMENT replan
// bound without opening a third design round.
func TestEAcceptGateAndNeedsReplanRouting(t *testing.T) {
	t.Run("accept with a conflicted outcome is refused", func(t *testing.T) {
		fixture := eFixture(t)
		fixture.eOwnership = ownerEntry("RC:1:1", eOwnerKey) + "," + ownerEntry("RC:1:2", eOwnerKey)
		fixture.eOutcomes = `[` +
			`{"required_change_id":"RC:1:1","status":"conflicted","canonical_resolution":"","conflicting_task_refs":["task:1"]},` +
			`{"required_change_id":"RC:1:2","status":"resolved","canonical_resolution":"cited","conflicting_task_refs":[]}]`
		driveCapability(t, fixture, 30)
		saw := false
		for _, code := range fixture.tasks.failed {
			if code == "model_result_contract_rejected" {
				saw = true
			}
		}
		if !saw {
			t.Fatal("accept over a conflicted outcome was never refused")
		}
	})

	t.Run("accept with an unresolved outcome is refused", func(t *testing.T) {
		fixture := eFixture(t)
		fixture.eOwnership = ownerEntry("RC:1:1", eOwnerKey) + "," + ownerEntry("RC:1:2", eOwnerKey)
		fixture.eOutcomes = `[` +
			`{"required_change_id":"RC:1:1","status":"resolved","canonical_resolution":"stated","conflicting_task_refs":[]},` +
			`{"required_change_id":"RC:1:2","status":"unresolved","canonical_resolution":"nobody addressed it","conflicting_task_refs":[]}]`
		driveCapability(t, fixture, 30)
		saw := false
		for _, code := range fixture.tasks.failed {
			if code == "model_result_contract_rejected" {
				saw = true
			}
		}
		if !saw {
			t.Fatal("accept over an unresolved outcome was never refused")
		}
	})

	t.Run("contradiction routes to the department replan bound", func(t *testing.T) {
		fixture := eFixture(t)
		fixture.eOwnership = ownerEntry("RC:1:1", eOwnerKey) + "," + ownerEntry("RC:1:2", eOwnerKey)
		fixture.eReviewVerdict = "needs_replan"
		fixture.eOutcomes = `[` +
			`{"required_change_id":"RC:1:1","status":"conflicted","canonical_resolution":"","conflicting_task_refs":["task:a","task:b"]},` +
			`{"required_change_id":"RC:1:2","status":"resolved","canonical_resolution":"cited","conflicting_task_refs":[]}]`
		fixture.eFollowups = `[{"client_key":"reconcile_mdr","assigned_role_id":"ingenieria_ia/qa","task_class":"engineering.review",` +
			`"title":"Reconcile MDR granularity","instructions":"One falsifiable claim.","acceptance_criteria":["Cite"],"dependencies":[]}]`
		fixture.eFollowupOwnership = `[{"required_change_id":"RC:1:1","owner_client_key":"reconcile_mdr"}]`

		driveCapability(t, fixture, 30)

		root := fixture.rootRecord(t)
		if root.ReasonCode == ReasonDesignRoundsExhausted {
			t.Fatal("a department contradiction consumed a DESIGN round")
		}
		allTasks, _ := fixture.tasks.ListByCorrelation(context.Background(), root.CorrelationID)
		replanReviewExists := false
		for _, task := range allTasks {
			if strings.Contains(task.IdempotencyKey, ":replan:1") {
				replanReviewExists = true
			}
			if strings.Contains(task.IdempotencyKey, "design-round:3") {
				t.Fatal("a third design round opened: MaxDesignRounds was touched by a department event")
			}
		}
		if !replanReviewExists {
			t.Fatal("needs_replan did not open the department replan iteration")
		}
	})
}
