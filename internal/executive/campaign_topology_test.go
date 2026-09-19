package executive

import (
	"context"
	"reflect"
	"testing"
)

// The minimal canonical campaign, pinned. If the orchestrator's tree changes,
// this fails and forces the change to be deliberate -- Campaign's execution
// budget floor is derived from MinimalCampaignTopology, so a silent change here
// would silently change what Finance is told a budget must fund.
func TestMinimalCampaignTopologyIsPinned(t *testing.T) {
	want := []CampaignStage{
		{Name: "ceo_plan", Purpose: PurposeCEOPlan, Actor: CampaignActorCEO, Depth: 1},
		{Name: "department_plan", Purpose: PurposeDepartmentPlan, Actor: CampaignActorLeader, Depth: 2},
		{Name: "department_worker", Purpose: PurposeDepartmentWorker, Actor: CampaignActorWorker, Depth: 3},
		{Name: "department_review", Purpose: PurposeDepartmentReview, Actor: CampaignActorLeader, Depth: 2},
		{Name: "ceo_closure", Purpose: PurposeCEOClosure, Actor: CampaignActorCEO, Depth: 1},
	}
	if got := MinimalCampaignStages(); !reflect.DeepEqual(got, want) {
		t.Fatalf("minimal campaign stages changed:\n got %+v\nwant %+v", got, want)
	}
	floor := MinimalCampaignTopology()
	if floor.ModelCalls != 5 || floor.Subagents != 5 || floor.Depth != 3 {
		t.Fatalf("topology floor = calls %d, subagents %d, depth %d; want 5, 5, 3", floor.ModelCalls, floor.Subagents, floor.Depth)
	}
}

// Two independent derivations of "how many model calls does the minimal
// campaign make" must agree: this declaration and the orchestrator's own
// happy-path accounting (one department, one worker, no design freeze).
func TestMinimalCampaignTopologyAgreesWithTheOrchestratorsCallAccounting(t *testing.T) {
	if got, want := MinimalCampaignTopology().ModelCalls, int64(NormalExpectedCalls(1, 1, false)); got != want {
		t.Fatalf("topology model calls %d != NormalExpectedCalls(1,1,false) %d", got, want)
	}
	// ...and the campaign promotion path never asks for a design freeze or a
	// mission (nothing adds those requirements), so the floor must not count
	// their CEO phases: with a freeze the CEO would make one more call.
	if NormalExpectedCalls(1, 1, true) == int64ToInt(MinimalCampaignTopology().ModelCalls) {
		t.Fatal("test premise broken: a design-freeze campaign should need more calls than the minimal one")
	}
}

func int64ToInt(v int64) int { return int(v) }

// Each stage's depth is what the orchestrator attaches it at: the constants
// are the ones orchestrator.go uses, so this ties the declaration to behavior.
func TestStageDepthsAreTheConstantsTheOrchestratorUses(t *testing.T) {
	byName := map[string]int64{}
	for _, stage := range MinimalCampaignStages() {
		byName[stage.Name] = stage.Depth
	}
	if byName["ceo_plan"] != DepthCEOPlan || byName["department_plan"] != DepthDepartmentPlan ||
		byName["department_worker"] != DepthWorker || byName["department_review"] != DepthDepartmentReview ||
		byName["ceo_closure"] != DepthCEOClosure {
		t.Fatalf("stage depths drifted from the depth constants: %+v", byName)
	}
	// A worker sits below a department plan, which sits below the CEO plan.
	if !(DepthCEOPlan < DepthDepartmentPlan && DepthDepartmentPlan < DepthWorker) {
		t.Fatal("the CEO -> department -> worker depth ordering is the topology; it must strictly deepen")
	}
}

// Pin the current CEO-plan output contract: the ceiling every dispatch of a
// purpose reserves comes from Limits.MaxOutputTokensFor, never from Finance
// policy, so a change to Executive's limit changes the floor with it.
func TestPurposeOutputCeilingsComeFromExecutiveLimits(t *testing.T) {
	limits := DefaultLimits()
	if got, want := limits.MaxOutputTokensFor(PurposeCEOPlan), limits.MaxOutputTokens; got != want {
		t.Fatalf("CEO-plan output ceiling = %d, want Limits.MaxOutputTokens %d", got, want)
	}
	if limits.MaxOutputTokensFor(PurposeDepartmentWorker) >= limits.MaxOutputTokensFor(PurposeDepartmentPlan) {
		t.Fatal("a department worker's ceiling is deliberately below a plan's")
	}
	lowered := limits
	lowered.MaxOutputTokens = 9000
	if lowered.MaxOutputTokensFor(PurposeCEOPlan) != 9000 || lowered.MaxOutputTokensFor(PurposeCEOClosure) != 9000 {
		t.Fatal("changing Executive's canonical limit must change every non-worker purpose's ceiling")
	}
}

