package executive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// orchestratorWorkerID is operational provenance only: it names the process
// doing the work, it is recorded on task_attempts.worker_id, and it is NOT an
// execution principal. The security identity of an attempt is the role-bound
// principal resolved from the task's AssignedRoleID (see Dependencies.Principals).
const orchestratorWorkerID = "executive-orchestrator"

// executiveLeaseTTL is how long a claimed executive attempt's lease is issued
// for, and how far each heartbeat extends it.
const executiveLeaseTTL = 5 * time.Minute

type Orchestrator struct {
	organizationID string
	registry       RegistryResolver
	tasks          TaskCoordinator
	contexts       ContextCoordinator
	assignments    DispatchProvisioner
	principals     RoleBoundPrincipalResolver
	models         ModelInvocationReader
	acceptance     AcceptanceRecorder
	harness        HarnessExecutor
	budget         ModelBudgetGate
	completion     CompletionGate
	decisions      DecisionRecorder
	validator      *Validator
	limits         Limits
	clock          Clock
	budgets        AgentBudgetProvider
	messages       AgentMessagingProvider
	leaseKeeper    LeaseKeeperConfig

	programTarget ProgramTargetResolver
	missions      MissionProvisioner

	mu     sync.Mutex
	leases map[int64]LeaseRecord
}

// Dependencies is the Orchestrator's whole inbound surface. It is a struct
// rather than a positional parameter list because several of these are
// same-shaped interfaces whose order nobody can verify by reading a call
// site, and swapping two of them would compile.
type Dependencies struct {
	OrganizationID string
	Registry       RegistryResolver
	Tasks          TaskCoordinator
	Contexts       ContextCoordinator
	Assignments    DispatchProvisioner
	// Principals resolves the canonical role-bound execution principal that
	// holds an attempt's lease and executes under it.
	Principals RoleBoundPrincipalResolver
	// Models is Model Runtime's READ side. It carries no operation capable of
	// producing a provider call; execution goes through Harness or nowhere.
	Models ModelInvocationReader
	// Harness is the only execute side the productive Executive can reach.
	Harness HarnessExecutor
	// Budget is the Executive's correlation-wide model-call limit, enforced
	// before the Harness is entered.
	// Acceptance records which phase owns each owner criterion, so the
	// design reviewer is never handed a requirement only the built change
	// could satisfy.
	Acceptance    AcceptanceRecorder
	Budget        ModelBudgetGate
	Completion    CompletionGate
	Decisions     DecisionRecorder
	Authorization AuthorizationGate
	Limits        Limits
	Clock         Clock
}

// OrchestratorOption configures optional Orchestrator behavior that most
// callers (in particular every existing test) don't need to set up.
type OrchestratorOption func(*Orchestrator)

// WithAgentBudgets wires multidimensional budget tracking into every task
// the orchestrator creates. Without this option, Orchestrator behaves
// exactly as it did before AgentBudgetProvider existed.
func WithAgentBudgets(budgets AgentBudgetProvider) OrchestratorOption {
	return func(o *Orchestrator) { o.budgets = budgets }
}

// WithAgentMessaging wires durable delegation/completion messaging into
// every task the orchestrator creates. Without this option, Orchestrator
// behaves exactly as it did before AgentMessagingProvider existed.
func WithAgentMessaging(messages AgentMessagingProvider) OrchestratorOption {
	return func(o *Orchestrator) { o.messages = messages }
}

func NewOrchestrator(deps Dependencies, opts ...OrchestratorOption) (*Orchestrator, error) {
	if strings.TrimSpace(deps.OrganizationID) == "" || deps.Registry == nil || deps.Tasks == nil || deps.Contexts == nil ||
		deps.Assignments == nil || deps.Principals == nil || deps.Models == nil || deps.Harness == nil ||
		deps.Budget == nil || deps.Completion == nil || deps.Decisions == nil || deps.Authorization == nil ||
		deps.Acceptance == nil {
		return nil, errors.New("executive orchestrator dependencies are incomplete")
	}
	limits := deps.Limits
	if limits.MaxDepartments <= 0 {
		limits = DefaultLimits()
	}
	clock := deps.Clock
	if clock == nil {
		clock = ClockFunc(time.Now)
	}
	validator, err := NewValidator(deps.Registry, deps.Authorization, limits)
	if err != nil {
		return nil, err
	}
	orchestrator := &Orchestrator{
		organizationID: strings.TrimSpace(deps.OrganizationID), registry: deps.Registry, tasks: deps.Tasks,
		contexts: deps.Contexts, assignments: deps.Assignments, principals: deps.Principals, models: deps.Models,
		acceptance: deps.Acceptance,
		harness:    deps.Harness, budget: deps.Budget, completion: deps.Completion, decisions: deps.Decisions, validator: validator, limits: limits,
		clock: clock, leases: map[int64]LeaseRecord{}, leaseKeeper: DefaultLeaseKeeperConfig(),
	}
	for _, opt := range opts {
		opt(orchestrator)
	}
	return orchestrator, nil
}

