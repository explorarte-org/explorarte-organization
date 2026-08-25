package executive

import (
	"context"
	"encoding/json"
	"fmt"
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
	fixture.harness.departmentPlanBody = func(task TaskRecord) string {
		if designRoundOf(task.IdempotencyKey) < 2 {
			return v2Plan("")
		}
		return v2Plan(fixture.eOwnership)
	}
	fixture.harness.departmentReviewBody = func(task TaskRecord) string {
		// Round 1 has no ownership table to answer: fall back to the plain
		// accept body. The needs_replan/consistency variations belong to the
		// round whose adjudication created required changes.
		if designRoundOf(task.IdempotencyKey) < 2 {
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

// eTaskUnit recovers the department a plan or review task belongs to, from
// its key (leader-plan:<unit>... or leader-review:<unit>...).
func eTaskUnit(task TaskRecord) string {
	for _, marker := range []string{"leader-plan:", "leader-review:"} {
		if index := strings.Index(task.IdempotencyKey, marker); index >= 0 {
			rest := task.IdempotencyKey[index+len(marker):]
			if end := strings.Index(rest, ":"); end >= 0 {
				rest = rest[:end]
			}
			return rest
		}
	}
	return ""
}

// eReplanReviewBody is the accept body a replan review returns once the redo
// settled everything: every assigned change resolved, nothing to redo.
func eReplanReviewBody(ids []string) string {
	outcomes := ""
	for _, id := range ids {
		if outcomes != "" {
			outcomes += ","
		}
		outcomes += `{"required_change_id":"` + id + `","status":"resolved","canonical_resolution":"settled by the redo","conflicting_task_refs":[]}`
	}
	return `{"schema_version":"department-review/v2","verdict":"accept",` +
		`"findings":["redo verified"],"unsatisfied_criteria":[],"evidence_refs":[],` +
		`"proposed_followup_tasks":[],"followup_ownership":[],"revision_outcomes":[` + outcomes + `]}`
}

// newTwoUnitEFixture drives eFixture's campaign across TWO departments
// (diseno and ingenieria_ia). Both plan and work round 1; the round-1
// adjudication demands exactly the given changes; the round-2 adjudication
// freezes. claimsByUnit maps each unit to the 1-based ordinals of the
// required changes its round-2 sheet CLAIMS -- the departments decide their
// own share and the host only judges the union -- and the scripted bodies
// answer within them. A unit absent from the map claims nothing, so its
// round-2 plan proposes no tasks at all.
func newTwoUnitEFixture(t *testing.T, requiredChanges []string, claimsByUnit map[string][]int) (*wiringFixture, map[string][]RequiredChange) {
	t.Helper()
	fixture := newMultiUnitWiringFixture(t, "revise", eSources(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition"}},
	}, []string{"diseno"}, WithRepositoryEvidenceSource("explorarte-organization", eWorld()))
	h := fixture.harness
	h.bodies[PurposeDepartmentWorker] = eWorkerBody()
	h.adjudicationRequiredChanges = requiredChanges
	h.adjudicationEvidence = `[{"subject":"driveDesignFreeze","relations":["application"]}]`
	h.adjudicationVerdictByRound = map[int]string{2: "freeze"}

	changes := make([]RequiredChange, 0, len(requiredChanges))
	for index, text := range requiredChanges {
		changes = append(changes, RequiredChange{ID: requiredChangeID(1, index+1), Text: text})
	}
	scopes := map[string][]RequiredChange{"diseno": nil, "ingenieria_ia": nil}
	for unit, ordinals := range claimsByUnit {
		for _, ordinal := range ordinals {
			if ordinal < 1 || ordinal > len(changes) {
				t.Fatalf("fixture misused: %s claims ordinal %d of %d changes", unit, ordinal, len(changes))
			}
			scopes[unit] = append(scopes[unit], changes[ordinal-1])
		}
	}

	v2Plan := func(unit string, claims []RequiredChange, round int) string {
		items := ""
		if round < 2 || len(claims) > 0 {
			items =
				`{"client_key":"` + unit + `_owner","assigned_role_id":"` + unit + `/qa","task_class":"engineering.review",` +
					`"title":"Resolve required changes","instructions":"Address the assigned required changes.","acceptance_criteria":["Cite the pin"],"dependencies":[]},` +
					`{"client_key":"` + unit + `_support","assigned_role_id":"` + unit + `/qa","task_class":"qa.testing",` +
					`"title":"Verify claims","instructions":"Verify.","acceptance_criteria":["Check"],"dependencies":[]}`
		}
		ownership := ""
		for _, change := range claims {
			if ownership != "" {
				ownership += ","
			}
			ownership += ownerEntry(change.ID, unit+"_owner")
		}
		return `{"schema_version":"department-plan/v2","department_id":"` + unit + `",` +
			`"tasks":[` + items + `],"review_criteria":["Consistent"],"unresolved":[],` +
			`"revision_ownership":[` + ownership + `]}`
	}
	h.departmentPlanBody = func(task TaskRecord) string {
		unit := eTaskUnit(task)
		round := designRoundOf(task.IdempotencyKey)
		if round < 2 || unit == "" {
			return v2Plan(unit, nil, round)
		}
		return v2Plan(unit, scopes[unit], round)
	}
	h.departmentReviewBody = func(task TaskRecord) string {
		unit := eTaskUnit(task)
		if designRoundOf(task.IdempotencyKey) < 2 || unit == "" {
			return ""
		}
		outcomes := ""
		for _, change := range scopes[unit] {
			if outcomes != "" {
				outcomes += ","
			}
			outcomes += `{"required_change_id":"` + change.ID + `","status":"resolved","canonical_resolution":"settled with citation","conflicting_task_refs":[]}`
		}
		return `{"schema_version":"department-review/v2","verdict":"accept",` +
			`"findings":["reviewed"],"unsatisfied_criteria":[],"evidence_refs":[],` +
			`"proposed_followup_tasks":[],"followup_ownership":[],"revision_outcomes":[` + outcomes + `]}`
	}
	return fixture, scopes
}

// GUARD: two departments claiming the SAME required change are refused at
// the second completing sheet -- the refusal names the department that
// already holds it -- and no worker of the round is ever born beside an
// inexact partition.
func TestECrossDepartmentDuplicateClaimIsRefusedBeforeWorkers(t *testing.T) {
	fixture, _ := newTwoUnitEFixture(t, []string{
		"Clarify MaxDepartmentReplans granularity: aggregate ceiling or per-department cap.",
		"Ground the revise-to-next-round transition in activeDesignRound citations.",
	}, map[string][]int{"diseno": {1}, "ingenieria_ia": {1}})

	driveCapability(t, fixture, 48)

	sawRefusal := false
	for _, code := range fixture.tasks.failed {
		if code == "model_result_contract_rejected" {
			sawRefusal = true
		}
	}
	if !sawRefusal {
		t.Fatal("a duplicate cross-department claim was never refused")
	}
	root := fixture.rootRecord(t)
	allTasks, err := fixture.tasks.ListByCorrelation(context.Background(), root.CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	namedHolder := false
	for _, task := range allTasks {
		if strings.Contains(task.Reason, "already claimed by") &&
			(strings.Contains(task.Reason, "diseno") || strings.Contains(task.Reason, "ingenieria_ia")) {
			namedHolder = true
		}
		prefix := "executive:" + itoa64(root.ID) + ":worker:"
		if strings.HasPrefix(task.IdempotencyKey, prefix) && strings.Contains(task.IdempotencyKey, "design-round:2") {
			t.Fatalf("round-2 workers were materialized beside a duplicated claim: %s", task.IdempotencyKey)
		}
	}
	if !namedHolder {
		t.Fatal("the duplicate refusal did not name the department already holding the change")
	}
}

// GUARD: when every department has planned and required changes remain
// claimed by NOBODY, the last completing sheet is refused with the measured
// list -- and nothing of the round materializes over a hole.
func TestECoverageGapIsRefusedAtTheLastPlan(t *testing.T) {
	fixture, _ := newTwoUnitEFixture(t, []string{
		"Clarify MaxDepartmentReplans granularity: aggregate ceiling or per-department cap.",
		"Ground the revise-to-next-round transition in activeDesignRound citations.",
	}, nil)

	driveCapability(t, fixture, 48)

	sawRefusal := false
	for _, code := range fixture.tasks.failed {
		if code == "model_result_contract_rejected" {
			sawRefusal = true
		}
	}
	if !sawRefusal {
		t.Fatal("an uncovered round was never refused")
	}
	root := fixture.rootRecord(t)
	allTasks, err := fixture.tasks.ListByCorrelation(context.Background(), root.CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	gapNamed := false
	for _, task := range allTasks {
		if strings.Contains(task.Reason, "ownership partition is not exact") &&
			strings.Contains(task.Reason, "claimed by no department") &&
			strings.Contains(task.Reason, "RC:1:1") && strings.Contains(task.Reason, "RC:1:2") {
			gapNamed = true
		}
		prefix := "executive:" + itoa64(root.ID) + ":worker:"
		if strings.HasPrefix(task.IdempotencyKey, prefix) && strings.Contains(task.IdempotencyKey, "design-round:2") {
			t.Fatalf("round-2 workers were materialized over an unclaimed change: %s", task.IdempotencyKey)
		}
	}
	if !gapNamed {
		t.Fatal("the coverage refusal did not name every unclaimed change")
	}
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
			if strings.Contains(task.Reason, "claimed by no department") &&
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

// GUARD: the provider-facing plan schema and the planner-facing instructions
// teach the SAME claim rule. When the schema still said "every required
// change MUST appear here exactly once" while the instructions said "claim
// only your share; empty is legal", the contract was impossible for two
// departments -- and it incentivized exactly the duplicate claim the host
// then refused. This guard fails when either side stops saying: subset
// claims, empty legality with no tasks, and one-exact global union.
func TestThePlanSchemaTeachesTheSameClaimRuleAsTheInstructions(t *testing.T) {
	var envelope struct {
		Properties struct {
			RevisionOwnership struct {
				Description string `json:"description"`
			} `json:"revision_ownership"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(departmentPlanOutputSchema, &envelope); err != nil {
		t.Fatalf("plan schema is not readable JSON: %v", err)
	}
	schema := envelope.Properties.RevisionOwnership.Description
	if schema == "" {
		t.Fatal("revision_ownership declares no description; the model reads nothing")
	}
	for _, shared := range []string{"exactly once", "propose no tasks"} {
		if !strings.Contains(schema, shared) || !strings.Contains(departmentRoundClaimRules, shared) {
			t.Fatalf("claim rule drift on %q:\nSCHEMA: %s\nINSTRUCTIONS: %s", shared, schema, departmentRoundClaimRules)
		}
	}
	for _, forbidden := range []string{"MUST appear here exactly once", "Empty only when"} {
		if strings.Contains(schema, forbidden) {
			t.Fatalf("schema still teaches the old every-change-is-yours rule (%q):\n%s", forbidden, schema)
		}
	}
}

// GUARD: after a replan settles a contradiction, the candidate handed to the
// adversarial reviewer is the POST-replan frontier. The superseded owner's
// deliverable must not reappear beside the resolution that replaced it --
// that would re-inject the contradiction checkpoint E just settled into the
// design loop -- and everything still authoritative appears exactly once.
func TestECandidateAfterReplanIsThePostReplanFrontier(t *testing.T) {
	fixture := eFixture(t)
	fixture.eOwnership = ownerEntry("RC:1:1", eOwnerKey) + "," + ownerEntry("RC:1:2", eSupportKey)
	fixture.eReviewVerdict = "needs_replan"
	fixture.eOutcomes = `[` +
		`{"required_change_id":"RC:1:1","status":"conflicted","canonical_resolution":"","conflicting_task_refs":["task:a","task:b"]},` +
		`{"required_change_id":"RC:1:2","status":"resolved","canonical_resolution":"cited","conflicting_task_refs":[]}]`
	fixture.eFollowups = `[{"client_key":"reconcile_mdr","assigned_role_id":"ingenieria_ia/qa","task_class":"engineering.review",` +
		`"title":"Reconcile MDR granularity","instructions":"One falsifiable claim.","acceptance_criteria":["Cite"],"dependencies":[]}]`
	fixture.eFollowupOwnership = `[{"required_change_id":"RC:1:1","owner_client_key":"reconcile_mdr"}]`

	base := fixture.harness.departmentReviewBody
	fixture.harness.departmentReviewBody = func(task TaskRecord) string {
		if strings.Contains(task.IdempotencyKey, ":replan:") {
			return eReplanReviewBody([]string{"RC:1:1", "RC:1:2"})
		}
		return base(task)
	}

	driveCapability(t, fixture, 40)

	root := fixture.rootRecord(t)
	allTasks, err := fixture.tasks.ListByCorrelation(context.Background(), root.CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	reviewTask, ok := findTaskByKey(allTasks, childKey(root.ID, "design-review:round:2"))
	if !ok || reviewTask.Status != "completed" {
		t.Fatal("the round-2 candidate was never reviewed")
	}
	var supersededWorker, keptWorker, followupWorker TaskRecord
	for _, worker := range departmentWorkerTasks(allTasks, root.ID, "ingenieria_ia") {
		if designRoundOf(worker.IdempotencyKey) != 2 || worker.Status != "completed" {
			continue
		}
		switch {
		case strings.Contains(worker.IdempotencyKey, ":"+eOwnerKey):
			supersededWorker = worker
		case strings.Contains(worker.IdempotencyKey, ":"+eSupportKey):
			keptWorker = worker
		case strings.Contains(worker.IdempotencyKey, ":reconcile_mdr"):
			followupWorker = worker
		}
	}
	if supersededWorker.ID == 0 || keptWorker.ID == 0 || followupWorker.ID == 0 {
		t.Fatal("the replan chain never materialized its three workers")
	}
	instructions := reviewTask.Instructions
	if !strings.Contains(instructions, fmt.Sprintf("(task:%d ", followupWorker.ID)) ||
		!strings.Contains(instructions, fmt.Sprintf("(task:%d ", keptWorker.ID)) {
		t.Fatalf("the post-replan frontier is incomplete in the candidate:\n%.500s", instructions)
	}
	if strings.Contains(instructions, fmt.Sprintf("(task:%d ", supersededWorker.ID)) {
		t.Fatalf("the superseded deliverable reappeared in the candidate:\n%.500s", instructions)
	}
	if got := strings.Count(instructions, fmt.Sprintf("(task:%d ", keptWorker.ID)); got != 1 {
		t.Fatalf("a kept deliverable appears %d times in the candidate, want once", got)
	}
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

// GUARD: a department whose round sheet claims NOTHING is asked for nothing
// -- no work, no review -- and its accepted deliverable is CARRIED FORWARD
// into the candidate, explicitly labeled, instead of being dropped from the
// design under review. More departments than demanded changes is a legal
// shape of the partition; losing a component nobody redid is not.
func TestEZeroScopeDepartmentIsCarriedForwardNotDropped(t *testing.T) {
	fixture, scopes := newTwoUnitEFixture(t, []string{
		"Clarify MaxDepartmentReplans granularity: aggregate ceiling or per-department cap.",
	}, map[string][]int{"diseno": {1}})
	if len(scopes["diseno"]) != 1 || len(scopes["ingenieria_ia"]) != 0 {
		t.Fatalf("fixture precondition broken: diseno=%d ingenieria_ia=%d",
			len(scopes["diseno"]), len(scopes["ingenieria_ia"]))
	}

	driveCapability(t, fixture, 48)

	root := fixture.rootRecord(t)
	if root.ReasonCode == ReasonDesignRoundsExhausted {
		t.Fatal("a legal N-departments-x-M-changes shape consumed every design round")
	}
	allTasks, err := fixture.tasks.ListByCorrelation(context.Background(), root.CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "executive:" + itoa64(root.ID) + ":"
	for _, task := range allTasks {
		key := task.IdempotencyKey
		if strings.Contains(key, "design-round:2") &&
			(strings.HasPrefix(key, prefix+"worker:ingenieria_ia:") ||
				strings.HasPrefix(key, prefix+"leader-review:ingenieria_ia")) {
			t.Fatalf("the unclaiming department materialized round-2 work %s (%s)",
				task.IdempotencyKey, task.TaskClass)
		}
	}
	// The claim sheet exists -- the department WAS asked -- and bound nothing.
	sheet, ok := findTaskByKey(allTasks, childKey(root.ID, "leader-plan:ingenieria_ia:design-round:2"))
	if !ok || sheet.Status != "completed" {
		t.Fatal("the unclaiming department never got to state its empty claim")
	}
	disenoDroveRoundTwo := false
	for _, task := range allTasks {
		if strings.Contains(task.IdempotencyKey, "leader-review:diseno:design-round:2") &&
			task.Status == "completed" {
			disenoDroveRoundTwo = true
		}
	}
	if !disenoDroveRoundTwo {
		t.Fatal("the claiming department never drove its round-2 review")
	}
	adjudication, ok := findTaskByKey(allTasks, childKey(root.ID, "design-adjudication:round:2"))
	if !ok || adjudication.Status != "completed" {
		t.Fatal("the claiming department's round never reached its adjudication")
	}
	// And the round-2 candidate still contains ingenieria_ia's accepted
	// round-1 deliverable, labeled as carried forward.
	reviewTask, ok := findTaskByKey(allTasks, childKey(root.ID, "design-review:round:2"))
	if !ok || reviewTask.Status != "completed" {
		t.Fatal("the round-2 candidate was never reviewed")
	}
	var carriedWorker TaskRecord
	foundWorker := false
	for _, task := range allTasks {
		if strings.HasPrefix(task.IdempotencyKey, prefix+"worker:ingenieria_ia:") &&
			designRoundOf(task.IdempotencyKey) == 1 {
			carriedWorker, foundWorker = task, true
		}
	}
	if !foundWorker {
		t.Fatal("no round-1 deliverable exists for the un-asked department")
	}
	if !strings.Contains(reviewTask.Instructions, "[carried forward unchanged from design round 1") {
		t.Fatalf("the carry-forward was not labeled in the candidate:\n%.400s", reviewTask.Instructions)
	}
	if !strings.Contains(reviewTask.Instructions, fmt.Sprintf("task:%d", carriedWorker.ID)) {
		t.Fatalf("the carried deliverable (task %d) vanished from the candidate:\n%.400s",
			carriedWorker.ID, reviewTask.Instructions)
	}
}

// GUARD: each department's reviewer reads an ownership table built from ITS
// assigned subset -- what its own sheet claimed -- and nothing else. Another
// department's change must not appear as an UNASSIGNED row instructing this
// reviewer to state an outcome the closed-world gate then refuses.
func TestEReviewerSeesOnlyItsAssignedSubset(t *testing.T) {
	fixture, scopes := newTwoUnitEFixture(t, []string{
		"Clarify MaxDepartmentReplans granularity: aggregate ceiling or per-department cap.",
		"Ground the revise-to-next-round transition in activeDesignRound citations.",
	}, map[string][]int{"diseno": {1}, "ingenieria_ia": {2}})
	disenoChange, ingenieriaChange := scopes["diseno"][0], scopes["ingenieria_ia"][0]

	driveCapability(t, fixture, 48)

	tableOf := func(unit string) string {
		allTasks, err := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range allTasks {
			if task.TaskClass == TaskClassCoordinationDeptReview &&
				strings.HasPrefix(task.IdempotencyKey,
					"executive:"+itoa64(fixture.rootRecord(t).ID)+":leader-review:"+unit+":design-round:2") {
				index := strings.Index(task.Instructions, "REVISION OWNERSHIP TABLE")
				if index < 0 {
					t.Fatalf("%s review missing the ownership table:\n%s", unit, task.Instructions)
				}
				return task.Instructions[index:]
			}
		}
		t.Fatalf("no round-2 review exists for %s", unit)
		return ""
	}
	disenoTable, ingenieriaTable := tableOf("diseno"), tableOf("ingenieria_ia")
	if !strings.Contains(disenoTable, disenoChange.ID+" [owner: diseno_owner]") {
		t.Fatalf("diseno table missing its own row:\n%s", disenoTable)
	}
	if strings.Contains(disenoTable, ingenieriaChange.ID) {
		t.Fatalf("another department's change leaked into diseno's table:\n%s", disenoTable)
	}
	if !strings.Contains(ingenieriaTable, ingenieriaChange.ID+" [owner: ingenieria_ia_owner]") {
		t.Fatalf("ingenieria_ia table missing its own row:\n%s", ingenieriaTable)
	}
	if strings.Contains(ingenieriaTable, disenoChange.ID) {
		t.Fatalf("another department's change leaked into ingenieria_ia's table:\n%s", ingenieriaTable)
	}
}

// GUARD: after a replan, authority over the redone changes MOVED with the
// previous review's followup_ownership bindings -- the :replan:1 reviewer
// must see the redo owner as authoritative, while changes nobody re-executed
// keep the plan's owner. Stale authority is exactly what made R16's replan
// reviewer judge answers whose provenance it could not see.
func TestEReplanReviewSeesTheRedoOwnerAsAuthoritative(t *testing.T) {
	fixture := eFixture(t)
	fixture.eOwnership = ownerEntry("RC:1:1", eOwnerKey) + "," + ownerEntry("RC:1:2", eOwnerKey)
	fixture.eReviewVerdict = "needs_replan"
	fixture.eOutcomes = `[` +
		`{"required_change_id":"RC:1:1","status":"conflicted","canonical_resolution":"","conflicting_task_refs":["task:a","task:b"]},` +
		`{"required_change_id":"RC:1:2","status":"resolved","canonical_resolution":"cited","conflicting_task_refs":[]}]`
	fixture.eFollowups = `[{"client_key":"reconcile_mdr","assigned_role_id":"ingenieria_ia/qa","task_class":"engineering.review",` +
		`"title":"Reconcile MDR granularity","instructions":"One falsifiable claim.","acceptance_criteria":["Cite"],"dependencies":[]}]`
	fixture.eFollowupOwnership = `[{"required_change_id":"RC:1:1","owner_client_key":"reconcile_mdr"}]`

	base := fixture.harness.departmentReviewBody
	fixture.harness.departmentReviewBody = func(task TaskRecord) string {
		if strings.Contains(task.IdempotencyKey, ":replan:") {
			return eReplanReviewBody([]string{"RC:1:1", "RC:1:2"})
		}
		return base(task)
	}

	driveCapability(t, fixture, 30)

	allTasks, err := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	var replanReview TaskRecord
	found := false
	for _, task := range allTasks {
		if task.TaskClass == TaskClassCoordinationDeptReview && strings.Contains(task.IdempotencyKey, ":replan:1") {
			replanReview, found = task, true
		}
	}
	if !found {
		t.Fatal("the replan review was never created")
	}
	index := strings.Index(replanReview.Instructions, "REVISION OWNERSHIP TABLE")
	if index < 0 {
		t.Fatalf("replan review missing the ownership table:\n%s", replanReview.Instructions)
	}
	table := replanReview.Instructions[index:]
	if !strings.Contains(table, "RC:1:1 [owner: reconcile_mdr]") {
		t.Fatalf("the redone change does not show its redo owner:\n%s", table)
	}
	if !strings.Contains(table, "RC:1:2 [owner: "+eOwnerKey+"]") {
		t.Fatalf("the change nobody re-executed lost its plan owner:\n%s", table)
	}
}

// GUARDS: accept + conflicted, and accept + unresolved, are both contract
// rejections; conflicted under needs_replan routes to the DEPARTMENT replan
// bound without opening a third design round; and so does the honest
// unresolved answer -- the routing needs_replan exists to deliver.
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

	t.Run("unresolved routes to the department replan bound", func(t *testing.T) {
		fixture := eFixture(t)
		fixture.eOwnership = ownerEntry("RC:1:1", eOwnerKey) + "," + ownerEntry("RC:1:2", eOwnerKey)
		fixture.eReviewVerdict = "needs_replan"
		fixture.eOutcomes = `[` +
			`{"required_change_id":"RC:1:1","status":"unresolved","canonical_resolution":"no deliverable addressed it","conflicting_task_refs":[]},` +
			`{"required_change_id":"RC:1:2","status":"resolved","canonical_resolution":"cited","conflicting_task_refs":[]}]`
		fixture.eFollowups = `[{"client_key":"redo_rc11","assigned_role_id":"ingenieria_ia/qa","task_class":"engineering.review",` +
			`"title":"Redo the unanswered change","instructions":"Address it.","acceptance_criteria":["Cite"],"dependencies":[]}]`
		fixture.eFollowupOwnership = `[{"required_change_id":"RC:1:1","owner_client_key":"redo_rc11"}]`

		driveCapability(t, fixture, 30)

		root := fixture.rootRecord(t)
		if root.ReasonCode == ReasonDesignRoundsExhausted {
			t.Fatal("an honest unresolved answer consumed a DESIGN round")
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