func TestExecutionContractBytesIsTheOrchestratorsContract(t *testing.T) {
	for _, stage := range MinimalCampaignStages() {
		if got, want := ExecutionContractBytes(stage.Purpose), len(executionContractFor(stage.Purpose, nil)); got != want {
			t.Fatalf("%s: ExecutionContractBytes = %d, contract length = %d", stage.Name, got, want)
		}
	}
	// Purposes that carry host guidance must be counted (a CEO-plan input has
	// none today; a department call does).
	if ExecutionContractBytes(PurposeDepartmentPlan) <= 0 || ExecutionContractBytes(PurposeDepartmentWorker) <= 0 {
		t.Fatal("department purposes carry an execution contract; the input ceiling must include it")
	}
}

// The eligibility helpers must match what the Validator actually accepts, or a
// preflight would count roles a real plan can never assign (and skip ones it
// can).
func TestWorkerEligibilityMatchesTheValidator(t *testing.T) {
	_, r := testValidator(t)
	dept := "ingenieria_ia"
	cases := map[string]RoleRef{
		"plain worker":       {ID: dept + "/w1", UnitID: dept, Enabled: true, Executable: true},
		"canonical leader":   {ID: dept + "/w2", UnitID: dept, Enabled: true, Executable: true, CanonicalLeader: true},
		"disabled":           {ID: dept + "/w3", UnitID: dept, Enabled: false, Executable: true},
		"not executable":     {ID: dept + "/w4", UnitID: dept, Enabled: true, Executable: false},
		"retired":            {ID: dept + "/w5", UnitID: dept, Enabled: true, Executable: true, Retired: true},
		"execution service":  {ID: dept + "/w6", UnitID: dept, Enabled: true, Executable: true, AuthorityClass: "execution_service"},
		"owner":              {ID: OwnerRoleID, UnitID: dept, Enabled: true, Executable: true},
		"ceo":                {ID: CEORoleID, UnitID: dept, Enabled: true, Executable: true},
		"observer":           {ID: ObserverRoleID, UnitID: dept, Enabled: true, Executable: true},
		"cognitive w/ class": {ID: dept + "/w7", UnitID: dept, Enabled: true, Executable: true, AuthorityClass: "department_worker"},
	}
	for _, role := range cases {
		r.roles[role.ID] = role
	}
	v, err := NewValidator(r, allowAuthz{}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for name, role := range cases {
		plan := DepartmentPlan{SchemaVersion: DepartmentPlanSchemaVersion, DepartmentID: dept, Tasks: []WorkerTaskProposal{{ClientKey: "a", AssignedRoleID: role.ID, Title: "x", Instructions: "x", AcceptanceCriteria: []string{"done"}}}}
		accepted := v.ValidateDepartmentPlan(context.Background(), 7, dept, dept+"/orquestador", plan) == nil
		if CampaignWorkerEligible(role) != accepted {
			t.Errorf("%s: CampaignWorkerEligible=%v but the validator accepts=%v", name, CampaignWorkerEligible(role), accepted)
		}
	}
}

func TestLeaderEligibilityMatchesTheValidator(t *testing.T) {
	_, base := testValidator(t)
	leader := base.leaders["ingenieria_ia"]
	unit := base.units["ingenieria_ia"]
	type variant struct {
		name   string
		unit   UnitRef
		leader RoleRef
	}
	variants := []variant{
		{"healthy", unit, leader},
		{"retired unit", func() UnitRef { u := unit; u.Retired = true; return u }(), leader},
		{"non-operational unit", func() UnitRef { u := unit; u.Operational = false; return u }(), leader},
		{"leaderless unit", func() UnitRef { u := unit; u.Leaderless = true; return u }(), leader},
		{"unit names no leader", func() UnitRef { u := unit; u.LeaderRoleID = ""; return u }(), leader},
		{"leader not canonical", unit, func() RoleRef { l := leader; l.CanonicalLeader = false; return l }()},
		{"leader disabled", unit, func() RoleRef { l := leader; l.Enabled = false; return l }()},
		{"leader in another unit", unit, func() RoleRef { l := leader; l.UnitID = "marketing"; return l }()},
		{"leader id mismatch", unit, func() RoleRef { l := leader; l.ID = "ingenieria_ia/otro"; return l }()},
	}
	for _, tc := range variants {
		reg := fakeRegistry{rev: RevisionRef{ID: 7}, units: map[string]UnitRef{"ingenieria_ia": tc.unit}, roles: map[string]RoleRef{tc.leader.ID: tc.leader}, leaders: map[string]RoleRef{"ingenieria_ia": tc.leader}}
		v, err := NewValidator(reg, allowAuthz{}, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		plan := ExecutivePlan{SchemaVersion: ExecutivePlanSchemaVersion, Objective: "x", DepartmentRequests: []DepartmentRequest{{UnitID: "ingenieria_ia", Objective: "x", Deliverable: "y"}}, SuccessCriteria: []string{"done"}}
		_, verr := v.ValidateExecutivePlan(context.Background(), 7, plan)
		if got := CampaignLeaderEligible(tc.unit, tc.leader); got != (verr == nil) {
			t.Errorf("%s: CampaignLeaderEligible=%v but the validator accepts=%v (err=%v)", tc.name, got, verr == nil, verr)
		}
	}
}
