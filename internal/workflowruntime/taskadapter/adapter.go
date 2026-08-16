// Package taskadapter maps the V2 workflow task port onto internal/tasks.
// It contains no persistence of its own.
package taskadapter

import (
	"context"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"github.com/Mireuz13/explorarte-organization/internal/workflowruntime"
)

type Adapter struct {
	Service *tasks.Service
}

func New(service *tasks.Service) (*Adapter, error) {
	if service == nil {
		return nil, workflowruntime.ErrInvalidRequest
	}
	return &Adapter{Service: service}, nil
}

func (a *Adapter) Initiate(ctx context.Context, work workflowruntime.WorkRequest, actor workflowruntime.Actor) (workflowruntime.Snapshot, bool, error) {
	requirements := make([]tasks.RequirementSpec, 0, len(work.Requirements))
	for _, requirement := range work.Requirements {
		required := requirement.Required
		requirements = append(requirements, tasks.RequirementSpec{
			Key: requirement.Key, Type: tasks.RequirementType(requirement.Type), Description: requirement.Description, Required: &required,
		})
	}
	value, created, err := a.Service.CreateTask(ctx, tasks.CreateRequest{
		OrganizationID: work.OrganizationID, RequestedByRoleID: work.RequestedByRoleID,
		AssignedRoleID: work.AssignedRoleID, IdempotencyKey: work.IdempotencyKey,
		Title: work.Title, Instructions: work.Instructions,
		AcceptanceCriteria: append([]string(nil), work.AcceptanceCriteria...), Priority: work.Priority,
		MaxAttempts: work.MaxAttempts, CorrelationID: work.CorrelationID, CausationID: work.CausationID,
		Dependencies: append([]int64(nil), work.Dependencies...), Requirements: requirements,
	}, actor.ActorType, actor.DurableActorID())
	if err != nil {
		return workflowruntime.Snapshot{}, false, err
	}
	snapshot, err := a.Observe(ctx, value.ID)
	return snapshot, !created, err
}

func (a *Adapter) Observe(ctx context.Context, taskID int64) (workflowruntime.Snapshot, error) {
	detail, err := a.Service.GetTask(ctx, taskID)
	if err != nil {
		return workflowruntime.Snapshot{}, err
	}
	events, err := a.Service.ListEvents(ctx, taskID, 5000)
	if err != nil {
		return workflowruntime.Snapshot{}, err
	}
	return mapSnapshot(detail, events), nil
}

func (a *Adapter) StartExecution(ctx context.Context, command workflowruntime.ExecutionCommand) (workflowruntime.Snapshot, error) {
	_, err := a.Service.StartAttempt(ctx, tasks.LeaseCommand{
		TaskID: command.TaskID, AttemptID: command.AttemptID, LeaseToken: command.LeaseToken, ActorID: command.Actor.DurableActorID(),
	})
	if err != nil {
		return workflowruntime.Snapshot{}, err
	}
	return a.Observe(ctx, command.TaskID)
}

func (a *Adapter) RecordOutcome(ctx context.Context, command workflowruntime.OutcomeCommand) (workflowruntime.Snapshot, error) {
	_, err := a.Service.RecordAttemptResult(ctx, tasks.RecordAttemptResultCommand{
		LeaseCommand: tasks.LeaseCommand{TaskID: command.TaskID, AttemptID: command.AttemptID, LeaseToken: command.LeaseToken, ActorID: command.Actor.DurableActorID()},
		Result:       tasks.AttemptResult{Outcome: tasks.AttemptOutcome(command.Outcome), Summary: command.Summary, FailureCode: command.FailureCode},
	})
	if err != nil {
		return workflowruntime.Snapshot{}, err
	}
	return a.Observe(ctx, command.TaskID)
}

func (a *Adapter) RecordEvidence(ctx context.Context, command workflowruntime.EvidenceCommand) (workflowruntime.Snapshot, error) {
	var requirementID *int64
	if command.RequirementID > 0 {
		value := command.RequirementID
		requirementID = &value
	}
	_, err := a.Service.RecordEvidence(ctx, tasks.RecordEvidenceCommand{
		TaskID: command.TaskID, RequirementID: requirementID, Type: tasks.RequirementType(command.Type),
		Reference: command.Reference, Digest: command.Digest, RecordedBy: command.Actor.DurableActorID(),
		Metadata: command.Metadata, Satisfies: command.Satisfies,
	})
	if err != nil {
		return workflowruntime.Snapshot{}, err
	}
	return a.Observe(ctx, command.TaskID)
}