func (o *Orchestrator) Submit(ctx context.Context, request SubmitRequest) (Run, bool, error) {
	request.ActorRoleID = strings.TrimSpace(request.ActorRoleID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if request.ActorRoleID != OwnerRoleID {
		return Run{}, false, fmt.Errorf("%w: executive submit requires %s", ErrInvalidInput, OwnerRoleID)
	}
	if len(request.IdempotencyKey) == 0 || len(request.IdempotencyKey) > 200 {
		return Run{}, false, fmt.Errorf("%w: idempotency key", ErrInvalidInput)
	}
	if err := validateRequiredString(request.Goal.Goal, o.limits.MaxInstructionsBytes, "goal"); err != nil {
		return Run{}, false, err
	}
	if len(request.Goal.AcceptanceCriteria) == 0 || len(request.Goal.AcceptanceCriteria) > o.limits.MaxAcceptanceCriteria {
		return Run{}, false, fmt.Errorf("%w: acceptance criteria", ErrInvalidInput)
	}
	criteriaTexts := AcceptanceTexts(request.Goal.AcceptanceCriteria)
	if err := validateStrings(criteriaTexts, o.limits, "acceptance_criteria"); err != nil {
		return Run{}, false, err
	}
	// A goal whose criteria are all deferred leaves the design reviewer
	// nothing to judge the design against, which is the mirror image of
	// the bug that motivated phases: a review with no applicable
	// requirement is as useless as one with unsatisfiable requirements.
	if len(AcceptanceForPhase(request.Goal.AcceptanceCriteria, AcceptanceDesign)) == 0 {
		return Run{}, false, fmt.Errorf("%w: at least one acceptance criterion must belong to the design phase", ErrInvalidInput)
	}
	if len(request.Goal.Requirements) > o.limits.MaxRequirementsPerTask {
		return Run{}, false, ErrPlanTooLarge
	}
	seenReq := map[string]struct{}{}
	requirements := append([]RequirementProposal(nil), request.Goal.Requirements...)
	for _, req := range requirements {
		if !validRequirementType(req.Type) || strings.TrimSpace(req.Key) == "" || strings.TrimSpace(req.Description) == "" {
			return Run{}, false, fmt.Errorf("%w: owner requirement", ErrInvalidInput)
		}
		if _, ok := seenReq[req.Key]; ok {
			return Run{}, false, fmt.Errorf("%w: duplicate requirement %s", ErrInvalidInput, req.Key)
		}
		seenReq[req.Key] = struct{}{}
	}
	if _, exists := seenReq["executive_closure_verified"]; exists {
		return Run{}, false, fmt.Errorf("%w: reserved requirement key", ErrInvalidInput)
	}
	requirements = append(requirements, RequirementProposal{Key: "executive_closure_verified", Type: "result", Description: "CEO closure is materialized from verified departmental results", Required: true})
	// Resolved BEFORE the root exists, so a campaign is never created with a
	// budget the system then fails to record.
	budget, err := resolveCampaignBudget(request.Budget)
	if err != nil {
		return Run{}, false, err
	}
	correlation := correlationID(request)
	root, reused, err := o.tasks.CreateTask(ctx, CreateTaskCommand{
		RequestedByRoleID:  OwnerRoleID,
		AssignedRoleID:     CEORoleID,
		TaskClass:          TaskClassOwnerGoal,
		IdempotencyKey:     request.IdempotencyKey,
		Title:              "Executive owner goal",
		Instructions:       request.Goal.Goal,
		AcceptanceCriteria: criteriaTexts,
		Priority:           100,
		MaxAttempts:        2,
		CorrelationID:      correlation,
		CausationID:        "owner:" + request.IdempotencyKey,
		Requirements:       requirements,
	})
	if err != nil {
		return Run{}, false, err
	}
	// The phase assignment is written before anything can read it, and it
	// is idempotent, so a resumed submit finds what the first one stored
	// rather than a second opinion about the same goal.
	if err := o.acceptance.RecordAcceptance(ctx, root.ID, request.Goal.AcceptanceCriteria); err != nil {
		return Run{}, false, fmt.Errorf("record acceptance phases for task %d: %w", root.ID, err)
	}
	if o.budgets != nil {
		if err := o.budgets.CreateRootBudget(ctx, root, budget, o.clock.Now()); err != nil {
			return Run{}, false, fmt.Errorf("create root agent budget for task %d: %w", root.ID, err)
		}
	}
	children, err := o.tasks.ListByCorrelation(ctx, root.CorrelationID)
	if err != nil {
		return Run{}, false, err
	}
	return ProjectRun(root, withoutRoot(children, root.ID)), reused, nil
}

func (o *Orchestrator) Status(ctx context.Context, rootTaskID int64) (Run, error) {
	root, err := o.tasks.GetTask(ctx, rootTaskID)
	if err != nil {
		return Run{}, err
	}
	if root.AssignedRoleID != CEORoleID || root.CorrelationID == "" {
		return Run{}, fmt.Errorf("%w: task is not an executive root", ErrInvalidInput)
	}
	tasks, err := o.tasks.ListByCorrelation(ctx, root.CorrelationID)
	if err != nil {
		return Run{}, err
	}
	run := ProjectRun(root, withoutRoot(tasks, root.ID))
	if closure, ok := findTaskByMarker(tasks, keyClosureMarker); ok && closure.Status == "completed" {
		if result, ok := o.resultForCompletedTask(ctx, closure); ok {
			if parsed, parseErr := ParseExecutiveClosure(result.JSONOutput, o.limits); parseErr == nil {
				run.AnswerToOwner = parsed.AnswerToOwner
			}
		}
	}
	return run, nil
}

func (o *Orchestrator) Resume(ctx context.Context, rootTaskID int64) (Run, error) {
	root, err := o.tasks.GetTask(ctx, rootTaskID)
	if err != nil {
		return Run{}, err
	}
	if root.AssignedRoleID != CEORoleID || root.CorrelationID == "" {
		return Run{}, fmt.Errorf("%w: task is not an executive root", ErrInvalidInput)
	}
	if isTerminalTask(root.Status) {
		return o.Status(ctx, rootTaskID)
	}
	if root.Status == "blocked" {
		switch root.ReasonCode {
		case "model_outcome_ambiguous":
			return ProjectRun(root, nil), ErrModelOutcomeAmbiguous
		case "indeterminate_tool_execution":
			// A tool may already have produced an external side effect.
			// Reopening this automatically is the one thing that could
			// duplicate it, so it stays blocked until a human reconciles.
			return ProjectRun(root, nil), ErrIndeterminateToolExecution
		case ReasonDesignRevisionRequired:
			// A design sent back for changes is waiting on a human. Silently
			// unblocking would re-run the reviewer and the adjudicator to
			// re-decide something already decided, at the reviewer's cost.
			return ProjectRun(root, nil), ErrRunBlocked
		case ReasonDesignRejected:
			return ProjectRun(root, nil), ErrRunBlocked
		case ReasonAdversarialReviewUnavailable:
			// Fails closed and stays closed: there is no second provider to
			// fall back to, so retrying on its own would only spin.
			return ProjectRun(root, nil), ErrRunBlocked
		case "dispatch_assignment_required":
			if !o.anyProvisionedLeasedTask(ctx, root.CorrelationID) {
				return o.Status(ctx, rootTaskID)
			}
		}
		if _, err = o.tasks.UnblockTask(ctx, root.ID, "service", orchestratorWorkerID); err != nil {
			return Run{}, err
		}
		root, err = o.tasks.GetTask(ctx, root.ID)
		if err != nil {
			return Run{}, err
		}
	}
	if err = o.tasks.Reconcile(ctx, 100); err != nil {
		return Run{}, err
	}
	revision, err := o.registry.CurrentRevision(ctx)
	if err != nil {
		return Run{}, err
	}
	if revision.ID != root.OrganizationRevisionID {
		_, _ = o.tasks.BlockTask(ctx, root.ID, "organization_revision_drift", "organization revision changed during executive run", "service", orchestratorWorkerID)
		return o.Status(ctx, root.ID)
	}

	all, err := o.tasks.ListByCorrelation(ctx, root.CorrelationID)
	if err != nil {
		return Run{}, err
	}
	children := withoutRoot(all, root.ID)
	planTask, ok := findTaskByMarker(children, keyCEOPlanMarker)
	if !ok {
		planTask, _, err = o.createCEOPlanTask(ctx, root)
		if err != nil {
			return Run{}, err
		}
	}
	if planTask.Status != "completed" {
		if _, err = o.driveTypedTask(ctx, root, planTask, executivePlanOutputSchema, PurposeCEOPlan, func(result InvocationResult) error {
			plan, parseErr := ParseExecutivePlan(result.JSONOutput, o.limits)
			if parseErr != nil {
				return parseErr
			}
			leaders, validateErr := o.validator.ValidateExecutivePlan(ctx, revision.ID, plan)
			if validateErr != nil {
				return validateErr
			}
			if len(plan.OwnerDecisionsRequired) > 0 {
				return fmt.Errorf("%w: owner_decision_required", ErrRunBlocked)
			}
			for _, req := range plan.DepartmentRequests {
				leader := leaders[req.UnitID]
				if _, _, createErr := o.createLeaderPlanTask(ctx, root, req, leader); createErr != nil {
					return createErr
				}
			}
			return nil
		}); err != nil {
			return o.handlePhaseError(ctx, root, planTask, err)
		}
		return o.Status(ctx, root.ID)
	}

	planResult, ok := o.resultForCompletedTask(ctx, planTask)
	if !ok {
		return o.blockRoot(ctx, root, "executive_plan_result_missing", "completed CEO planning task has no durable invocation result")
	}
	plan, err := ParseExecutivePlan(planResult.JSONOutput, o.limits)
	if err != nil {
		return o.blockRoot(ctx, root, "executive_plan_invalid", err.Error())
	}
	leaders, err := o.validator.ValidateExecutivePlan(ctx, revision.ID, plan)
	if err != nil {
		return o.blockRoot(ctx, root, "executive_plan_invalid", err.Error())
	}

	if run, done, phaseErr := o.driveDepartments(ctx, root, revision, plan, leaders); done || phaseErr != nil {
		if phaseErr != nil {
			return o.handlePhaseError(ctx, root, TaskRecord{}, phaseErr)
		}
		return run, nil
	}

	all, err = o.tasks.ListByCorrelation(ctx, root.CorrelationID)
	if err != nil {
		return Run{}, err
	}
	// The candidate design exists once the departments are done. If this run
	// is governed by a design freeze, it is decided here -- before closure,
	// because a CEO closure over an unfrozen design would report a settled
	// answer the organization never actually settled.
	root, err = o.tasks.GetTask(ctx, root.ID)
	if err != nil {
		return Run{}, err
	}
	if run, done, freezeErr := o.driveDesignFreeze(ctx, root, all); done || freezeErr != nil {
		if freezeErr != nil {
			return Run{}, freezeErr
		}
		return run, nil
	}
	all, err = o.tasks.ListByCorrelation(ctx, root.CorrelationID)
	if err != nil {
		return Run{}, err
	}
	// Engineering owns the work from here. This is the last thing the
	// Executive does before the boundary, and it ends when a governed
	// mission exists -- never by executing one.
	root, err = o.tasks.GetTask(ctx, root.ID)
	if err != nil {
		return Run{}, err
	}
	if run, done, missionErr := o.driveImplementationMission(ctx, root, all); done || missionErr != nil {
		if missionErr != nil {
			return Run{}, missionErr
		}
		return run, nil
	}
	all, err = o.tasks.ListByCorrelation(ctx, root.CorrelationID)
	if err != nil {
		return Run{}, err
	}
	closureTask, ok := findTaskByMarker(all, keyClosureMarker)
	if !ok {
		closureTask, _, err = o.createClosureTask(ctx, root, plan, all)
		if err != nil {
			return Run{}, err
		}
	}
	if closureTask.Status != "completed" {
		if _, err = o.driveTypedTask(ctx, root, closureTask, executiveClosureOutputSchema, PurposeCEOClosure, func(result InvocationResult) error {
			closure, parseErr := ParseExecutiveClosure(result.JSONOutput, o.limits)
			if parseErr != nil {
				return parseErr
			}
			if closure.Status == ClosureCompleted {
				if evidenceErr := o.validateRunCompletionEvidence(ctx, root, plan); evidenceErr != nil {
					return evidenceErr
				}
			}
			return nil
		}); err != nil {
			return o.handlePhaseError(ctx, root, closureTask, err)
		}
		return o.Status(ctx, root.ID)
	}
	closureResult, ok := o.resultForCompletedTask(ctx, closureTask)
	if !ok {
		return o.blockRoot(ctx, root, "executive_closure_result_missing", "completed CEO closure has no durable result")
	}
	closure, err := ParseExecutiveClosure(closureResult.JSONOutput, o.limits)
	if err != nil {
		return o.blockRoot(ctx, root, "executive_closure_invalid", err.Error())
	}
	if err = o.validateRunCompletionEvidence(ctx, root, plan); err != nil {
		return o.blockRoot(ctx, root, "executive_closure_evidence_conflict", err.Error())
	}
	if closure.Status != ClosureCompleted || len(closure.UnresolvedDecisions) > 0 || len(closure.BlockedItems) > 0 {
		return o.blockRoot(ctx, root, "executive_closure_not_complete", "CEO closure does not support a verified completed root")
	}
	if err = o.completeRoot(ctx, root, closureTask, closureResult, closure); err != nil {
		return o.handlePhaseError(ctx, root, closureTask, err)
	}
	return o.Status(ctx, root.ID)
}

const ceoPlanInstructionPrefix = `Produce only the ExecutivePlan JSON contract for the authoritative owner goal below. Propose operational departments; do not select providers, models, capabilities, tools, authority, credentials, or egress.

OWNER_DECISION_POLICY:
- owner_decisions_required is reserved ONLY for a human decision that is strictly required to continue THIS owner goal safely.
- Every requested owner decision must be directly grounded in the authoritative owner goal below or in a concrete blocker discovered while executing that goal.
- Do NOT copy, inherit, summarize, or reactivate historical pending decisions from memory, RAG, prior tasks, canonical documents, previous executive runs, or unrelated organizational work merely because they appear in context.
- A historical decision may be requested only when the current owner goal explicitly depends on it and cannot safely continue without the owner's choice.
- Optional unavailable integrations that the current owner goal explicitly declares non-blocking must NOT become owner decisions.
- Existing unresolved decisions concerning unrelated models, schedules, cells, repositories, profiles, skills, products, or previous milestones are not blockers for this goal.
- If this owner goal can proceed under existing authority, budget, safety constraints, and registered capabilities, owner_decisions_required MUST be [].
- Never suppress a genuine current-goal security, data-corruption, budget-escape, irreversible-data-loss, or real-execution blocker merely to keep owner_decisions_required empty.

AUTHORITATIVE_OWNER_GOAL_JSON=`

// buildCEOPlanInstructions preserves the owner's actual durable request across
// the root -> CEO-planning boundary. The planning task is the TaskRef used by
// Context Assembly, so referring vaguely to "the owner goal" is insufficient:
// the authoritative root content must travel with the child task.
//
// This fails closed rather than truncating the owner request. A plan generated
// from a silently shortened goal is not an acceptable substitute for the goal.
func buildCEOPlanInstructions(root TaskRecord, maxBytes int) (string, error) {
	payload, err := json.Marshal(struct {
		Goal               string   `json:"goal"`
		AcceptanceCriteria []string `json:"acceptance_criteria"`
	}{
		Goal:               root.Instructions,
		AcceptanceCriteria: append([]string(nil), root.AcceptanceCriteria...),
	})
	if err != nil {
		return "", fmt.Errorf("encode authoritative owner goal: %w", err)
	}

	if maxBytes <= 0 ||
		len(ceoPlanInstructionPrefix)+len(payload) > maxBytes {
		return "", fmt.Errorf(
			"%w: authoritative owner goal cannot fit CEO planning instructions without truncation",
			ErrPlanTooLarge,
		)
	}

	return ceoPlanInstructionPrefix + string(payload), nil
}

func (o *Orchestrator) createCEOPlanTask(ctx context.Context, root TaskRecord) (TaskRecord, bool, error) {
	instructions, err := buildCEOPlanInstructions(root, o.limits.MaxInstructionsBytes)
	if err != nil {
		return TaskRecord{}, false, err
	}

	task, reused, err := o.coordinatedChildren().Materialize(ctx, childRequest{Root: root, Sender: root, Depth: 1, Command: CreateTaskCommand{
		RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID,
		TaskClass:      TaskClassCoordinationCEOPlan,
		IdempotencyKey: childKey(root.ID, "ceo-plan"),
		Title:          "CEO executive planning",
		Instructions:   instructions,
		AcceptanceCriteria: []string{
			"Return one strict ExecutivePlan JSON value",
			"Use only operational registry unit IDs",
			"Do not grant authority or capabilities",
			"OwnerDecisionsRequired may contain only unresolved decisions that are actually required to execute the current owner goal",
			"Do not copy historical, bootstrap, prior-run, memory, RAG, evidence, or canonical-context decisions into OwnerDecisionsRequired",
			"If the current owner goal already decides an issue, treat it as decided and do not ask the owner again",
		},
		Priority: 100, MaxAttempts: 3,
		CorrelationID: root.CorrelationID,
		CausationID:   taskCausation(root.ID),
		Requirements: []RequirementProposal{{
			Key: "typed_plan", Type: "result",
			Description: "Validated ExecutivePlan invocation result",
			Required:    true,
		}},
	}})
	if err != nil {
		return TaskRecord{}, false, err
	}
	return task, reused, nil
}

func (o *Orchestrator) createLeaderPlanTask(ctx context.Context, root TaskRecord, req DepartmentRequest, leader RoleRef) (TaskRecord, bool, error) {
	instructions := boundedJSON(map[string]any{"department_id": req.UnitID, "objective": req.Objective, "deliverable": req.Deliverable, "constraints": req.Constraints, "priority": req.Priority}, o.limits.MaxInstructionsBytes)
	task, reused, err := o.coordinatedChildren().Materialize(ctx, childRequest{Root: root, Sender: root, Depth: 2, Command: CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: leader.ID,
		TaskClass:      TaskClassCoordinationDeptPlan,
		IdempotencyKey: childKey(root.ID, "leader-plan:"+req.UnitID), Title: "Department planning: " + req.UnitID,
		Instructions:       "Produce only DepartmentPlan JSON for this bounded request: " + instructions + "\n\n" + taskClassGuidance,
		AcceptanceCriteria: []string{"Return one strict DepartmentPlan JSON value", "Delegate only to active assignable roles in this department", "Use only existing requirement types"},
		Priority:           req.Priority, MaxAttempts: 3, CorrelationID: root.CorrelationID, CausationID: taskCausation(root.ID),
		Requirements: []RequirementProposal{{Key: "typed_plan", Type: "result", Description: "Validated DepartmentPlan invocation result", Required: true}},
	}})
	if err != nil {
		return TaskRecord{}, false, err
	}
	return task, reused, nil
}

