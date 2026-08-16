package workflowruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type Runtime struct {
	tasks        TaskPort
	completion   CompletionPort
	decisions    DecisionPort
	coordination CoordinationPort
	executive    ExecutivePort
}

func New(tasks TaskPort, completion CompletionPort, decisions DecisionPort, coordination CoordinationPort, executive ExecutivePort) (*Runtime, error) {
	if tasks == nil || completion == nil || decisions == nil || coordination == nil || executive == nil {
		return nil, errors.New("workflow runtime requires tasks, completion, decisions, coordination, and executive ports")
	}
	return &Runtime{tasks: tasks, completion: completion, decisions: decisions, coordination: coordination, executive: executive}, nil
}

func (r *Runtime) Initiate(ctx context.Context, command InitiateCommand) (Snapshot, bool, error) {
	if err := validateActor(command.Actor); err != nil {
		return Snapshot{}, false, err
	}
	work := command.Work
	if strings.TrimSpace(work.OrganizationID) == "" || work.OrganizationID != command.Actor.OrganizationID ||
		strings.TrimSpace(work.AssignedRoleID) == "" || strings.TrimSpace(work.RequestedByRoleID) == "" ||
		work.RequestedByRoleID != command.Actor.RoleID || strings.TrimSpace(work.CorrelationID) == "" {
		return Snapshot{}, false, ErrTaskBinding
	}
	snapshot, reused, err := r.tasks.Initiate(ctx, work, command.Actor)
	if err != nil {
		return Snapshot{}, false, err
	}
	if err := validateCreatedSnapshot(snapshot, work); err != nil {
		return Snapshot{}, false, err
	}
	return r.enrichCompletion(ctx, snapshot), reused, nil
}

func (r *Runtime) Observe(ctx context.Context, taskID int64) (Snapshot, error) {
	if taskID <= 0 {
		return Snapshot{}, ErrInvalidRequest
	}
	snapshot, err := r.tasks.Observe(ctx, taskID)
	if err != nil {
		return Snapshot{}, err
	}
	return r.enrichCompletion(ctx, snapshot), nil
}

func (r *Runtime) StartExecution(ctx context.Context, command ExecutionCommand) (Snapshot, error) {
	snapshot, err := r.boundTask(ctx, command.Actor, command.TaskID, command.CorrelationID, command.CausationID)
	if err != nil {
		return Snapshot{}, err
	}
	if snapshot.Status.Terminal() {
		return Snapshot{}, ErrTerminalReplay
	}
	value, err := r.tasks.StartExecution(ctx, command)
	if err != nil {
		return Snapshot{}, err
	}
	return r.enrichCompletion(ctx, value), nil
}

func (r *Runtime) RecordOutcome(ctx context.Context, command OutcomeCommand) (Snapshot, error) {
	snapshot, err := r.boundTask(ctx, command.Actor, command.TaskID, command.CorrelationID, command.CausationID)
	if err != nil {
		return Snapshot{}, err
	}
	if snapshot.Status.Terminal() {
		return Snapshot{}, ErrTerminalReplay
	}
	value, err := r.tasks.RecordOutcome(ctx, command)
	if err != nil {
		return Snapshot{}, err
	}
	return r.enrichCompletion(ctx, value), nil
}

func (r *Runtime) RecordEvidence(ctx context.Context, command EvidenceCommand) (Snapshot, error) {
	snapshot, err := r.boundTask(ctx, command.Actor, command.TaskID, command.CorrelationID, command.CausationID)
	if err != nil {
		return Snapshot{}, err
	}
	if snapshot.Status.Terminal() {
		return Snapshot{}, ErrTerminalReplay
	}
	value, err := r.tasks.RecordEvidence(ctx, command)
	if err != nil {
		return Snapshot{}, err
	}
	return r.enrichCompletion(ctx, value), nil
}

