package executive

import "fmt"

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

// GovernedCampaignTopology is the structural floor of a governed_implementation
// campaign: its WORST admissible tree, since a budget below it can stop a run the
// orchestrator would otherwise drive on.
//
// Every design round plans the department again, runs a worker and reviews it,
// and each review may ask for maxDepartmentReplans replans of a worker and a
// review (replans are counted per round: the review key carries the round).
// Each of those children takes a subagent from the root budget, like the CEO
// plan and the closure, and every attempt of one takes a model call. The
// adversarial review, the adjudication, the implementation plan and the
// engineering mission are not charged to the root budget, so they add nothing.
//
// Smoke #30 (root 1687, 2026-09-27) was approved with the minimal 5 subagents
// and blocked in its second design round. Smoke #33 (root 1742) cleared a floor
// that counted one attempt and no replan, spent three worker retries and a
// round-2 replan, and blocked with 11 of 12 model calls used when the next
// review could not reserve its tokens.
func GovernedCampaignTopology(maxDesignRounds, maxDepartmentReplans int) CampaignTopologyFloor {
	if maxDesignRounds < 1 {
		maxDesignRounds = 1
	}
	if maxDepartmentReplans < 0 {
		maxDepartmentReplans = 0
	}
	minimal := MinimalCampaignStages()
	plan, worker, review := minimal[1], minimal[2], minimal[3]
	named := func(stage CampaignStage, suffix string) CampaignStage {
		stage.Name += suffix
		return stage
	}
	stages := []CampaignStage{minimal[0]}
	for round := 1; round <= maxDesignRounds; round++ {
		suffix := fmt.Sprintf("_round_%d", round)
		stages = append(stages, named(plan, suffix), named(worker, suffix), named(review, suffix))
		for replan := 1; replan <= maxDepartmentReplans; replan++ {
			replanSuffix := fmt.Sprintf("%s_replan_%d", suffix, replan)
			stages = append(stages, named(worker, replanSuffix), named(review, replanSuffix))
		}
	}
	stages = append(stages, minimal[4])
	floor := CampaignTopologyFloor{
		Stages:     stages,
		ModelCalls: int64(len(stages) * governedTaskAttempts),
		Subagents:  int64(len(stages)),
	}
	for _, stage := range stages {
		if stage.Depth > floor.Depth {
			floor.Depth = stage.Depth
		}
	}
	return floor
}

// AnalysisWorkersPerDepartment is the workers the analysis floor sizes each department for. Local
// smoke #47 (root 2066) ran four departments with seven workers, one department with three.
const AnalysisWorkersPerDepartment = 2

// AnalysisCampaignTopology is the structural floor of an analysis_only campaign across
// `departments` departments: the CEO plan, then for each department its plan,
// AnalysisWorkersPerDepartment workers and its review, plus maxDepartmentReplans replans of a
// worker and a review, then the closure. Every child takes a subagent from the root budget and
// every attempt of one takes a model call.
//
// The minimal topology sizes one department with one worker. Local smoke #54 (root 2186) was
// approved with 12 subagents, above that floor of 5, and blocked at its thirteenth child with
// four departments to answer: the floor let Finance recommend a budget the campaign could not
// finish.
func AnalysisCampaignTopology(departments, maxDepartmentReplans int) CampaignTopologyFloor {
	if departments < 1 {
		departments = 1
	}
	if maxDepartmentReplans < 0 {
		maxDepartmentReplans = 0
	}
	minimal := MinimalCampaignStages()
	plan, worker, review := minimal[1], minimal[2], minimal[3]
	named := func(stage CampaignStage, suffix string) CampaignStage {
		stage.Name += suffix
		return stage
	}
	stages := []CampaignStage{minimal[0]}
	for department := 1; department <= departments; department++ {
		suffix := fmt.Sprintf("_department_%d", department)
		stages = append(stages, named(plan, suffix))
		for w := 1; w <= AnalysisWorkersPerDepartment; w++ {
			stages = append(stages, named(worker, fmt.Sprintf("%s_worker_%d", suffix, w)))
		}
		stages = append(stages, named(review, suffix))
		for replan := 1; replan <= maxDepartmentReplans; replan++ {
			replanSuffix := fmt.Sprintf("%s_replan_%d", suffix, replan)
			stages = append(stages, named(worker, replanSuffix), named(review, replanSuffix))
		}
	}
	stages = append(stages, minimal[4])
	floor := CampaignTopologyFloor{
		Stages:     stages,
		ModelCalls: int64(len(stages) * governedTaskAttempts),
		Subagents:  int64(len(stages)),
	}
	for _, stage := range stages {
		if stage.Depth > floor.Depth {
			floor.Depth = stage.Depth
		}
	}
	return floor
}