// driveInProgress signals that driveDepartments made partial progress this
// cycle (drove a typed task, or is waiting on outstanding worker tasks) and
// the caller must stop here and return the live status instead of falling
// through to closure. Without this, the caller's `done` check never fires
// mid-department and it attempts to create/drive the CEO closure task
// before every department's review has actually completed.
func (o *Orchestrator) driveInProgress(ctx context.Context, root TaskRecord) (Run, bool, error) {
	run, err := o.Status(ctx, root.ID)
	return run, true, err
}

func (o *Orchestrator) driveDepartments(ctx context.Context, root TaskRecord, revision RevisionRef, plan ExecutivePlan, leaders map[string]RoleRef) (Run, bool, error) {
	for _, req := range plan.DepartmentRequests {
		leader := leaders[req.UnitID]
		all, err := o.tasks.ListByCorrelation(ctx, root.CorrelationID)
		if err != nil {
			return Run{}, false, err
		}
		planTask, ok := findTaskByKey(all, childKey(root.ID, "leader-plan:"+req.UnitID))
		if !ok {
			return Run{}, false, fmt.Errorf("%w: department planning task missing", ErrRegistryMismatch)
		}
		if planTask.Status != "completed" {
			_, err = o.driveTypedTask(ctx, root, planTask, departmentPlanOutputSchema, PurposeDepartmentPlan, func(result InvocationResult) error {
				parsed, e := ParseDepartmentPlan(result.JSONOutput, o.limits)
				if e != nil {
					return e
				}
				if e = o.validator.ValidateDepartmentPlan(ctx, revision.ID, req.UnitID, leader.ID, parsed); e != nil {
					return e
				}
				return o.materializeWorkerTasks(ctx, root, planTask, req.UnitID, parsed.Tasks, 0)
			})
			if err != nil {
				return Run{}, false, err
			}
			return o.driveInProgress(ctx, root)
		}
		planResult, ok := o.resultForCompletedTask(ctx, planTask)
		if !ok {
			return Run{}, false, fmt.Errorf("%w: leader plan result missing", ErrContractRejected)
		}
		deptPlan, e := ParseDepartmentPlan(planResult.JSONOutput, o.limits)
		if e != nil {
			return Run{}, false, e
		}
		if e = o.validator.ValidateDepartmentPlan(ctx, revision.ID, req.UnitID, leader.ID, deptPlan); e != nil {
			return Run{}, false, e
		}
		if e = o.materializeWorkerTasks(ctx, root, planTask, req.UnitID, deptPlan.Tasks, 0); e != nil {
			return Run{}, false, e
		}

		all, e = o.tasks.ListByCorrelation(ctx, root.CorrelationID)
		if e != nil {
			return Run{}, false, e
		}
		workerTasks := departmentWorkerTasks(all, root.ID, req.UnitID)
		sort.Slice(workerTasks, func(i, j int) bool { return workerTasks[i].ID < workerTasks[j].ID })
		for _, wt := range workerTasks {
			if wt.Status == "completed" || wt.Status == "no_action" {
				continue
			}
			_, e = o.driveTypedTask(ctx, root, wt, workerResultOutputSchema, PurposeDepartmentWorker, func(result InvocationResult) error {
				_, pErr := ParseWorkerResult(result.JSONOutput, o.limits)
				return pErr
			})
			if e != nil {
				return Run{}, false, e
			}
			return o.driveInProgress(ctx, root)
		}

		all, e = o.tasks.ListByCorrelation(ctx, root.CorrelationID)
		if e != nil {
			return Run{}, false, e
		}
		if !allDepartmentWorkersTerminal(all, root.ID, req.UnitID) {
			return o.driveInProgress(ctx, root)
		}
		reviewTask, ok := latestReviewTask(all, root.ID, req.UnitID)
		if !ok {
			reviewTask, _, e = o.createReviewTask(ctx, root, req, leader, all, 0)
			if e != nil {
				return Run{}, false, e
			}
		}
		if reviewTask.Status != "completed" {
			_, e = o.driveTypedTask(ctx, root, reviewTask, departmentReviewOutputSchema, PurposeDepartmentReview, func(result InvocationResult) error {
				review, pErr := ParseDepartmentReview(result.JSONOutput, o.limits)
				if pErr != nil {
					return pErr
				}
				if review.Verdict == ReviewNeedsReplan {
					if reviewReplanOrdinal(reviewTask.IdempotencyKey) >= o.limits.MaxDepartmentReplans {
						return fmt.Errorf("%w: department replan budget exhausted", ErrBudgetExceeded)
					}
					if pErr = o.validator.ValidateFollowups(ctx, revision.ID, req.UnitID, leader.ID, review.ProposedFollowupTasks); pErr != nil {
						return pErr
					}
					return o.materializeWorkerTasks(ctx, root, reviewTask, req.UnitID, review.ProposedFollowupTasks, reviewReplanOrdinal(reviewTask.IdempotencyKey)+1)
				}
				if len(review.ProposedFollowupTasks) > 0 {
					return fmt.Errorf("%w: followup tasks require needs_replan verdict", ErrContractRejected)
				}
				if review.Verdict == ReviewBlocked {
					return ErrRunBlocked
				}
				if review.Verdict == ReviewFail {
					return fmt.Errorf("%w: department review failed", ErrCompletionFailed)
				}
				return nil
			})
			if e != nil {
				return Run{}, false, e
			}
			return o.driveInProgress(ctx, root)
		}
		reviewResult, ok := o.resultForCompletedTask(ctx, reviewTask)
		if !ok {
			return Run{}, false, fmt.Errorf("%w: review result missing", ErrContractRejected)
		}
		review, e := ParseDepartmentReview(reviewResult.JSONOutput, o.limits)
		if e != nil {
			return Run{}, false, e
		}
		if review.Verdict == ReviewNeedsReplan {
			ordinal := reviewReplanOrdinal(reviewTask.IdempotencyKey) + 1
			if ordinal > o.limits.MaxDepartmentReplans {
				return Run{}, false, ErrBudgetExceeded
			}
			if e = o.validator.ValidateFollowups(ctx, revision.ID, req.UnitID, leader.ID, review.ProposedFollowupTasks); e != nil {
				return Run{}, false, e
			}
			if e = o.materializeWorkerTasks(ctx, root, reviewTask, req.UnitID, review.ProposedFollowupTasks, ordinal); e != nil {
				return Run{}, false, e
			}
			all, e = o.tasks.ListByCorrelation(ctx, root.CorrelationID)
			if e != nil {
				return Run{}, false, e
			}
			if !allDepartmentWorkersTerminal(all, root.ID, req.UnitID) {
				return o.driveInProgress(ctx, root)
			}
			if _, exists := findTaskByKey(all, childKey(root.ID, "leader-review:"+req.UnitID+":replan:"+strconv.Itoa(ordinal))); !exists {
				if _, _, e = o.createReviewTask(ctx, root, req, leader, all, ordinal); e != nil {
					return Run{}, false, e
				}
			}
			return o.driveInProgress(ctx, root)
		}
		if review.Verdict != ReviewAccept {
			return Run{}, false, fmt.Errorf("%w: department review %s", ErrCompletionFailed, review.Verdict)
		}
	}
	return Run{}, false, nil
}