func (r *Runtime) Complete(ctx context.Context, command CompleteCommand) (CompleteResult, error) {
	snapshot, err := r.boundTask(ctx, command.Actor, command.TaskID, command.CorrelationID, command.CausationID)
	if err != nil {
		return CompleteResult{}, err
	}
	if snapshot.Status.Terminal() {
		return CompleteResult{}, ErrTerminalReplay
	}
	if snapshot.Status != StatusAwaitingVerification || command.AttemptID <= 0 {
		return CompleteResult{}, ErrCompletionNotReady
	}
	decision, err := r.completion.Verify(ctx, command.TaskID, command.AttemptID)
	if err != nil {
		return CompleteResult{}, err
	}
	if decision.TaskID != command.TaskID || decision.AttemptID != command.AttemptID {
		return CompleteResult{}, ErrTaskBinding
	}
	snapshot.Completion = decision
	if decision.Disposition != CompletionAllow {
		return CompleteResult{Decision: decision, Snapshot: snapshot}, nil
	}
	completed, err := r.tasks.FinalizeCompleted(ctx, command)
	if err != nil {
		return CompleteResult{}, err
	}
	completed.Completion = decision
	return CompleteResult{Decision: decision, Snapshot: completed}, nil
}

func (r *Runtime) Coordinate(ctx context.Context, command CoordinationCommand) (CoordinationRecord, bool, error) {
	if err := validateActor(command.Actor); err != nil {
		return CoordinationRecord{}, false, err
	}
	if command.OrganizationID != command.Actor.OrganizationID || command.SenderTaskID <= 0 || command.RecipientTaskID <= 0 ||
		strings.TrimSpace(command.CorrelationID) == "" || strings.TrimSpace(command.CausationID) == "" || strings.TrimSpace(command.IdempotencyKey) == "" {
		return CoordinationRecord{}, false, ErrInvalidRequest
	}
	sender, err := r.tasks.Observe(ctx, command.SenderTaskID)
	if err != nil {
		return CoordinationRecord{}, false, err
	}
	recipient, err := r.tasks.Observe(ctx, command.RecipientTaskID)
	if err != nil {
		return CoordinationRecord{}, false, err
	}
	if sender.OrganizationID != command.OrganizationID || recipient.OrganizationID != command.OrganizationID ||
		sender.AssignedRoleID != command.Actor.RoleID || sender.CorrelationID != command.CorrelationID || recipient.CorrelationID != command.CorrelationID {
		return CoordinationRecord{}, false, ErrTaskBinding
	}
	if sender.AssignedRoleID == recipient.AssignedRoleID {
		return CoordinationRecord{}, false, ErrSameRoleCoordination
	}
	switch command.Kind {
	case CoordinationDelegation:
		if recipient.CausationID != command.CausationID {
			return CoordinationRecord{}, false, ErrTaskBinding
		}
	case CoordinationCompletion:
		if sender.CausationID != command.CausationID {
			return CoordinationRecord{}, false, ErrTaskBinding
		}
	default:
		return CoordinationRecord{}, false, ErrInvalidRequest
	}
	return r.coordination.Send(ctx, command, sender, recipient)
}

func (r *Runtime) ApplyDecision(ctx context.Context, request BranchRequest) (Snapshot, BranchDecision, error) {
	snapshot, err := r.boundTask(ctx, request.Actor, request.TaskID, request.CorrelationID, request.CausationID)
	if err != nil {
		return Snapshot{}, BranchDecision{}, err
	}
	if snapshot.Status.Terminal() {
		return Snapshot{}, BranchDecision{}, ErrTerminalReplay
	}
	decision, err := r.decisions.Evaluate(ctx, request)
	if err != nil {
		return Snapshot{}, BranchDecision{}, err
	}
	if decision.TaskID != request.TaskID || decision.CorrelationID != request.CorrelationID || decision.CausationID != request.CausationID ||
		strings.TrimSpace(decision.SelectedBranch) == "" || strings.TrimSpace(decision.DecisionRef) == "" {
		return Snapshot{}, BranchDecision{}, ErrDecisionBinding
	}
	if decision.Action.Kind != BranchActionBlock || strings.TrimSpace(decision.Action.ReasonCode) == "" || strings.TrimSpace(decision.Action.Reason) == "" {
		return Snapshot{}, BranchDecision{}, ErrUnsupportedAction
	}
	value, err := r.tasks.Block(ctx, request, decision.Action)
	if err != nil {
		return Snapshot{}, BranchDecision{}, err
	}
	return r.enrichCompletion(ctx, value), decision, nil
}

