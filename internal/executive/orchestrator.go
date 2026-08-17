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
		deps.Budget == nil || deps.Completion == nil || deps.Decisions == nil || deps.Authorization == nil {
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
		harness: deps.Harness, budget: deps.Budget, completion: deps.Completion, decisions: deps.Decisions, validator: validator, limits: limits,
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
	if err := validateStrings(request.Goal.AcceptanceCriteria, o.limits, "acceptance_criteria"); err != nil {
		return Run{}, false, err
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
	correlation := correlationID(request)
	root, reused, err := o.tasks.CreateTask(ctx, CreateTaskCommand{
		RequestedByRoleID:  OwnerRoleID,
		AssignedRoleID:     CEORoleID,
		IdempotencyKey:     request.IdempotencyKey,
		Title:              "Executive owner goal",
		Instructions:       request.Goal.Goal,
		AcceptanceCriteria: append([]string(nil), request.Goal.AcceptanceCriteria...),
		Priority:           100,
		MaxAttempts:        2,
		CorrelationID:      correlation,
		CausationID:        "owner:" + request.IdempotencyKey,
		Requirements:       requirements,
	})
	if err != nil {
		return Run{}, false, err
	}
	if o.budgets != nil {
		if err := o.budgets.CreateRootBudget(ctx, root, o.clock.Now()); err != nil {
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

func (o *Orchestrator) createCEOPlanTask(ctx context.Context, root TaskRecord) (TaskRecord, bool, error) {
	task, reused, err := o.tasks.CreateTask(ctx, CreateTaskCommand{
		RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID,
		IdempotencyKey: childKey(root.ID, "ceo-plan"),
		Title:          "CEO executive planning", Instructions: "Produce only the ExecutivePlan JSON contract for the owner goal. Propose operational departments; do not select providers, models, capabilities, tools, authority, credentials, or egress.",
		AcceptanceCriteria: []string{"Return one strict ExecutivePlan JSON value", "Use only operational registry unit IDs", "Do not grant authority or capabilities"},
		Priority:           100, MaxAttempts: 3, CorrelationID: root.CorrelationID, CausationID: taskCausation(root.ID),
		Requirements: []RequirementProposal{{Key: "typed_plan", Type: "result", Description: "Validated ExecutivePlan invocation result", Required: true}},
	})
	if err != nil {
		return TaskRecord{}, false, err
	}
	if err := o.attachChildCoordination(ctx, root, root, task, 1); err != nil {
		return TaskRecord{}, false, err
	}
	return task, reused, nil
}

func (o *Orchestrator) createLeaderPlanTask(ctx context.Context, root TaskRecord, req DepartmentRequest, leader RoleRef) (TaskRecord, bool, error) {
	instructions := boundedJSON(map[string]any{"department_id": req.UnitID, "objective": req.Objective, "deliverable": req.Deliverable, "constraints": req.Constraints, "priority": req.Priority}, o.limits.MaxInstructionsBytes)
	task, reused, err := o.tasks.CreateTask(ctx, CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: leader.ID,
		IdempotencyKey: childKey(root.ID, "leader-plan:"+req.UnitID), Title: "Department planning: " + req.UnitID,
		Instructions:       "Produce only DepartmentPlan JSON for this bounded request: " + instructions,
		AcceptanceCriteria: []string{"Return one strict DepartmentPlan JSON value", "Delegate only to active assignable roles in this department", "Use only existing requirement types"},
		Priority:           req.Priority, MaxAttempts: 3, CorrelationID: root.CorrelationID, CausationID: taskCausation(root.ID),
		Requirements: []RequirementProposal{{Key: "typed_plan", Type: "result", Description: "Validated DepartmentPlan invocation result", Required: true}},
	})
	if err != nil {
		return TaskRecord{}, false, err
	}
	if err := o.attachChildCoordination(ctx, root, root, task, 2); err != nil {
		return TaskRecord{}, false, err
	}
	return task, reused, nil
}

// attachChildCoordination inherits child's budget from root's tree and
// sends a durable delegation message from sender to child, when the
// orchestrator has those optional providers configured. Both are
// independently optional and independently best-effort against
// already-created tasks: a budget/messaging failure here must not undo a
// task creation that already durably happened, so it is surfaced as a
// wrapped error for the caller to decide on, not silently swallowed.
func (o *Orchestrator) attachChildCoordination(ctx context.Context, root, sender, child TaskRecord, depth int64) error {
	now := o.clock.Now()
	if o.budgets != nil {
		if err := o.budgets.InheritForChild(ctx, root, child, depth, now); err != nil {
			return fmt.Errorf("inherit agent budget for task %d: %w", child.ID, err)
		}
	}
	// A role creating its own sub-task (e.g. the CEO's root task spawning its
	// own "CEO planning" task -- both AssignedRoleID==CEORoleID) crosses no
	// organizational trust boundary: nothing is being delegated to a
	// different actor, so there is nothing for agent-messaging to
	// authenticate. agentmessaging's own topology validator enforces this as
	// a hard invariant (ValidateEdge denies senderRole==recipientRole
	// unconditionally, see internal/agentmessaging/topology.go) -- this is
	// not a gap to route around, it is the reason a same-role hop must never
	// reach SendDelegation in the first place.
	if o.messages != nil && sender.AssignedRoleID != child.AssignedRoleID {
		// FIX 6 (EXEC-PRINCIPAL-001): no principal is configured on the
		// Orchestrator itself anymore -- AgentMessagingProvider resolves the
		// correct, role-bound principal internally from sender.AssignedRoleID.
		// A single static principal here could not authenticate more than one
		// sender role across a multi-hop flow (CEO->leader, leader->worker,
		// worker->leader, leader->CEO); see runtimeadapter.AgentMessages.
		if err := o.messages.SendDelegation(ctx, sender, child, now); err != nil {
			return fmt.Errorf("send delegation message for task %d: %w", child.ID, err)
		}
	}
	return nil
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

func (o *Orchestrator) materializeWorkerTasks(ctx context.Context, root, source TaskRecord, departmentID string, proposals []WorkerTaskProposal, replan int) error {
	created := map[string]TaskRecord{}
	for _, p := range proposals {
		suffix := "worker:" + departmentID + ":" + p.ClientKey
		if replan > 0 {
			suffix += "-replan:" + strconv.Itoa(replan)
		}
		t, _, err := o.tasks.CreateTask(ctx, CreateTaskCommand{RequestedByRoleID: source.AssignedRoleID, AssignedRoleID: p.AssignedRoleID, IdempotencyKey: childKey(root.ID, suffix), Title: p.Title, Instructions: p.Instructions, AcceptanceCriteria: p.AcceptanceCriteria, Priority: p.Priority, MaxAttempts: 3, CorrelationID: root.CorrelationID, CausationID: taskCausation(source.ID), Requirements: appendResultRequirement(p.Requirements)})
		if err != nil {
			return err
		}
		if err := o.attachChildCoordination(ctx, root, source, t, 3); err != nil {
			return err
		}
		created[p.ClientKey] = t
	}
	for _, p := range proposals {
		for _, dep := range p.Dependencies {
			if err := o.tasks.AddDependency(ctx, created[p.ClientKey].ID, created[dep].ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func appendResultRequirement(in []RequirementProposal) []RequirementProposal {
	out := append([]RequirementProposal(nil), in...)
	for _, r := range out {
		if r.Key == "model_result" {
			return out
		}
	}
	return append(out, RequirementProposal{Key: "model_result", Type: "result", Description: "Validated durable model invocation result", Required: true})
}

func (o *Orchestrator) createReviewTask(ctx context.Context, root TaskRecord, req DepartmentRequest, leader RoleRef, all []TaskRecord, replan int) (TaskRecord, bool, error) {
	summary := boundedDepartmentSummary(all, root.ID, req.UnitID, o.limits.MaxInstructionsBytes)
	suffix := "leader-review:" + req.UnitID
	if replan > 0 {
		suffix += ":replan:" + strconv.Itoa(replan)
	}
	task, reused, err := o.tasks.CreateTask(ctx, CreateTaskCommand{RequestedByRoleID: CEORoleID, AssignedRoleID: leader.ID, IdempotencyKey: childKey(root.ID, suffix), Title: "Department review: " + req.UnitID, Instructions: "Review only this bounded durable task/evidence summary and return DepartmentReview JSON: " + summary, AcceptanceCriteria: []string{"Use only durable task states and evidence refs", "Return strict DepartmentReview JSON", "Do not execute tool intents"}, Priority: req.Priority, MaxAttempts: 3, CorrelationID: root.CorrelationID, CausationID: taskCausation(root.ID), Requirements: []RequirementProposal{{Key: "typed_review", Type: "result", Description: "Validated DepartmentReview invocation result", Required: true}}})
	if err != nil {
		return TaskRecord{}, false, err
	}
	if err := o.attachChildCoordination(ctx, root, root, task, 2); err != nil {
		return TaskRecord{}, false, err
	}
	return task, reused, nil
}

func (o *Orchestrator) createClosureTask(ctx context.Context, root TaskRecord, plan ExecutivePlan, all []TaskRecord) (TaskRecord, bool, error) {
	summary := boundedClosureSummary(plan, all, root.ID, o.limits.MaxInstructionsBytes)
	task, reused, err := o.tasks.CreateTask(ctx, CreateTaskCommand{RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, IdempotencyKey: childKey(root.ID, "ceo-closure"), Title: "CEO executive closure", Instructions: "Synthesize only from this bounded durable summary and return ExecutiveClosure JSON. A completed claim cannot override backend verification: " + summary, AcceptanceCriteria: []string{"Return strict ExecutiveClosure JSON", "Cite only supplied evidence refs", "Report blockers and unresolved owner decisions explicitly"}, Priority: 100, MaxAttempts: 3, CorrelationID: root.CorrelationID, CausationID: taskCausation(root.ID), Requirements: []RequirementProposal{{Key: "typed_closure", Type: "result", Description: "Validated ExecutiveClosure invocation result", Required: true}}})
	if err != nil {
		return TaskRecord{}, false, err
	}
	if err := o.attachChildCoordination(ctx, root, root, task, 1); err != nil {
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
		claimed, attempt, l, err := o.tasks.ClaimTask(ctx, ClaimTaskCommand{
			TaskID: task.ID, WorkerID: orchestratorWorkerID, HolderPrincipalID: principal.ID,
			AssignedRoleID: task.AssignedRoleID, LeaseDuration: executiveLeaseTTL,
		})
		if err != nil {
			return task, err
		}
		task = claimed
		lease = l
		o.rememberLease(task.ID, lease)
		haveLease = true
		assignment, err := o.assignments.ResolveAssignment(ctx, task.ID, attempt.ID, task.AssignedRoleID)
		if err != nil {
			_, _ = o.tasks.BlockTask(ctx, root.ID, "dispatch_assignment_required", fmt.Sprintf("task=%d attempt=%d subject_role=%s lease_expires_at=%s", task.ID, attempt.ID, task.AssignedRoleID, lease.ExpiresAt.UTC().Format(time.RFC3339)), "service", orchestratorWorkerID)
			return task, ErrDispatchAssignmentRequired
		}
		if assignment.OrganizationRevisionID != task.OrganizationRevisionID || assignment.SubjectRoleID != task.AssignedRoleID {
			return task, fmt.Errorf("%w: dispatch assignment scope mismatch", ErrRegistryMismatch)
		}
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
		assignment, err := o.assignments.ResolveAssignment(ctx, task.ID, lease.AttemptID, task.AssignedRoleID)
		if err != nil {
			return task, ErrDispatchAssignmentRequired
		}
		if assignment.OrganizationRevisionID != task.OrganizationRevisionID {
			return task, fmt.Errorf("%w: assignment revision drift", ErrRegistryMismatch)
		}
		if _, err = o.tasks.StartAttempt(ctx, lease, actorID); err != nil {
			return task, err
		}
		task, err = o.tasks.GetTask(ctx, task.ID)
		if err != nil {
			return task, err
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
		return o.failAttempt(ctx, task, lease, actorID, "harness_execution_failed", execErr.Error(), ErrCompletionFailed)
	}
	if err = outcome.Validate(); err != nil {
		return task, err
	}
	if outcome.Status == HarnessRunSucceeded {
		return o.recordHarnessSuccess(ctx, task, lease, actorID, outcome, validate)
	}
	return o.handleHarnessFailure(ctx, root, task, lease, actorID, outcome)
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

func (o *Orchestrator) failAttempt(ctx context.Context, task TaskRecord, lease LeaseRecord, actorID, code, detail string, sentinel error) (TaskRecord, error) {
	failed, recErr := o.tasks.RecordAttemptFailed(ctx, lease, actorID, code, truncate(detail, 2000), false)
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
		return task, err
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

// handleHarnessFailure maps one Harness verdict onto existing Executive task
// semantics. There is deliberately no new state machine here: every branch
// lands on a status the Executive already had.
func (o *Orchestrator) handleHarnessFailure(ctx context.Context, root, task TaskRecord, lease LeaseRecord, actorID string, outcome HarnessRunOutcome) (TaskRecord, error) {
	switch outcome.Failure {
	case HarnessFailureAuthorityUnavailable:
		// Not a denial and not terminal: the Harness wrote nothing, the
		// attempt keeps its lease, and the same run identity resumes on the
		// next tick. Failing the attempt here would turn an outage into a
		// durable statement about the principal.
		return task, fmt.Errorf("%w: %s", ErrExecutionAuthorityUnavailable, outcome.TerminationReason)
	case HarnessFailureAuthorizationDenied:
		return o.failAttempt(ctx, task, lease, actorID, "execution_authority_denied", outcome.TerminationReason, ErrExecutionAuthorityDenied)
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
		return o.failAttempt(ctx, task, lease, actorID, "harness_limit_reached", outcome.TerminationReason, ErrBudgetExceeded)
	case HarnessFailureIdentityDrift:
		return o.failAttempt(ctx, task, lease, actorID, "harness_identity_drift", outcome.TerminationReason, ErrRunIdentityDrift)
	case HarnessFailureCancelled:
		if handled, blocked, err := o.ambiguityGuard(ctx, root, task, lease.AttemptID); handled {
			return blocked, err
		}
		return task, fmt.Errorf("%w: %s", ErrExecutionInterrupted, outcome.TerminationReason)
	case HarnessFailureHistoryError:
		if handled, blocked, err := o.ambiguityGuard(ctx, root, task, lease.AttemptID); handled {
			return blocked, err
		}
		return o.failAttempt(ctx, task, lease, actorID, "harness_history_error", outcome.TerminationReason, ErrHarnessHistoryFailed)
	case HarnessFailureModelError:
		if handled, blocked, err := o.ambiguityGuard(ctx, root, task, lease.AttemptID); handled {
			return blocked, err
		}
		return o.failAttempt(ctx, task, lease, actorID, "model_invocation_failed", outcome.TerminationReason, ErrCompletionFailed)
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
		errors.Is(err, ErrExecutionInterrupted):
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
		if r.Required && r.Type == "result" && (r.Key == "typed_plan" || r.Key == "typed_review" || r.Key == "typed_closure" || r.Key == "model_result" || r.Key == "executive_closure_verified") {
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
func allDepartmentWorkersTerminal(all []TaskRecord, rootID int64, dept string) bool {
	tasks := departmentWorkerTasks(all, rootID, dept)
	if len(tasks) == 0 {
		return true
	}
	for _, t := range tasks {
		if t.Status != "completed" && t.Status != "no_action" {
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