// materializeWorkerTasks creates each worker task WITH its dependencies, in
// topological order.
//
// It used to create every task first and wire dependencies afterwards. Two
// things went wrong with that, and only one of them was visible.
//
// The visible one: a worker claims a task the instant it exists, and the task
// engine refuses to change dependencies on a running task, so AddDependency
// failed outright once a poll landed between the two loops.
//
// The one that matters: a dependent task was created with no dependencies, so
// it was born ready and claimable. Its prerequisite gate did not exist yet. In
// the run that exposed this, a design task and the review of that design were
// created 18 milliseconds apart and both became runnable immediately -- the
// review did not run first by luck of scheduling, not by guarantee.
//
// tasks.Service already does the right thing when dependencies are supplied at
// creation: it inserts the task, its dependencies and its requirements in one
// transaction, and a task with dependencies is born pending rather than ready.
// The capability was there; this function simply was not using it.
//
// Ordering is topological and stable: among nodes that become available at the
// same time, the DepartmentPlan's own order is preserved, so this introduces no
// new semantic ordering of its own and the same plan always materializes the
// same way.
func (o *Orchestrator) materializeWorkerTasks(ctx context.Context, root, source TaskRecord, departmentID string, proposals []WorkerTaskProposal, replan int) error {
	ordered, err := topologicalProposals(proposals)
	if err != nil {
		return err
	}
	created := map[string]TaskRecord{}
	for _, p := range ordered {
		// Every dependency is already created, because the order guarantees
		// it. A missing one is a graph this function cannot materialize, and
		// creating the task anyway would reproduce exactly the bug being
		// fixed: a dependent task, ready, with no gate.
		dependencies := make([]int64, 0, len(p.Dependencies))
		for _, dep := range p.Dependencies {
			prerequisite, ok := created[dep]
			if !ok {
				return fmt.Errorf("%w: worker task %q depends on %q, which was not materialized first", ErrContractRejected, p.ClientKey, dep)
			}
			dependencies = append(dependencies, prerequisite.ID)
		}
		suffix := "worker:" + departmentID + ":" + p.ClientKey
		if replan > 0 {
			suffix += "-replan:" + strconv.Itoa(replan)
		}
		t, _, err := o.coordinatedChildren().Materialize(ctx, childRequest{Root: root, Sender: source, Depth: 3, Command: CreateTaskCommand{RequestedByRoleID: source.AssignedRoleID, AssignedRoleID: p.AssignedRoleID, TaskClass: p.TaskClass, IdempotencyKey: childKey(root.ID, suffix), Title: p.Title, Instructions: p.Instructions, AcceptanceCriteria: p.AcceptanceCriteria, Dependencies: dependencies, Priority: p.Priority, MaxAttempts: 3, CorrelationID: root.CorrelationID, CausationID: taskCausation(source.ID), Requirements: appendResultRequirement(p.Requirements)}})
		if err != nil {
			return err
		}
		created[p.ClientKey] = t
	}
	return nil
}

// topologicalProposals orders proposals so every dependency precedes its
// dependents, breaking ties by the plan's own order.
//
// The tie-break is not cosmetic: it makes materialization deterministic, so a
// replan or a restart derives the same order, reuses the same idempotency keys
// and therefore the same tasks.
//
// A cycle or an unknown dependency fails closed. ValidateDepartmentPlan
// already rejects both, so reaching either here means the two disagree -- and
// materializing half a graph is worse than refusing it.
func topologicalProposals(proposals []WorkerTaskProposal) ([]WorkerTaskProposal, error) {
	index := make(map[string]int, len(proposals))
	for i, p := range proposals {
		index[p.ClientKey] = i
	}
	remaining := make(map[string]int, len(proposals))
	for _, p := range proposals {
		outstanding := 0
		for _, dep := range p.Dependencies {
			if _, known := index[dep]; !known {
				return nil, fmt.Errorf("%w: worker task %q depends on unknown %q", ErrContractRejected, p.ClientKey, dep)
			}
			outstanding++
		}
		remaining[p.ClientKey] = outstanding
	}
	ordered := make([]WorkerTaskProposal, 0, len(proposals))
	placed := make(map[string]struct{}, len(proposals))
	for len(ordered) < len(proposals) {
		progressed := false
		// Scanning in plan order every pass is what preserves the original
		// order among simultaneously available nodes.
		for _, p := range proposals {
			if _, done := placed[p.ClientKey]; done || remaining[p.ClientKey] != 0 {
				continue
			}
			ordered = append(ordered, p)
			placed[p.ClientKey] = struct{}{}
			progressed = true
			for _, other := range proposals {
				for _, dep := range other.Dependencies {
					if dep == p.ClientKey {
						remaining[other.ClientKey]--
					}
				}
			}
		}
		if !progressed {
			return nil, fmt.Errorf("%w: worker task dependencies are not materializable", ErrDependencyCycle)
		}
	}
	return ordered, nil
}

// hostOwnedResultRequirementKey is the single blocking requirement the host
// attaches to every worker task and guarantees to satisfy from the validated
// model result. It is named here, beside the function that attaches it, so the
// validator that enforces it cannot drift from the code that creates it.
const hostOwnedResultRequirementKey = "model_result"

// isHostOwnedWorkerRequirement recognizes the host's blocking requirement by
// its COMPLETE shape, not by its key.
//
// The key alone was not enough. A leader could occupy model_result with any
// other shape -- optional, or typed as an artifact -- and appendResultRequirement,
// which matched on key, would then decline to attach the real one. The task
// would run, and recordHarnessSuccess would look for a required result
// requirement to record its evidence against, find none, and reject a
// perfectly good model result with "result requirement missing".
//
// resultRequirementID demands Required and Type=="result" for this key, so
// those two fields are as much a part of the host's ownership as the key is.
func isHostOwnedWorkerRequirement(r RequirementProposal) bool {
	return r.Key == hostOwnedResultRequirementKey && r.Type == "result" && r.Required
}

func appendResultRequirement(in []RequirementProposal) []RequirementProposal {
	out := append([]RequirementProposal(nil), in...)
	for _, r := range out {
		if isHostOwnedWorkerRequirement(r) {
			return out
		}
	}
	return append(out, RequirementProposal{Key: hostOwnedResultRequirementKey, Type: "result", Description: "Validated durable model invocation result", Required: true})
}

func (o *Orchestrator) createReviewTask(ctx context.Context, root TaskRecord, req DepartmentRequest, leader RoleRef, all []TaskRecord, replan int) (TaskRecord, bool, error) {
	summary := boundedDepartmentSummary(all, root.ID, req.UnitID, o.limits.MaxInstructionsBytes)
	suffix := "leader-review:" + req.UnitID
	if replan > 0 {
		suffix += ":replan:" + strconv.Itoa(replan)
	}
	task, reused, err := o.coordinatedChildren().Materialize(ctx, childRequest{Root: root, Sender: root, Depth: 2, Command: CreateTaskCommand{RequestedByRoleID: CEORoleID, AssignedRoleID: leader.ID, TaskClass: TaskClassCoordinationDeptReview, IdempotencyKey: childKey(root.ID, suffix), Title: "Department review: " + req.UnitID, Instructions: "Review only this bounded durable task/evidence summary and return DepartmentReview JSON: " + summary + "\n\n" + taskClassGuidance, AcceptanceCriteria: []string{"Use only durable task states and evidence refs", "Return strict DepartmentReview JSON", "Do not execute tool intents"}, Priority: req.Priority, MaxAttempts: 3, CorrelationID: root.CorrelationID, CausationID: taskCausation(root.ID), Requirements: []RequirementProposal{{Key: "typed_review", Type: "result", Description: "Validated DepartmentReview invocation result", Required: true}}}})
	if err != nil {
		return TaskRecord{}, false, err
	}
	return task, reused, nil
}

func (o *Orchestrator) createClosureTask(ctx context.Context, root TaskRecord, plan ExecutivePlan, all []TaskRecord) (TaskRecord, bool, error) {
	summary := boundedClosureSummary(plan, all, root.ID, o.limits.MaxInstructionsBytes)
	task, reused, err := o.coordinatedChildren().Materialize(ctx, childRequest{Root: root, Sender: root, Depth: 1, Command: CreateTaskCommand{RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, TaskClass: TaskClassCoordinationCEOClosure, IdempotencyKey: childKey(root.ID, "ceo-closure"), Title: "CEO executive closure", Instructions: "Synthesize only from this bounded durable summary and return ExecutiveClosure JSON. A completed claim cannot override backend verification: " + summary, AcceptanceCriteria: []string{"Return strict ExecutiveClosure JSON", "Cite only supplied evidence refs", "Report blockers and unresolved owner decisions explicitly"}, Priority: 100, MaxAttempts: 3, CorrelationID: root.CorrelationID, CausationID: taskCausation(root.ID), Requirements: []RequirementProposal{{Key: "typed_closure", Type: "result", Description: "Validated ExecutiveClosure invocation result", Required: true}}}})
	if err != nil {
		return TaskRecord{}, false, err
	}
	return task, reused, nil
}