func (a *Adapter) FinalizeCompleted(ctx context.Context, command workflowruntime.CompleteCommand) (workflowruntime.Snapshot, error) {
	_, err := a.Service.FinalizeTask(ctx, tasks.FinalizeCommand{
		TaskID: command.TaskID, Outcome: tasks.FinalCompleted, ActorType: command.Actor.ActorType, ActorID: command.Actor.DurableActorID(),
	})
	if err != nil {
		return workflowruntime.Snapshot{}, err
	}
	return a.Observe(ctx, command.TaskID)
}

func (a *Adapter) Block(ctx context.Context, request workflowruntime.BranchRequest, action workflowruntime.BranchAction) (workflowruntime.Snapshot, error) {
	_, err := a.Service.BlockTask(ctx, tasks.BlockCommand{
		TaskID: request.TaskID, ReasonCode: action.ReasonCode, Reason: action.Reason,
		ActorType: request.Actor.ActorType, ActorID: request.Actor.DurableActorID(),
	})
	if err != nil {
		return workflowruntime.Snapshot{}, err
	}
	return a.Observe(ctx, request.TaskID)
}

func mapSnapshot(detail tasks.TaskDetail, events []tasks.Event) workflowruntime.Snapshot {
	requestedBy := ""
	if detail.Task.RequestedByRoleID != nil {
		requestedBy = *detail.Task.RequestedByRoleID
	}
	correlationID, causationID := "", ""
	if detail.Task.CorrelationID != nil {
		correlationID = *detail.Task.CorrelationID
	}
	if detail.Task.CausationID != nil {
		causationID = *detail.Task.CausationID
	}
	out := workflowruntime.Snapshot{
		TaskID: detail.Task.ID, OrganizationID: detail.Task.OrganizationID, Status: workflowruntime.Status(detail.Task.Status),
		AssignedRoleID: detail.Task.AssignedRoleID, AssignedUnitID: detail.Task.AssignedUnitID,
		RequestedByRoleID: requestedBy, CorrelationID: correlationID, CausationID: causationID,
	}
	for _, requirement := range detail.Requirements {
		out.Requirements = append(out.Requirements, workflowruntime.Requirement{
			ID: requirement.ID, Key: requirement.Key, Type: string(requirement.Type), Description: requirement.Description,
			Required: requirement.Required, Status: string(requirement.Status),
		})
	}
	for _, evidence := range detail.Evidence {
		requirementID := int64(0)
		if evidence.RequirementID != nil {
			requirementID = *evidence.RequirementID
		}
		out.Evidence = append(out.Evidence, workflowruntime.Evidence{
			ID: evidence.ID, RequirementID: requirementID, Type: string(evidence.Type), Reference: evidence.Reference,
			Digest: valueOrEmpty(evidence.Digest), RecordedBy: evidence.RecordedBy, Metadata: evidence.Metadata, CreatedAt: evidence.CreatedAt,
		})
	}
	for _, attempt := range detail.Attempts {
		out.Attempts = append(out.Attempts, workflowruntime.Attempt{ID: attempt.ID, Ordinal: attempt.Ordinal, State: string(attempt.State)})
	}
	for _, event := range events {
		mapped := workflowruntime.DurableEvent{
			ID: event.ID, Sequence: event.Sequence, EventType: event.EventType, ActorType: event.ActorType,
			ActorID: event.ActorID, CorrelationID: valueOrEmpty(event.CorrelationID), CausationID: valueOrEmpty(event.CausationID), OccurredAt: event.OccurredAt,
		}
		if event.FromStatus != nil {
			mapped.FromStatus = workflowruntime.Status(*event.FromStatus)
		}
		if event.ToStatus != nil {
			mapped.ToStatus = workflowruntime.Status(*event.ToStatus)
		}
		out.Events = append(out.Events, mapped)
	}
	if len(out.Events) > 0 {
		last := out.Events[len(out.Events)-1]
		out.LastTransition = &last
	}
	return out
}

func valueOrEmpty[T ~string](value *T) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

var _ workflowruntime.TaskPort = (*Adapter)(nil)
