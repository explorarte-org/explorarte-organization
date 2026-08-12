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

const orchestratorWorkerID = "executive-orchestrator"

type Orchestrator struct {
	organizationID string
	registry       RegistryResolver
	tasks          TaskCoordinator
	contexts       ContextCoordinator
	assignments    DispatchProvisioner
	models         ModelCoordinator
	completion     CompletionGate
	decisions      DecisionRecorder
	validator      *Validator
	limits         Limits
	clock          Clock
	budgets        AgentBudgetProvider
	messages       AgentMessagingProvider
	principalKey   string // Pre-resolved execution principal key for messaging operations

	mu     sync.Mutex
	leases map[int64]LeaseRecord
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

// WithExecutionPrincipal sets the configured execution principal key for
// authenticating agent messaging operations. This prevents spoofing - the
// principal must be resolved from the authenticated identity, not from
// sender role/task data.
func WithExecutionPrincipal(principalKey string) OrchestratorOption {
	return func(o *Orchestrator) { o.principalKey = strings.TrimSpace(principalKey) }
}

func NewOrchestrator(organizationID string, registry RegistryResolver, tasks TaskCoordinator, contexts ContextCoordinator, assignments DispatchProvisioner, models ModelCoordinator, completion CompletionGate, decisions DecisionRecorder, authz AuthorizationGate, limits Limits, clock Clock, opts ...OrchestratorOption) (*Orchestrator, error) {
	if strings.TrimSpace(organizationID) == "" || registry == nil || tasks == nil || contexts == nil || assignments == nil || models == nil || completion == nil || decisions == nil || authz == nil {
		return nil, errors.New("executive orchestrator dependencies are incomplete")
	}
	if limits.MaxDepartments <= 0 {
		limits = DefaultLimits()
	}
	if clock == nil {
		clock = ClockFunc(time.Now)
	}
	validator, err := NewValidator(registry, authz, limits)
	if err != nil {
		return nil, err
	}
	orchestrator := &Orchestrator{organizationID: strings.TrimSpace(organizationID), registry: registry, tasks: tasks, contexts: contexts, assignments: assignments, models: models, completion: completion, decisions: decisions, validator: validator, limits: limits, clock: clock, leases: map[int64]LeaseRecord{}}
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
		if _, err = o.driveTypedTask(ctx, root, planTask, executivePlanOutputSchema, "executive_ceo_plan", func(result InvocationResult) error {
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
		if _, err = o.driveTypedTask(ctx, root, closureTask, executiveClosureOutputSchema, "executive_ceo_closure", func(result InvocationResult) error {
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
	if o.messages != nil {
		// FIX 6: Use the configured execution principal key resolved from
		// authenticated identity (NOT from sender/task data).
		// CRITICAL: If messaging is wired but no principal is configured, FAIL explicitly.
		// NO silent skipping — that would allow tasks to propagate without coordinated messaging,
		// potentially creating orphaned state machines or inconsistent coordination.
		executionPrincipalID := o.principalKey
		if executionPrincipalID == "" {
			// Explicit fail: messaging is enabled in wiring but authentication identity not configured.
			// This is a configuration error, NOT a "skip" scenario.
			return fmt.Errorf("agent messaging wired but no execution principal configured "+
				"(ORG_MODEL_EXECUTION_PRINCIPAL_KEY or WithExecutionPrincipal required): "+
				"cannot delegate task %d without authenticated principal — safe-fail, not silent-skip", child.ID)
		}
		if err := o.messages.SendDelegation(ctx, executionPrincipalID, sender, child, now); err != nil {
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
			_, err = o.driveTypedTask(ctx, root, planTask, departmentPlanOutputSchema, "department_plan", func(result InvocationResult) error {
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
			_, e = o.driveTypedTask(ctx, root, wt, workerResultOutputSchema, "department_worker", func(result InvocationResult) error {
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
			_, e = o.driveTypedTask(ctx, root, reviewTask, departmentReviewOutputSchema, "department_review", func(result InvocationResult) error {
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

func (o *Orchestrator) driveTypedTask(ctx context.Context, root TaskRecord, task TaskRecord, schema json.RawMessage, purpose string, validate func(InvocationResult) error) (TaskRecord, error) {
	if task.Status == "completed" {
		return task, nil
	}
	if task.Status == "awaiting_verification" {
		return o.gatedComplete(ctx, task)
	}
	lease, haveLease := o.localLease(task.ID)
	if task.Status == "ready" {
		claimed, attempt, l, err := o.tasks.ClaimTask(ctx, task.ID, orchestratorWorkerID, task.AssignedRoleID, 5*time.Minute)
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
		if _, err = o.tasks.StartAttempt(ctx, lease, orchestratorWorkerID); err != nil {
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
	invocations, err := o.models.FindTaskAttemptInvocations(ctx, task.ID, lease.AttemptID)
	if err != nil {
		return task, err
	}
	if len(invocations) > 1 {
		return task, fmt.Errorf("%w: multiple invocations for one task attempt", ErrContractRejected)
	}
	var invocation InvocationRecord
	if len(invocations) == 0 {
		snapshotID, buildErr := o.contexts.Build(ctx, ContextRequest{OrganizationRevisionID: task.OrganizationRevisionID, ActorRoleID: task.AssignedRoleID, Purpose: purpose, TaskRef: "task:" + strconv.FormatInt(task.ID, 10), IdempotencyKey: childKey(root.ID, fmt.Sprintf("context:%d:%d", task.ID, lease.AttemptID)), CorrelationID: root.CorrelationID, CausationID: attemptCausation(task.ID, lease.AttemptID)})
		if buildErr != nil {
			return task, buildErr
		}
		invocation, _, err = o.models.EnsureInvocation(ctx, InvocationCommand{TaskID: task.ID, AttemptID: lease.AttemptID, SubjectRoleID: task.AssignedRoleID, ContextSnapshotID: snapshotID, Purpose: purpose, OutputSchema: schema, MaxOutputTokens: o.limits.MaxOutputTokens, IdempotencyKey: childKey(root.ID, fmt.Sprintf("invocation:%d:%d", task.ID, lease.AttemptID)), CorrelationID: root.CorrelationID, CausationID: attemptCausation(task.ID, lease.AttemptID), Deadline: o.clock.Now().Add(o.limits.InvocationDeadline)})
		if err != nil {
			return task, err
		}
	} else {
		invocation = invocations[0]
	}
	switch invocation.Status {
	case "requested", "claimed", "send_started", "response_received":
		if updated, hbErr := o.tasks.Heartbeat(ctx, lease, orchestratorWorkerID, 5*time.Minute); hbErr == nil {
			o.rememberLease(task.ID, updated)
		}
		return task, nil
	case "ambiguous":
		_, _ = o.tasks.BlockTask(ctx, root.ID, "model_outcome_ambiguous", fmt.Sprintf("task=%d attempt=%d invocation=%d requires explicit inspection", task.ID, lease.AttemptID, invocation.ID), "service", orchestratorWorkerID)
		return task, ErrModelOutcomeAmbiguous
	case "failed", "cancelled":
		failed, recErr := o.tasks.RecordAttemptFailed(ctx, lease, orchestratorWorkerID, "model_invocation_failed", invocation.ErrorCode, false)
		o.forgetLease(task.ID)
		if recErr != nil {
			return task, recErr
		}
		return failed, fmt.Errorf("%w: model invocation %s", ErrCompletionFailed, invocation.Status)
	case "succeeded":
		result, rErr := o.models.GetResult(ctx, invocation.ID)
		if rErr != nil {
			return task, rErr
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
		if err = validate(result); err != nil {
			return task, err
		}
		reqID := resultRequirementID(task.Requirements)
		if reqID == 0 {
			return task, fmt.Errorf("%w: result requirement missing", ErrContractRejected)
		}
		if err = o.tasks.RecordEvidence(ctx, EvidenceCommand{TaskID: task.ID, RequirementID: reqID, Type: "result", Reference: fmt.Sprintf("model-invocation:%d", invocation.ID), Digest: result.ResponseHash, RecordedBy: orchestratorWorkerID, Metadata: map[string]any{"invocation_id": invocation.ID, "response_bytes": result.ResponseBytes}, Satisfies: true}); err != nil {
			return task, err
		}
		finished, recErr := o.tasks.RecordAttemptSucceeded(ctx, lease, orchestratorWorkerID, "validated model result")
		o.forgetLease(task.ID)
		if recErr != nil {
			return task, recErr
		}
		return o.gatedComplete(ctx, finished)
	default:
		return task, fmt.Errorf("%w: unknown invocation status %s", ErrContractRejected, invocation.Status)
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
	_, _, lease, err := o.tasks.ClaimTask(ctx, root.ID, orchestratorWorkerID, CEORoleID, 2*time.Minute)
	if err != nil {
		return err
	}
	o.rememberLease(root.ID, lease)
	if _, err = o.tasks.StartAttempt(ctx, lease, orchestratorWorkerID); err != nil {
		return err
	}
	finished, err := o.tasks.RecordAttemptSucceeded(ctx, lease, orchestratorWorkerID, "executive closure verified")
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

func (o *Orchestrator) handlePhaseError(ctx context.Context, root, task TaskRecord, err error) (Run, error) {
	if errors.Is(err, ErrDispatchAssignmentRequired) || errors.Is(err, ErrModelOutcomeAmbiguous) {
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
	run, blockErr := o.blockRoot(ctx, root, code, err.Error())
	if blockErr != nil {
		return Run{}, blockErr
	}
	return run, err
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