// driveTypedTask runs one cognitive execution for one task, synchronously,
// through the Execution Harness.
//
// The Executive no longer creates model invocations. It resolves identity,
// claims the attempt, builds context, checks its own budget, and then hands a
// deterministic run command to the Harness, which enters Model Runtime and
// leaves a durable execution history behind. What comes back is a verdict, not
// a provider result: the answer itself is read back from the durable Model
// Runtime row, so evidence keeps pointing at exactly the bytes that were
// persisted and hashed.
func (o *Orchestrator) driveTypedTask(ctx context.Context, root TaskRecord, task TaskRecord, schema json.RawMessage, purpose ExecutionPurpose, validate func(InvocationResult) error) (TaskRecord, error) {
	if !purpose.Valid() {
		return task, fmt.Errorf("%w: unknown execution purpose %q", ErrContractRejected, purpose)
	}
	if task.Status == "completed" {
		return task, nil
	}
	if task.Status == "awaiting_verification" {
		return o.gatedComplete(ctx, task)
	}
	// Before anything else -- before a claim, before a budget charge, before
	// the Harness -- ask whether an earlier execution of this task is still
	// unresolved at the provider boundary. This runs ahead of the claim on
	// purpose: a fresh attempt beside an in-flight provider call is already the
	// duplicate this guard exists to prevent, so the attempt must not be
	// created either.
	if handled, blocked, barrierErr := o.priorExecutionBarrier(ctx, root, task); handled {
		return blocked, barrierErr
	}
	lease, haveLease := o.localLease(task.ID)
	if task.Status == "ready" {
		// The principal is resolved exactly once per attempt, here, before the
		// claim, and then propagated: as the lease holder, as the ActorID of
		// every lease-authorized mutation below, and as the run's execution
		// principal. Resolving it a second time later would mean an attempt
		// could silently execute under a different identity than the one its
		// lease was issued to.
		principal, resolveErr := o.principals.ResolveRoleBoundPrincipal(ctx, task.AssignedRoleID)
		if resolveErr != nil {
			return task, resolveErr
		}
		if principal.ID == "" || (principal.RoleID != "" && principal.RoleID != task.AssignedRoleID) {
			return task, fmt.Errorf("%w: resolved principal is not bound to %s", ErrExecutionPrincipalUnusable, task.AssignedRoleID)
		}
		claimed, _, l, claimErr := o.tasks.ClaimTask(ctx, ClaimTaskCommand{
			TaskID: task.ID, WorkerID: orchestratorWorkerID, HolderPrincipalID: principal.ID,
			AssignedRoleID: task.AssignedRoleID, LeaseDuration: executiveLeaseTTL,
		})
		if claimErr != nil {
			return task, claimErr
		}
		task = claimed
		lease = l
		o.rememberLease(task.ID, lease)
		haveLease = true
	}
	// From here on every lease-authorized mutation is performed as the lease
	// holder, not as the worker name. The task engine matches ActorID against
	// task_leases.holder_id, so using orchestratorWorkerID here would be
	// rejected outright -- and, worse, would mean the attempt's authority was
	// never the principal the lease was issued to.
	actorID := lease.HolderID
	if haveLease && strings.TrimSpace(actorID) == "" {
		return task, fmt.Errorf("%w: active lease has no holder principal", ErrExecutionPrincipalUnusable)
	}
	if task.Status == "leased" {
		if !haveLease {
			return task, fmt.Errorf("%w: active task lease token unavailable after process restart", ErrRunBlocked)
		}

		// ModelDispatch intentionally allows creation of an assignment only
		// for a running task/attempt. Starting the attempt is therefore the
		// durable boundary that must precede assignment resolution. This
		// transition performs no model dispatch and remains lease-authorized
		// by the same role-bound principal that claimed the attempt.
		if _, startErr := o.tasks.StartAttempt(ctx, lease, actorID); startErr != nil {
			return task, startErr
		}
		refreshed, getErr := o.tasks.GetTask(ctx, task.ID)
		if getErr != nil {
			return task, getErr
		}
		task = refreshed
	}

	if task.Status == "running" {
		if !haveLease {
			return task, fmt.Errorf("%w: running task lease token unavailable after process restart", ErrRunBlocked)
		}

		// Assignment authorization is checked only after the attempt is
		// running, matching ModelDispatch's task-attempt invariant. Missing
		// authorization blocks the root before context construction, budget
		// authorization, or Harness/model execution.
		assignment, assignmentErr := o.assignments.ResolveAssignment(
			ctx,
			task.ID,
			lease.AttemptID,
			task.AssignedRoleID,
		)
		if assignmentErr != nil {
			_, _ = o.tasks.BlockTask(
				ctx,
				root.ID,
				"dispatch_assignment_required",
				fmt.Sprintf(
					"task=%d attempt=%d subject_role=%s lease_expires_at=%s",
					task.ID,
					lease.AttemptID,
					task.AssignedRoleID,
					lease.ExpiresAt.UTC().Format(time.RFC3339),
				),
				"service",
				orchestratorWorkerID,
			)
			return task, ErrDispatchAssignmentRequired
		}
		if assignment.OrganizationRevisionID != task.OrganizationRevisionID ||
			assignment.SubjectRoleID != task.AssignedRoleID {
			return task, fmt.Errorf("%w: dispatch assignment scope mismatch", ErrRegistryMismatch)
		}
	}
	if task.Status != "running" {
		return task, nil
	}
	if !haveLease {
		return task, fmt.Errorf("%w: running task lease token unavailable after process restart", ErrRunBlocked)
	}
	// One task attempt still means at most one model invocation. The Harness
	// enforces one turn per run through MaxTurns; this is the durable check
	// that a second invocation never appeared behind its back.
	invocations, err := o.models.FindTaskAttemptInvocations(ctx, task.ID, lease.AttemptID)
	if err != nil {
		return task, err
	}
	if len(invocations) > 1 {
		return task, fmt.Errorf("%w: multiple invocations for one task attempt", ErrContractRejected)
	}
	snapshot, err := o.contexts.Build(ctx, ContextRequest{
		OrganizationRevisionID: task.OrganizationRevisionID, ActorRoleID: task.AssignedRoleID,
		Purpose: purpose.LegacyPurpose(), TaskRef: "task:" + strconv.FormatInt(task.ID, 10),
		// M1.3: durable semantic selector facts, propagated alongside
		// (never instead of) the legacy Purpose string above. TaskClass/
		// ActorUnitID come from the durable Task record itself -- host-
		// assigned or host-validated at task-creation time, never
		// invented here. ExecutionPurpose is the validated host enum
		// value (purpose.Valid() was already checked above), never
		// model/instruction text.
		TaskClass: task.TaskClass, ExecutionPurpose: string(purpose), ActorUnitID: task.AssignedUnitID,
		IdempotencyKey: childKey(root.ID, fmt.Sprintf("context:%d:%d", task.ID, lease.AttemptID)),
		CorrelationID:  root.CorrelationID, CausationID: attemptCausation(task.ID, lease.AttemptID),
	})
	if err != nil {
		return task, err
	}
	// The correlation-wide model-call budget is checked HERE, before the run,
	// because this is the last point the Executive still controls. Once the
	// Harness is entered, invocation creation happens inside Model Runtime and
	// the Executive has no say left. len(invocations) > 0 means this attempt
	// already has its invocation and is being resumed, which is the same
	// logical model call and must not be charged twice.
	if len(invocations) == 0 {
		if err = o.budget.AuthorizeModelCall(ctx, ModelCallBudgetRequest{
			TaskID: task.ID, AttemptID: lease.AttemptID, CorrelationID: root.CorrelationID,
		}); err != nil {
			return task, err
		}
	}

	command := HarnessRunCommand{
		RunID:                harnessRunID(o.organizationID, task.ID, lease.AttemptID, purpose),
		TaskID:               task.ID,
		AttemptID:            lease.AttemptID,
		RoleID:               task.AssignedRoleID,
		ExecutionPrincipalID: actorID,
		LeaseToken:           lease.LeaseToken,
		Context:              snapshot,
		Purpose:              purpose,
		OutputSchema:         schema,
		ExecutionContract:    executionContractFor(purpose),
		MaxOutputTokens:      o.limits.MaxOutputTokens,
		CorrelationID:        root.CorrelationID,
		CausationID:          attemptCausation(task.ID, lease.AttemptID),
		Deadline:             o.clock.Now().Add(o.limits.InvocationDeadline),
	}

	execCtx, keeper := o.startLeaseKeeper(ctx, task.ID, lease, actorID)
	outcome, execErr := o.harness.Execute(execCtx, command)
	keeperErr := keeper.stop()
	if refreshed := keeper.currentLease(); refreshed.LeaseToken != "" {
		lease = refreshed
		if keeperErr == nil {
			o.rememberLease(task.ID, lease)
		}
	}

	// Keeper first, unconditionally. A run that "succeeded" while its lease
	// was being lost produced a result nobody is authorized to record, and the
	// task engine would refuse the write anyway. Deciding this before looking
	// at the Harness verdict is what keeps that from becoming a spurious
	// completion.
	if keeperErr != nil {
		if handled, blocked, ambiguityErr := o.ambiguityGuard(ctx, root, task, lease.AttemptID); handled {
			return blocked, ambiguityErr
		}
		o.forgetLease(task.ID)
		return task, fmt.Errorf("%w: task=%d attempt=%d: %v", ErrLeaseLost, task.ID, lease.AttemptID, keeperErr)
	}
	if execErr != nil {
		if handled, blocked, ambiguityErr := o.ambiguityGuard(ctx, root, task, lease.AttemptID); handled {
			return blocked, ambiguityErr
		}
		// The Harness itself failed to run, which says nothing about the
		// provider. Repeating it would meet the same broken execution.
		return o.failAttempt(ctx, task, lease, actorID, "harness_execution_failed", execErr.Error(), ErrCompletionFailed, false)
	}
	if err = outcome.Validate(); err != nil {
		return task, err
	}
	if outcome.Status == HarnessRunSucceeded {
		return o.recordHarnessSuccess(ctx, task, lease, actorID, outcome, validate)
	}
	return o.handleHarnessFailure(ctx, root, task, lease, actorID, outcome)
}

// executionContractFor returns the execution-time output-contract guidance that
// must reach the model for a given purpose, regardless of when the durable task
// was created. A task created by an older Executive build is re-driven through
// the same TaskRecord (never re-created), so its persisted Instructions may lack
// the contract; injecting it here, at run time, is what closes that gap without
// rewriting durable instructions or the context snapshot.
//
// The guidance is an execution instruction, not evidence and not authority:
// ValidTaskClass in the host-side parser remains the final acceptance boundary.
// A single definition (taskClassGuidance) is reused so the create-time and
// run-time paths cannot diverge.
func executionContractFor(purpose ExecutionPurpose) string {
	switch purpose {
	case PurposeDepartmentPlan, PurposeDepartmentReview:
		return taskClassGuidance
	default:
		return ""
	}
}

// harnessRunID is the durable identity of one cognitive execution. It is a
// pure function of organization, attempt and purpose: the same process
// re-entering the same attempt resumes the same run, and a fresh attempt
// (which is what a lease expiry produces) is necessarily a different run. A
// random per-tick identifier would have made every resume a new trajectory and
// every duplicate a new provider call.
func harnessRunID(organizationID string, taskID, attemptID int64, purpose ExecutionPurpose) string {
	return fmt.Sprintf("executive:%s:task:%d:attempt:%d:%s:v1", organizationID, taskID, attemptID, purpose)
}

