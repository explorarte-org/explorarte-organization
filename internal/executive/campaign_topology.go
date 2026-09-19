package executive

// The minimal canonical campaign is the shortest execution tree Orchestrator
// can drive to a completed root WITHOUT any optional phase: no design freeze
// (only a root carrying the design-freeze requirement runs one), no
// implementation mission, no replan, no retry, no follow-up task. Exactly one
// department is delegated to, and that department plans exactly one worker.
//
// This file is the single place that says what that tree is. Orchestrator
// attaches every child to the root budget at the depths declared here (it does
// not spell its own literals), and Campaign's execution-budget feasibility
// floor reads the same declaration through MinimalCampaignTopology. A change
// to the tree therefore changes both, and a test pins today's values against
// NormalExpectedCalls -- the orchestrator's own independent call accounting.
const (
	// DepthCEOPlan is the budget-tree depth of the CEO planning child.
	DepthCEOPlan int64 = 1
	// DepthDepartmentPlan is the depth of a department leader's planning child.
	DepthDepartmentPlan int64 = 2
	// DepthWorker is the depth of a department worker child.
	DepthWorker int64 = 3
	// DepthDepartmentReview is the depth of a department review child.
	DepthDepartmentReview int64 = 2
	// DepthCEOClosure is the depth of the CEO closure child.
	DepthCEOClosure int64 = 1
)

// CampaignStageActor names which kind of role runs a stage. The concrete role
// (and therefore its model route) is decided by the CEO's plan at run time, so
// a preflight can only reason about the set of roles that COULD run it.
type CampaignStageActor string

const (
	CampaignActorCEO    CampaignStageActor = "ceo"
	CampaignActorLeader CampaignStageActor = "department_leader"
	CampaignActorWorker CampaignStageActor = "department_worker"
)

// CampaignStage is one unconditional model-driven child of the minimal tree.
type CampaignStage struct {
	Name    string
	Purpose ExecutionPurpose
	Actor   CampaignStageActor
	// Depth is the budget-tree depth Orchestrator attaches the child at.
	Depth int64
}

// CampaignTopologyFloor is what the minimal canonical campaign structurally
// needs from an AgentBudget, before any price or token consideration.
type CampaignTopologyFloor struct {
	// Stages lists every unconditional child, in execution order.
	Stages []CampaignStage
	// ModelCalls is the number of model invocations the minimal tree makes
	// (one per stage; each is charged one model call by CostGate).
	ModelCalls int64
	// Subagents is the number of children attached to the root budget. Each
	// coordinated child consumes one (InheritForChild with no allocation).
	Subagents int64
	// Depth is the deepest budget-tree level a child is attached at.
	Depth int64
}

// MinimalCampaignStages is the ordered list of unconditional children.
func MinimalCampaignStages() []CampaignStage {
	return []CampaignStage{
		{Name: "ceo_plan", Purpose: PurposeCEOPlan, Actor: CampaignActorCEO, Depth: DepthCEOPlan},
		{Name: "department_plan", Purpose: PurposeDepartmentPlan, Actor: CampaignActorLeader, Depth: DepthDepartmentPlan},
		{Name: "department_worker", Purpose: PurposeDepartmentWorker, Actor: CampaignActorWorker, Depth: DepthWorker},
		{Name: "department_review", Purpose: PurposeDepartmentReview, Actor: CampaignActorLeader, Depth: DepthDepartmentReview},
		{Name: "ceo_closure", Purpose: PurposeCEOClosure, Actor: CampaignActorCEO, Depth: DepthCEOClosure},
	}
}

// MinimalCampaignTopology derives the structural floor from MinimalCampaignStages.
func MinimalCampaignTopology() CampaignTopologyFloor {
	stages := MinimalCampaignStages()
	floor := CampaignTopologyFloor{Stages: stages, ModelCalls: int64(len(stages)), Subagents: int64(len(stages))}
	for _, stage := range stages {
		if stage.Depth > floor.Depth {
			floor.Depth = stage.Depth
		}
	}
	return floor
}

// CampaignLeaderEligible reports whether unit and leader can host a campaign
// department: the exact conditions ValidateExecutivePlan requires of a
// delegated department.
func CampaignLeaderEligible(unit UnitRef, leader RoleRef) bool {
	return !unit.Retired && unit.Operational && !unit.Leaderless && unit.LeaderRoleID != "" &&
		leader.ID == unit.LeaderRoleID && leader.UnitID == unit.ID && leader.CanonicalLeader && roleAssignable(leader)
}

// CampaignWorkerEligible reports whether role can be assigned as a cognitive
// department worker: the exact rules ValidateDepartmentPlan applies to a
// proposed worker (assignable, never owner/CEO/observer, never an execution
// service).
func CampaignWorkerEligible(role RoleRef) bool {
	if !roleAssignable(role) {
		return false
	}
	if role.ID == OwnerRoleID || role.ID == CEORoleID || role.ID == ObserverRoleID {
		return false
	}
	return role.AuthorityClass != "execution_service"
}

// ExecutionContractBytes is the size of the host-owned execution-contract text
// Orchestrator appends to a purpose's model input (the contract with no
// evidence obligations). It is part of what a call sends, so a preflight that
// sizes a call's input must include it.
func ExecutionContractBytes(purpose ExecutionPurpose) int {
	return len(executionContractFor(purpose, nil))
}
