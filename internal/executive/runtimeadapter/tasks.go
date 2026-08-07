package runtimeadapter

import (
	"context"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

const serviceActor = "executive-orchestrator"

type Tasks struct {
	Service        *tasks.Service
	OrganizationID string
}

func (a Tasks) CreateTask(ctx context.Context, command executive.CreateTaskCommand) (executive.TaskRecord, bool, error) {
	requirements := make([]tasks.RequirementSpec, 0, len(command.Requirements))
	for _, requirement := range command.Requirements {
		required := requirement.Required
		requirements = append(requirements, tasks.RequirementSpec{
			Key:         requirement.Key,
			Type:        tasks.RequirementType(requirement.Type),
			Description: requirement.Description,
			Required:    &required,
		})
	}
	value, reused, err := a.Service.CreateTask(ctx, tasks.CreateRequest{
		OrganizationID:     a.OrganizationID,
		RequestedByRoleID:  command.RequestedByRoleID,
		AssignedRoleID:     command.AssignedRoleID,
		IdempotencyKey:     command.IdempotencyKey,
		Title:              command.Title,
		Instructions:       command.Instructions,
		AcceptanceCriteria: append([]string(nil), command.AcceptanceCriteria...),
		Priority:           command.Priority,
		MaxAttempts:        command.MaxAttempts,
		CorrelationID:      command.CorrelationID,
		CausationID:        command.CausationID,
		Dependencies:       append([]int64(nil), command.Dependencies...),
		Requirements:       requirements,
	}, "service", serviceActor)
	if err != nil {
		return executive.TaskRecord{}, false, err
	}
	detail, err := a.Service.GetTask(ctx, value.ID)
	if err != nil {
		return executive.TaskRecord{}, false, err
	}
	return mapTaskDetail(detail), reused, nil
}

func (a Tasks) AddDependency(ctx context.Context, taskID, dependsOnID int64) error {
	return a.Service.AddDependency(ctx, tasks.AddDependencyCommand{
		TaskID:      taskID,
		DependsOnID: dependsOnID,
		ActorType:   "service",
		ActorID:     serviceActor,
	})
}

func (a Tasks) GetTask(ctx context.Context, id int64) (executive.TaskRecord, error) {
	detail, err := a.Service.GetTask(ctx, id)
	if err != nil {
		return executive.TaskRecord{}, err
	}
	return mapTaskDetail(detail), nil
}

func (a Tasks) ListByCorrelation(ctx context.Context, correlation string) ([]executive.TaskRecord, error) {
	values, err := a.Service.ListTasks(ctx, tasks.TaskFilter{
		OrganizationID: a.OrganizationID,
		CorrelationID:  correlation,
		Limit:          1000,
	})
	if err != nil {
		return nil, err
	}
	out := make([]executive.TaskRecord, 0, len(values))
	for _, value := range values {
		detail, getErr := a.Service.GetTask(ctx, value.ID)
		if getErr != nil {
			return nil, getErr
		}
		out = append(out, mapTaskDetail(detail))
	}
	return out, nil
}

func (a Tasks) ClaimTask(ctx context.Context, taskID int64, workerID, assignedRole string, duration time.Duration) (executive.TaskRecord, executive.AttemptRecord, executive.LeaseRecord, error) {
	claimed, err := a.Service.ClaimTaskByID(ctx, taskID, tasks.ClaimRequest{
		OrganizationID: a.OrganizationID,
		WorkerID:       workerID,
		AssignedRoleID: assignedRole,
		LeaseDuration:  duration,
	})
	if err != nil {
		return executive.TaskRecord{}, executive.AttemptRecord{}, executive.LeaseRecord{}, err
	}
	detail, err := a.Service.GetTask(ctx, claimed.Task.ID)
	if err != nil {
		return executive.TaskRecord{}, executive.AttemptRecord{}, executive.LeaseRecord{}, err
	}
	return mapTaskDetail(detail), mapAttempt(claimed.Attempt), executive.LeaseRecord{
		TaskID:     claimed.Lease.TaskID,
		AttemptID:  claimed.Lease.AttemptID,
		HolderID:   claimed.Lease.HolderID,
		LeaseToken: claimed.LeaseToken,
		ExpiresAt:  claimed.Lease.ExpiresAt,
	}, nil
}

func (a Tasks) StartAttempt(ctx context.Context, lease executive.LeaseRecord, actor string) (executive.TaskRecord, error) {
	_, err := a.Service.StartAttempt(ctx, tasks.LeaseCommand{
		TaskID:     lease.TaskID,
		AttemptID:  lease.AttemptID,
		LeaseToken: lease.LeaseToken,
		ActorID:    actor,
	})
	if err != nil {
		return executive.TaskRecord{}, err
	}
	return a.GetTask(ctx, lease.TaskID)
}

func (a Tasks) Heartbeat(ctx context.Context, lease executive.LeaseRecord, actor string, extension time.Duration) (executive.LeaseRecord, error) {
	value, err := a.Service.Heartbeat(ctx, tasks.LeaseCommand{
		TaskID:     lease.TaskID,
		AttemptID:  lease.AttemptID,
		LeaseToken: lease.LeaseToken,
		ActorID:    actor,
		Extension:  extension,
	})
	if err != nil {
		return executive.LeaseRecord{}, err
	}
	return executive.LeaseRecord{
		TaskID:     value.TaskID,
		AttemptID:  value.AttemptID,
		HolderID:   value.HolderID,
		LeaseToken: lease.LeaseToken,
		ExpiresAt:  value.ExpiresAt,
	}, nil
}

func (a Tasks) RecordAttemptSucceeded(ctx context.Context, lease executive.LeaseRecord, actor, summary string) (executive.TaskRecord, error) {
	_, err := a.Service.RecordAttemptResult(ctx, tasks.RecordAttemptResultCommand{
		LeaseCommand: tasks.LeaseCommand{
			TaskID:     lease.TaskID,
			AttemptID:  lease.AttemptID,
			LeaseToken: lease.LeaseToken,
			ActorID:    actor,
		},
		Result: tasks.AttemptResult{Outcome: tasks.OutcomeSucceeded, Summary: summary},
	})
	if err != nil {
		return executive.TaskRecord{}, err
	}
	return a.GetTask(ctx, lease.TaskID)
}

func (a Tasks) RecordAttemptFailed(ctx context.Context, lease executive.LeaseRecord, actor, code, summary string, retryable bool) (executive.TaskRecord, error) {
	outcome := tasks.OutcomeNonRetryableFailure
	if retryable {
		outcome = tasks.OutcomeRetryableFailure
	}
	_, err := a.Service.RecordAttemptResult(ctx, tasks.RecordAttemptResultCommand{
		LeaseCommand: tasks.LeaseCommand{
			TaskID:     lease.TaskID,
			AttemptID:  lease.AttemptID,
			LeaseToken: lease.LeaseToken,
			ActorID:    actor,
		},
		Result: tasks.AttemptResult{Outcome: outcome, FailureCode: code, Summary: summary},
	})
	if err != nil {
		return executive.TaskRecord{}, err
	}
	return a.GetTask(ctx, lease.TaskID)
}

func (a Tasks) RecordEvidence(ctx context.Context, command executive.EvidenceCommand) error {
	var requirementID *int64
	if command.RequirementID > 0 {
		value := command.RequirementID
		requirementID = &value
	}
	_, err := a.Service.RecordEvidence(ctx, tasks.RecordEvidenceCommand{
		TaskID:        command.TaskID,
		RequirementID: requirementID,
		Type:          tasks.RequirementType(command.Type),
		Reference:     command.Reference,
		Digest:        command.Digest,
		RecordedBy:    command.RecordedBy,
		Metadata:      command.Metadata,
		Satisfies:     command.Satisfies,
	})
	return err
}

func (a Tasks) FinalizeCompleted(ctx context.Context, id int64, actorType, actorID string) (executive.TaskRecord, error) {
	_, err := a.Service.FinalizeTask(ctx, tasks.FinalizeCommand{TaskID: id, Outcome: tasks.FinalCompleted, ActorType: actorType, ActorID: actorID})
	if err != nil {
		return executive.TaskRecord{}, err
	}
	return a.GetTask(ctx, id)
}

func (a Tasks) FinalizeFailed(ctx context.Context, id int64, code, reason, actorType, actorID string) (executive.TaskRecord, error) {
	_, err := a.Service.FinalizeTask(ctx, tasks.FinalizeCommand{TaskID: id, Outcome: tasks.FinalFailed, ReasonCode: code, Reason: reason, ActorType: actorType, ActorID: actorID})
	if err != nil {
		return executive.TaskRecord{}, err
	}
	return a.GetTask(ctx, id)
}

func (a Tasks) BlockTask(ctx context.Context, id int64, code, reason, actorType, actorID string) (executive.TaskRecord, error) {
	_, err := a.Service.BlockTask(ctx, tasks.BlockCommand{TaskID: id, ReasonCode: code, Reason: reason, RevokeLease: false, ActorType: actorType, ActorID: actorID})
	if err != nil {
		return executive.TaskRecord{}, err
	}
	return a.GetTask(ctx, id)
}

func (a Tasks) UnblockTask(ctx context.Context, id int64, actorType, actorID string) (executive.TaskRecord, error) {
	_, err := a.Service.UnblockTask(ctx, tasks.UnblockCommand{TaskID: id, ActorType: actorType, ActorID: actorID})
	if err != nil {
		return executive.TaskRecord{}, err
	}
	return a.GetTask(ctx, id)
}

func (a Tasks) Reconcile(ctx context.Context, batch int) error {
	_, err := a.Service.Reconcile(ctx, batch)
	return err
}

func mapTaskDetail(detail tasks.TaskDetail) executive.TaskRecord {
	requested := ""
	if detail.Task.RequestedByRoleID != nil {
		requested = *detail.Task.RequestedByRoleID
	}
	correlation := ""
	if detail.Task.CorrelationID != nil {
		correlation = *detail.Task.CorrelationID
	}
	causation := ""
	if detail.Task.CausationID != nil {
		causation = *detail.Task.CausationID
	}
	reasonCode := ""
	if detail.Task.StatusReasonCode != nil {
		reasonCode = *detail.Task.StatusReasonCode
	}
	reason := ""
	if detail.Task.StatusReason != nil {
		reason = *detail.Task.StatusReason
	}
	out := executive.TaskRecord{
		ID:                     detail.Task.ID,
		OrganizationID:         detail.Task.OrganizationID,
		OrganizationRevisionID: detail.Task.OrganizationRevisionID,
		RequestedByRoleID:      requested,
		AssignedRoleID:         detail.Task.AssignedRoleID,
		AssignedUnitID:         detail.Task.AssignedUnitID,
		IdempotencyKey:         detail.Task.IdempotencyKey,
		RequestHash:            detail.Task.RequestHash,
		Title:                  detail.Task.Title,
		Instructions:           detail.Task.Instructions,
		AcceptanceCriteria:     append([]string(nil), detail.Task.AcceptanceCriteria...),
		Status:                 string(detail.Task.Status),
		Priority:               detail.Task.Priority,
		MaxAttempts:            detail.Task.MaxAttempts,
		AttemptCount:           detail.Task.AttemptCount,
		CorrelationID:          correlation,
		CausationID:            causation,
		ReasonCode:             reasonCode,
		Reason:                 reason,
	}
	for _, requirement := range detail.Requirements {
		out.Requirements = append(out.Requirements, executive.RequirementRecord{
			ID:       requirement.ID,
			Key:      requirement.Key,
			Type:     string(requirement.Type),
			Required: requirement.Required,
			Status:   string(requirement.Status),
		})
	}
	for _, attempt := range detail.Attempts {
		out.Attempts = append(out.Attempts, mapAttempt(attempt))
	}
	if detail.ActiveLease != nil {
		out.ActiveLease = &executive.LeaseRecord{
			TaskID:    detail.ActiveLease.TaskID,
			AttemptID: detail.ActiveLease.AttemptID,
			HolderID:  detail.ActiveLease.HolderID,
			ExpiresAt: detail.ActiveLease.ExpiresAt,
		}
	}
	return out
}

func mapAttempt(attempt tasks.Attempt) executive.AttemptRecord {
	return executive.AttemptRecord{ID: attempt.ID, Ordinal: attempt.Ordinal, State: string(attempt.State)}
}

var _ executive.TaskCoordinator = Tasks{}