// priorExecutionBarrier refuses to start new work for a task while any of its
// executions may already have reached the provider without a resolved outcome.
//
// It deliberately restates none of Model Runtime's state machine. Model Runtime
// answers "may this have reached the provider" itself
// (InvocationStatus.ProviderExecutionMayHaveStarted), and its reconciler is
// what later turns an expired send into the durable ambiguous verdict. This
// only enforces the consequence: while that answer is yes and no verdict
// exists, the Executive waits.
//
// The window this closes is an ordering race between two independent
// reconcilers. The Task Engine expires a lease and makes the task ready again
// on its own schedule; Model Runtime classifies an expired dispatch on its own.
// If the task became retryable first, the Executive would previously claim a
// fresh attempt and issue a second provider call for work whose first call may
// already be in flight. Task reconciliation running before model
// reconciliation is now safe, because readiness alone no longer authorizes
// execution.
//
// Every attempt of the task is inspected, not only the current one: the unsafe
// invocation belongs to the attempt that died, and the whole point is that a
// NEW attempt must not execute beside it.
func (o *Orchestrator) priorExecutionBarrier(ctx context.Context, root, task TaskRecord) (bool, TaskRecord, error) {
	var ambiguous, unresolved InvocationRecord
	for _, attempt := range task.Attempts {
		invocations, err := o.models.FindTaskAttemptInvocations(ctx, task.ID, attempt.ID)
		if err != nil {
			return true, task, err
		}
		for _, invocation := range invocations {
			if invocation.Status == "ambiguous" && ambiguous.ID == 0 {
				ambiguous = invocation
			}
			if invocation.ProviderExecutionMayHaveStarted && unresolved.ID == 0 {
				unresolved = invocation
			}
		}
	}
	// A resolved ambiguous verdict wins: it is durable, terminal, and requires
	// explicit reconciliation rather than waiting.
	if ambiguous.ID != 0 {
		_, _ = o.tasks.BlockTask(ctx, root.ID, "model_outcome_ambiguous",
			fmt.Sprintf("task=%d attempt=%d invocation=%d requires explicit inspection", task.ID, ambiguous.AttemptID, ambiguous.ID),
			"service", orchestratorWorkerID)
		return true, task, ErrModelOutcomeAmbiguous
	}
	if unresolved.ID != 0 {
		// Fail closed and stay retryable: nothing durable changes, no attempt is
		// created, no budget is charged, and the next pass re-asks once Model
		// Runtime has had the chance to reconcile.
		return true, task, fmt.Errorf("%w: task=%d attempt=%d invocation=%d is %q",
			ErrPriorExecutionUnresolved, task.ID, unresolved.AttemptID, unresolved.ID, unresolved.Status)
	}
	return false, task, nil
}

// ambiguityGuard is the safety property that survived the migration intact:
// Model Runtime, not the Harness, owns provider send/outcome ambiguity, so
// after any execution whose outcome is uncertain the durable invocation rows
// are inspected before anything is retried. An ambiguous invocation blocks the
// run for explicit reconciliation and never produces a second provider call.
func (o *Orchestrator) ambiguityGuard(ctx context.Context, root, task TaskRecord, attemptID int64) (bool, TaskRecord, error) {
	if ctx.Err() != nil {
		// Nothing can be read or written under a dead context; the next resume
		// performs the same inspection before it would execute anything.
		return false, task, nil
	}
	invocations, err := o.models.FindTaskAttemptInvocations(ctx, task.ID, attemptID)
	if err != nil {
		return true, task, err
	}
	for _, invocation := range invocations {
		if invocation.Status != "ambiguous" {
			continue
		}
		_, _ = o.tasks.BlockTask(ctx, root.ID, "model_outcome_ambiguous",
			fmt.Sprintf("task=%d attempt=%d invocation=%d requires explicit inspection", task.ID, attemptID, invocation.ID),
			"service", orchestratorWorkerID)
		return true, task, ErrModelOutcomeAmbiguous
	}
	return false, task, nil
}

// failAttempt records a terminal failure for THIS attempt. retryable decides
// whether the task may spend another one of its attempts or is finished.
//
// It is a parameter because the answer differs by failure. Identity drift and
// a broken run history describe something structural that a second attempt
// would meet again; a provider that was momentarily at capacity does not. A
// task carrying max_attempts=3 that died on attempt 1 of a transient failure
// is a task whose retry budget was never real.
func (o *Orchestrator) failAttempt(ctx context.Context, task TaskRecord, lease LeaseRecord, actorID, code, detail string, sentinel error, retryable bool) (TaskRecord, error) {
	failed, recErr := o.tasks.RecordAttemptFailed(ctx, lease, actorID, code, truncate(detail, 2000), retryable)
	o.forgetLease(task.ID)
	if recErr != nil {
		return task, recErr
	}
	return failed, fmt.Errorf("%w: %s", sentinel, code)
}

// recordHarnessSuccess validates the durable result the run produced. Every
// check the pre-Harness path performed still runs, against the same durable
// row: the Harness reports which invocation answered, and the answer itself is
// read back from Model Runtime rather than trusted from the verdict.
func (o *Orchestrator) recordHarnessSuccess(ctx context.Context, task TaskRecord, lease LeaseRecord, actorID string, outcome HarnessRunOutcome, validate func(InvocationResult) error) (TaskRecord, error) {
	result, err := o.models.GetResult(ctx, outcome.InvocationID)
	if err != nil {
		return task, err
	}
	if result.ToolIntents > 0 {
		return task, ErrToolIntentRejected
	}
	if len(result.JSONOutput) == 0 {
		return task, fmt.Errorf("%w: JSON output required", ErrContractRejected)
	}
	if result.ResponseBytes > o.limits.MaxInputBytes {
		return task, ErrPlanTooLarge
	}
	// The Harness carries Model Runtime's canonical JSON through unchanged. If
	// the two ever disagree, something rewrote the answer between the durable
	// row and the verdict, and neither copy can be trusted.
	if outcome.FinalOutput != string(result.JSONOutput) {
		return task, fmt.Errorf("%w: harness final output does not match the durable model result", ErrContractRejected)
	}
	if err = validate(result); err != nil {
		// Provider succeeded but host-side semantic validation rejected the
		// result. The invocation stays succeeded (it was executed and charged).
		// The attempt must NOT stay running — close it durably as a contract
		// rejection so the next tick can retry with a fresh attempt.
		_, failErr := o.tasks.RecordAttemptFailed(ctx, lease, actorID, "model_result_contract_rejected", truncate(err.Error(), 2000), true)
		o.forgetLease(task.ID)
		if failErr != nil {
			return task, failErr
		}
		return task, fmt.Errorf("%w: %v", ErrModelResultContractRejected, err)
	}
	reqID := resultRequirementID(task.Requirements)
	if reqID == 0 {
		return task, fmt.Errorf("%w: result requirement missing", ErrContractRejected)
	}
	if err = o.tasks.RecordEvidence(ctx, EvidenceCommand{
		TaskID: task.ID, RequirementID: reqID, Type: "result",
		Reference: fmt.Sprintf("model-invocation:%d", outcome.InvocationID), Digest: result.ResponseHash,
		RecordedBy: orchestratorWorkerID,
		Metadata:   map[string]any{"invocation_id": outcome.InvocationID, "response_bytes": result.ResponseBytes},
		Satisfies:  true,
	}); err != nil {
		return task, err
	}
	finished, recErr := o.tasks.RecordAttemptSucceeded(ctx, lease, actorID, "validated model result")
	o.forgetLease(task.ID)
	if recErr != nil {
		return task, recErr
	}
	return o.gatedComplete(ctx, finished)
}

// rescheduleAfterAuthorityOutage hands re-entry to the task engine after
// authority could not be CONSULTED -- an infrastructure fact, not a statement
// about the principal.
//
// Previously this returned the sentinel and left the attempt holding its
// lease, on the reasoning that the same run would resume on the next tick.
// Nothing scheduled that tick. The run sat until its lease expired, which is
// a slower and less legible path to the same place, and on a busy outage it
// meant the work simply stopped.
//
// Re-entry is delegated rather than invented: RecordAttemptFailed with
// retryable=true routes the attempt through the task engine's own retry
// policy, so backoff and max_attempts are the engine's, the state is durable
// across a restart, and an outage that never clears ends terminal on its own
// instead of looping. Nothing else in this switch becomes retryable.
//
// Two guards run first, and both exist to protect the same thing: a retry
// must never become a second provider call for work that may already have
// reached the provider.
func (o *Orchestrator) rescheduleAfterAuthorityOutage(ctx context.Context, root, task TaskRecord, lease LeaseRecord, actorID string, outcome HarnessRunOutcome) (TaskRecord, error) {
	// An execution whose send is unresolved at the provider boundary is
	// exactly the case where retrying duplicates it.
	if handled, blocked, err := o.ambiguityGuard(ctx, root, task, lease.AttemptID); handled {
		return blocked, err
	}
	// AuthorityUnavailable means the Harness wrote nothing, so this attempt
	// should have no durable invocation. If one exists anyway, the two facts
	// disagree, and choosing which to believe is how a duplicate provider
	// call happens. Fail closed and non-retryably instead.
	invocations, err := o.models.FindTaskAttemptInvocations(ctx, task.ID, lease.AttemptID)
	if err != nil {
		return task, err
	}
	if len(invocations) > 0 {
		// A run that claimed unavailable authority while durable invocations
		// already existed is contradictory, not transient.
		return o.failAttempt(ctx, task, lease, actorID, "authority_unavailable_with_durable_invocation",
			fmt.Sprintf("authority was reported unavailable while attempt %d already had %d durable invocation(s)", lease.AttemptID, len(invocations)),
			ErrExecutionAuthorityUnavailable, false)
	}
	failed, recErr := o.tasks.RecordAttemptFailed(ctx, lease, actorID, "execution_authority_unavailable",
		truncate(outcome.TerminationReason, 2000), true)
	o.forgetLease(task.ID)
	if recErr != nil {
		return task, recErr
	}
	return failed, fmt.Errorf("%w: %s", ErrExecutionAuthorityUnavailable, outcome.TerminationReason)
}