func (r *Runtime) StartGoal(ctx context.Context, request GoalRequest) (WorkflowStart, error) {
	if err := validateActor(request.Actor); err != nil {
		return WorkflowStart{}, err
	}
	if strings.TrimSpace(request.Goal) == "" || strings.TrimSpace(request.IdempotencyKey) == "" {
		return WorkflowStart{}, ErrInvalidRequest
	}
	started, err := r.executive.Start(ctx, request)
	if err != nil {
		return WorkflowStart{}, err
	}
	snapshot, err := r.tasks.Observe(ctx, started.RootTaskID)
	if err != nil {
		return WorkflowStart{}, err
	}
	if snapshot.OrganizationID != request.Actor.OrganizationID || snapshot.CorrelationID != started.CorrelationID {
		return WorkflowStart{}, ErrTaskBinding
	}
	return WorkflowStart{Executive: started, Snapshot: r.enrichCompletion(ctx, snapshot)}, nil
}

func (r *Runtime) boundTask(ctx context.Context, actor Actor, taskID int64, correlationID, causationID string) (Snapshot, error) {
	if err := validateActor(actor); err != nil || taskID <= 0 || strings.TrimSpace(correlationID) == "" {
		return Snapshot{}, ErrInvalidRequest
	}
	snapshot, err := r.tasks.Observe(ctx, taskID)
	if err != nil {
		return Snapshot{}, err
	}
	if snapshot.OrganizationID != actor.OrganizationID || snapshot.AssignedRoleID != actor.RoleID || snapshot.CorrelationID != correlationID || snapshot.CausationID != causationID {
		return Snapshot{}, ErrTaskBinding
	}
	return snapshot, nil
}

func (r *Runtime) enrichCompletion(ctx context.Context, snapshot Snapshot) Snapshot {
	snapshot.Completion = CompletionDecision{TaskID: snapshot.TaskID, Disposition: CompletionUnchecked}
	if snapshot.Status != StatusAwaitingVerification || len(snapshot.Attempts) == 0 {
		return snapshot
	}
	attemptID := snapshot.Attempts[len(snapshot.Attempts)-1].ID
	decision, err := r.completion.Verify(ctx, snapshot.TaskID, attemptID)
	if err != nil {
		snapshot.Completion = CompletionDecision{
			TaskID: snapshot.TaskID, AttemptID: attemptID, Disposition: CompletionBlocked,
			Reason: "completion verification unavailable: " + err.Error(),
		}
		return snapshot
	}
	if decision.TaskID == snapshot.TaskID && decision.AttemptID == attemptID {
		snapshot.Completion = decision
	}
	return snapshot
}

func validateActor(actor Actor) error {
	if strings.TrimSpace(actor.OrganizationID) == "" || strings.TrimSpace(actor.RoleID) == "" || strings.TrimSpace(actor.DurableActorID()) == "" {
		return ErrInvalidRequest
	}
	return nil
}

func validateCreatedSnapshot(snapshot Snapshot, work WorkRequest) error {
	if snapshot.TaskID <= 0 || snapshot.OrganizationID != work.OrganizationID || snapshot.AssignedRoleID != work.AssignedRoleID ||
		snapshot.CorrelationID != work.CorrelationID || snapshot.CausationID != work.CausationID {
		return fmt.Errorf("%w: initiated task does not match request", ErrTaskBinding)
	}
	return nil
}