// handleHarnessFailure maps one Harness verdict onto existing Executive task
// semantics. There is deliberately no new state machine here: every branch
// lands on a status the Executive already had.
func (o *Orchestrator) handleHarnessFailure(ctx context.Context, root, task TaskRecord, lease LeaseRecord, actorID string, outcome HarnessRunOutcome) (TaskRecord, error) {
	switch outcome.Failure {
	case HarnessFailureAuthorityUnavailable:
		return o.rescheduleAfterAuthorityOutage(ctx, root, task, lease, actorID, outcome)
	case HarnessFailureAuthorizationDenied:
		// Authority said no. It will say no again.
		return o.failAttempt(ctx, task, lease, actorID, "execution_authority_denied", outcome.TerminationReason, ErrExecutionAuthorityDenied, false)
	case HarnessFailureIndeterminateTool:
		// A tool may already have reached outside the system. This is the one
		// outcome that must never be retried automatically, so the attempt
		// fails non-retryably AND the root is blocked for reconciliation.
		_, _ = o.tasks.RecordAttemptFailed(ctx, lease, actorID, "indeterminate_tool_execution", truncate(outcome.TerminationReason, 2000), false)
		o.forgetLease(task.ID)
		_, _ = o.tasks.BlockTask(ctx, root.ID, "indeterminate_tool_execution",
			fmt.Sprintf("task=%d attempt=%d: %s", task.ID, lease.AttemptID, truncate(outcome.TerminationReason, 1000)),
			"service", orchestratorWorkerID)
		return task, ErrIndeterminateToolExecution
	case HarnessFailureToolRejected:
		// Executive typed tasks expose no tools at all. A model that asks for
		// one gets the same treatment it always got: rejected, never executed,
		// and never a completion.
		return task, ErrToolIntentRejected
	case HarnessFailureLimitReached:
		// A budget that is spent stays spent; retrying would only spend the
		// task's remaining attempts against the same exhausted ceiling.
		return o.failAttempt(ctx, task, lease, actorID, "harness_limit_reached", outcome.TerminationReason, ErrBudgetExceeded, false)
	case HarnessFailureIdentityDrift:
		return o.failAttempt(ctx, task, lease, actorID, "harness_identity_drift", outcome.TerminationReason, ErrRunIdentityDrift, false)
	case HarnessFailureCancelled:
		if handled, blocked, err := o.ambiguityGuard(ctx, root, task, lease.AttemptID); handled {
			return blocked, err
		}
		return task, fmt.Errorf("%w: %s", ErrExecutionInterrupted, outcome.TerminationReason)
	case HarnessFailureHistoryError:
		if handled, blocked, err := o.ambiguityGuard(ctx, root, task, lease.AttemptID); handled {
			return blocked, err
		}
		return o.failAttempt(ctx, task, lease, actorID, "harness_history_error", outcome.TerminationReason, ErrHarnessHistoryFailed, false)
	case HarnessFailureModelError:
		if handled, blocked, err := o.ambiguityGuard(ctx, root, task, lease.AttemptID); handled {
			return blocked, err
		}
		// Model Runtime already decided whether this failure was transient
		// and recorded the answer; the Executive reads it rather than
		// re-deriving it. An unreadable answer means no retry: spending an
		// attempt on a guess can repeat a call the provider may already have
		// billed.
		// A failure with no invocation to ask about, and an invocation with
		// no recorded outcome, are both "unknown" -- and unknown means no
		// retry, because spending an attempt on a guess can repeat a call
		// the provider may already have billed. What changed is that the
		// two are no longer indistinguishable from a recorded "no": the
		// reader now says which case it is, so a future decision can act on
		// the difference instead of inheriting it.
		retryable := false
		if outcome.InvocationID > 0 {
			if value, readErr := o.models.ProviderFailureRetryable(ctx, outcome.InvocationID); readErr == nil {
				retryable = value
			}
		}
		return o.failAttempt(ctx, task, lease, actorID, "model_invocation_failed", outcome.TerminationReason, ErrCompletionFailed, retryable)
	default:
		return task, fmt.Errorf("%w: unknown harness failure %q", ErrContractRejected, outcome.Failure)
	}
}

func (o *Orchestrator) gatedComplete(ctx context.Context, task TaskRecord) (TaskRecord, error) {
	attemptID := latestFinishedAttemptID(task.Attempts)
	if attemptID == 0 {
		return task, fmt.Errorf("%w: no finished attempt", ErrCompletionInconclusive)
	}
	verified, err := o.completion.Verify(ctx, task.ID, attemptID)
	if err != nil {
		return task, err
	}
	if err := o.decisions.RecordAttemptDecision(ctx, AttemptDecisionRecord{TaskID: task.ID, AttemptID: attemptID, Verdict: verified.Verdict, Detail: verified.Detail}); err != nil {
		return task, fmt.Errorf("record decision trace: %w", err)
	}
	switch verified.Verdict {
	case CompletionPass:
		return o.tasks.FinalizeCompleted(ctx, task.ID, "service", orchestratorWorkerID)
	case CompletionFail:
		failed, finErr := o.tasks.FinalizeFailed(ctx, task.ID, "completion_verification_failed", verified.Detail, "service", orchestratorWorkerID)
		if finErr != nil {
			return task, finErr
		}
		return failed, ErrCompletionFailed
	case CompletionInconclusive:
		blocked, blockErr := o.tasks.BlockTask(ctx, task.ID, "completion_verification_inconclusive", verified.Detail, "service", orchestratorWorkerID)
		if blockErr != nil {
			return task, blockErr
		}
		return blocked, ErrCompletionInconclusive
	default:
		return task, fmt.Errorf("%w: unknown completion verdict", ErrCompletionInconclusive)
	}
}

// ReconcileGatedCompletions closes the crash window between
// RecordAttemptDecision committing a terminal decision and the task itself
// being finalized/blocked: two separate Postgres transactions (decisiongraph
// and tasks each own their own commits; there is no shared transaction
// spanning both), with nothing driving a retry if the process dies between
// them. A task sits in StatusAwaitingVerification from the moment its
// attempt finishes until gatedComplete successfully finalizes it — nothing
// else produces or clears that status — so finding tasks stuck there is a
// direct, sufficient signal, without needing to read decisiongraph state at
// all. Re-running gatedComplete is safe to retry because RecordAttemptDecision
// is itself now idempotent (see runtimeadapter.DecisionGraph.RecordAttemptDecision):
// if the decision was already recorded on a prior attempt, it's a no-op, and
// gatedComplete proceeds straight to the task finalization step that never
// ran. This is manually triggered (see cmd/orgctl), matching every other
// reconciliation path in this codebase (orgctl task reconcile, orgctl sleep
// run, orgctl postrun) — nothing in this system triggers work autonomously
// on a timer yet; every recurring sweep is operator/cron-invoked.
func (o *Orchestrator) ReconcileGatedCompletions(ctx context.Context, limit int) (ReconcileGatedCompletionsResult, error) {
	pending, err := o.tasks.ListAwaitingGating(ctx, limit)
	if err != nil {
		return ReconcileGatedCompletionsResult{}, fmt.Errorf("list tasks awaiting gating: %w", err)
	}
	var result ReconcileGatedCompletionsResult
	result.Found = len(pending)
	// Each task's gatedComplete call is its own independent operation across
	// separate transactions (that's the exact gap this reconciler exists to
	// paper over) — one task failing to reconcile must not block recovery
	// of every other genuinely orphaned task in the same batch.
	for _, task := range pending {
		if _, gateErr := o.gatedComplete(ctx, task); gateErr != nil &&
			!errors.Is(gateErr, ErrCompletionFailed) && !errors.Is(gateErr, ErrCompletionInconclusive) {
			result.Failed = append(result.Failed, ReconcileGatedCompletionFailure{TaskID: task.ID, Err: gateErr})
			continue
		}
		result.Reconciled++
	}
	return result, nil
}

type ReconcileGatedCompletionsResult struct {
	Found      int
	Reconciled int
	Failed     []ReconcileGatedCompletionFailure
}

type ReconcileGatedCompletionFailure struct {
	TaskID int64
	Err    error
}

func (o *Orchestrator) completeRoot(ctx context.Context, root, closureTask TaskRecord, result InvocationResult, closure ExecutiveClosure) error {
	reqID := resultRequirementID(root.Requirements)
	if reqID == 0 {
		return fmt.Errorf("%w: root closure requirement missing", ErrContractRejected)
	}
	if err := o.tasks.RecordEvidence(ctx, EvidenceCommand{TaskID: root.ID, RequirementID: reqID, Type: "result", Reference: fmt.Sprintf("task:%d:model-invocation:%d", closureTask.ID, result.InvocationID), Digest: result.ResponseHash, RecordedBy: orchestratorWorkerID, Metadata: map[string]any{"closure_task_id": closureTask.ID, "answer_hash": actionDigest(closure.AnswerToOwner)}, Satisfies: true}); err != nil {
		return err
	}
	principal, err := o.principals.ResolveRoleBoundPrincipal(ctx, CEORoleID)
	if err != nil {
		return err
	}
	if principal.ID == "" {
		return fmt.Errorf("%w: no role-bound principal for %s", ErrExecutionPrincipalUnusable, CEORoleID)
	}
	_, _, lease, err := o.tasks.ClaimTask(ctx, ClaimTaskCommand{
		TaskID: root.ID, WorkerID: orchestratorWorkerID, HolderPrincipalID: principal.ID,
		AssignedRoleID: CEORoleID, LeaseDuration: 2 * time.Minute,
	})
	if err != nil {
		return err
	}
	o.rememberLease(root.ID, lease)
	if _, err = o.tasks.StartAttempt(ctx, lease, principal.ID); err != nil {
		return err
	}
	finished, err := o.tasks.RecordAttemptSucceeded(ctx, lease, principal.ID, "executive closure verified")
	o.forgetLease(root.ID)
	if err != nil {
		return err
	}
	_, err = o.gatedComplete(ctx, finished)
	return err
}

func (o *Orchestrator) validateRunCompletionEvidence(ctx context.Context, root TaskRecord, plan ExecutivePlan) error {
	all, err := o.tasks.ListByCorrelation(ctx, root.CorrelationID)
	if err != nil {
		return err
	}
	for _, req := range plan.DepartmentRequests {
		review, ok := latestReviewTask(all, root.ID, req.UnitID)
		if !ok || review.Status != "completed" {
			return fmt.Errorf("department %s review is not verified completed", req.UnitID)
		}
		result, ok := o.resultForCompletedTask(ctx, review)
		if !ok {
			return fmt.Errorf("department %s review result missing", req.UnitID)
		}
		parsed, e := ParseDepartmentReview(result.JSONOutput, o.limits)
		if e != nil {
			return e
		}
		if parsed.Verdict != ReviewAccept {
			return fmt.Errorf("department %s verdict is %s", req.UnitID, parsed.Verdict)
		}
	}
	return nil
}

func (o *Orchestrator) resultForCompletedTask(ctx context.Context, task TaskRecord) (InvocationResult, bool) {
	attemptID := latestFinishedAttemptID(task.Attempts)
	if attemptID == 0 {
		return InvocationResult{}, false
	}
	invocations, err := o.models.FindTaskAttemptInvocations(ctx, task.ID, attemptID)
	if err != nil || len(invocations) != 1 || invocations[0].Status != "succeeded" {
		return InvocationResult{}, false
	}
	result, err := o.models.GetResult(ctx, invocations[0].ID)
	if err != nil {
		return InvocationResult{}, false
	}
	return result, true
}

// handlePhaseError decides whether a phase failure is a durable statement
// about the run or a transient condition the next tick can retry.
//
// The non-blocking set matters more than it looks: blocking the root for a
// lost lease or an unavailable authority would wedge the run permanently,
// because ResumeDurable deliberately refuses to auto-reopen a blocked root.
// Those conditions resolve themselves -- the task engine expires the lease,
// reconciles the attempt and produces a fresh one -- so the correct response
// is to report and step back, not to record a verdict.
func (o *Orchestrator) handlePhaseError(ctx context.Context, root, task TaskRecord, err error) (Run, error) {
	if isNonBlockingPhaseError(err) {
		run, _ := o.Status(ctx, root.ID)
		return run, err
	}
	code := "executive_phase_failed"
	if errors.Is(err, ErrCompletionInconclusive) {
		code = "completion_verification_inconclusive"
	}
	if errors.Is(err, ErrToolIntentRejected) {
		code = "model_result_tool_intent_rejected"
	}
	if errors.Is(err, ErrExecutionAuthorityDenied) {
		code = "execution_authority_denied"
	}
	if errors.Is(err, ErrExecutionPrincipalUnusable) {
		code = "execution_principal_unusable"
	}
	if errors.Is(err, ErrModelResultContractRejected) {
		code = "model_result_contract_rejected"
	}
	if errors.Is(err, ErrContractRejected) {
		code = "model_result_contract_rejected"
	}
	run, blockErr := o.blockRoot(ctx, root, code, err.Error())
	if blockErr != nil {
		return Run{}, blockErr
	}
	return run, err
}

// isNonBlockingPhaseError lists the conditions whose root is already blocked
// with a more precise reason, or that must stay retryable.
func isNonBlockingPhaseError(err error) bool {
	switch {
	case errors.Is(err, ErrDispatchAssignmentRequired),
		errors.Is(err, ErrModelOutcomeAmbiguous),
		errors.Is(err, ErrIndeterminateToolExecution),
		errors.Is(err, ErrLeaseLost),
		errors.Is(err, ErrExecutionAuthorityUnavailable),
		errors.Is(err, ErrExecutionPrincipalUnavailable),
		errors.Is(err, ErrPriorExecutionUnresolved),
		errors.Is(err, ErrExecutionInterrupted),
		errors.Is(err, ErrModelResultContractRejected):
		return true
	}
	return false
}

func (o *Orchestrator) blockRoot(ctx context.Context, root TaskRecord, code, reason string) (Run, error) {
	_, err := o.tasks.BlockTask(ctx, root.ID, code, truncate(reason, 2000), "service", orchestratorWorkerID)
	if err != nil {
		return Run{}, err
	}
	return o.Status(ctx, root.ID)
}

func (o *Orchestrator) anyProvisionedLeasedTask(ctx context.Context, correlation string) bool {
	all, err := o.tasks.ListByCorrelation(ctx, correlation)
	if err != nil {
		return false
	}
	for _, t := range all {
		if t.Status != "leased" || t.ActiveLease == nil {
			continue
		}
		if _, err = o.assignments.ResolveAssignment(ctx, t.ID, t.ActiveLease.AttemptID, t.AssignedRoleID); err == nil {
			return true
		}
	}
	return false
}

func (o *Orchestrator) localLease(taskID int64) (LeaseRecord, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	l, ok := o.leases[taskID]
	return l, ok
}
func (o *Orchestrator) rememberLease(taskID int64, l LeaseRecord) {
	o.mu.Lock()
	o.leases[taskID] = l
	o.mu.Unlock()
}
func (o *Orchestrator) forgetLease(taskID int64) {
	o.mu.Lock()
	delete(o.leases, taskID)
	o.mu.Unlock()
}

func correlationID(r SubmitRequest) string {
	body, _ := json.Marshal(r.Goal)
	sum := sha256.Sum256(append([]byte(r.IdempotencyKey+"\x00"), body...))
	return "executive:" + hex.EncodeToString(sum[:16])
}
func childKey(rootID int64, suffix string) string {
	return "executive:" + strconv.FormatInt(rootID, 10) + ":" + suffix
}
func taskCausation(id int64) string { return "task:" + strconv.FormatInt(id, 10) }
func attemptCausation(taskID, attemptID int64) string {
	return fmt.Sprintf("task:%d:attempt:%d", taskID, attemptID)
}
func withoutRoot(all []TaskRecord, id int64) []TaskRecord {
	out := make([]TaskRecord, 0, len(all))
	for _, t := range all {
		if t.ID != id {
			out = append(out, t)
		}
	}
	return out
}
func findTaskByMarker(all []TaskRecord, marker string) (TaskRecord, bool) {
	for _, t := range all {
		if strings.Contains(t.IdempotencyKey, marker) {
			return t, true
		}
	}
	return TaskRecord{}, false
}
func findTaskByKey(all []TaskRecord, key string) (TaskRecord, bool) {
	for _, t := range all {
		if t.IdempotencyKey == key {
			return t, true
		}
	}
	return TaskRecord{}, false
}
func isTerminalTask(s string) bool {
	switch s {
	case "completed", "no_action", "failed", "dead_letter", "rejected", "cancelled":
		return true
	default:
		return false
	}
}
func latestFinishedAttemptID(attempts []AttemptRecord) int64 {
	var id int64
	var ordinal int
	for _, a := range attempts {
		if a.State == "finished" && a.Ordinal >= ordinal {
			ordinal = a.Ordinal
			id = a.ID
		}
	}
	return id
}
func resultRequirementID(reqs []RequirementRecord) int64 {
	for _, r := range reqs {
		if r.Required && r.Type == "result" && (r.Key == "typed_plan" || r.Key == "typed_review" || r.Key == "typed_closure" || r.Key == "model_result" || r.Key == "executive_closure_verified" || r.Key == "typed_adversarial_review" || r.Key == "typed_design_adjudication" || r.Key == "typed_implementation_plan") {
			return r.ID
		}
	}
	return 0
}
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
func boundedJSON(v any, max int) string {
	b, _ := json.Marshal(v)
	if len(b) <= max {
		return string(b)
	}
	return `{"bounded":true,"detail":"summary exceeded configured byte budget"}`
}

func departmentWorkerTasks(all []TaskRecord, rootID int64, dept string) []TaskRecord {
	prefix := childKey(rootID, "worker:"+dept+":")
	out := []TaskRecord{}
	for _, t := range all {
		if strings.HasPrefix(t.IdempotencyKey, prefix) {
			out = append(out, t)
		}
	}
	return out
}

// allDepartmentWorkersTerminal reports whether the department's workers have
// FINISHED, which is not the same as whether they succeeded.
//
// It used to require every worker to be completed or no_action, so a single
// failed or dead-lettered worker held the department open forever: the review
// that would have judged the department never ran, and the run waited on a
// task that was never coming back. One worker's failure took down its
// siblings' whole phase.
//
// A failure belongs to the worker that had it. What the department does about
// it is the reviewer's judgement, and the reviewer is equipped to make it --
// the summary it receives carries each worker's status, so a failed sibling
// is evidence it can weigh, and its verdict already has the vocabulary to
// respond: needs_replan to try again, blocked or fail to stop. Holding the
// phase open instead denies the review the chance to say any of that.
//
// Only terminal states count. A worker in retry_wait has not finished and
// still holds the phase open, which is correct: it is coming back. A blocked
// worker holds it open too, blocked being the state that asks for a human.
func allDepartmentWorkersTerminal(all []TaskRecord, rootID int64, dept string) bool {
	tasks := departmentWorkerTasks(all, rootID, dept)
	if len(tasks) == 0 {
		return true
	}
	for _, t := range tasks {
		if !isTerminalTask(t.Status) {
			return false
		}
	}
	return true
}
func latestReviewTask(all []TaskRecord, rootID int64, dept string) (TaskRecord, bool) {
	prefix := childKey(rootID, "leader-review:"+dept)
	var found TaskRecord
	ok := false
	best := -1
	for _, t := range all {
		if !strings.HasPrefix(t.IdempotencyKey, prefix) {
			continue
		}
		n := reviewReplanOrdinal(t.IdempotencyKey)
		if !ok || n > best {
			found = t
			ok = true
			best = n
		}
	}
	return found, ok
}
func reviewReplanOrdinal(key string) int {
	marker := ":replan:"
	idx := strings.LastIndex(key, marker)
	if idx < 0 {
		return 0
	}
	n, _ := strconv.Atoi(key[idx+len(marker):])
	return n
}

func boundedDepartmentSummary(all []TaskRecord, rootID int64, dept string, max int) string {
	type item struct {
		ID     int64  `json:"task_id"`
		Role   string `json:"role"`
		Status string `json:"status"`
		Result string `json:"result_summary,omitempty"`
	}
	items := []item{}
	for _, t := range departmentWorkerTasks(all, rootID, dept) {
		summary := ""
		for _, a := range t.Attempts {
			if a.State == "finished" {
				summary = "verified result recorded"
			}
		}
		items = append(items, item{t.ID, t.AssignedRoleID, t.Status, summary})
	}
	return boundedJSON(map[string]any{"department_id": dept, "tasks": items}, max)
}
func boundedClosureSummary(plan ExecutivePlan, all []TaskRecord, rootID int64, max int) string {
	type item struct {
		Department string `json:"department"`
		Status     string `json:"review_status"`
		TaskID     int64  `json:"review_task_id,omitempty"`
	}
	items := []item{}
	for _, d := range plan.DepartmentRequests {
		review, ok := latestReviewTask(all, rootID, d.UnitID)
		if ok {
			items = append(items, item{d.UnitID, review.Status, review.ID})
		} else {
			items = append(items, item{Department: d.UnitID, Status: "missing"})
		}
	}
	return boundedJSON(map[string]any{"objective": plan.Objective, "success_criteria": plan.SuccessCriteria, "departments": items, "owner_decisions_required": plan.OwnerDecisionsRequired}, max)
}
