package runtimeadapter

import (
	"context"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

type Models struct {
	Service        *modelruntime.InvocationService
	OrganizationID string
}

func (a Models) EnsureInvocation(ctx context.Context, command executive.InvocationCommand) (executive.InvocationRecord, bool, error) {
	result, err := a.Service.Create(ctx, modelruntime.CreateInvocationCommand{
		OrganizationID:    a.OrganizationID,
		TaskID:            command.TaskID,
		AttemptID:         command.AttemptID,
		SubjectRoleID:     command.SubjectRoleID,
		ContextSnapshotID: command.ContextSnapshotID,
		Purpose:           command.Purpose,
		OutputMode:        modelruntime.OutputJSON,
		OutputSchema:      command.OutputSchema,
		MaxOutputTokens:   command.MaxOutputTokens,
		ThinkingMode:      modelruntime.ThinkingOpaque,
		IdempotencyKey:    command.IdempotencyKey,
		CorrelationID:     command.CorrelationID,
		CausationID:       command.CausationID,
		Deadline:          command.Deadline,
	})
	if err != nil {
		return executive.InvocationRecord{}, false, err
	}
	return mapInvocation(result.Invocation), result.Reused, nil
}

func (a Models) GetInvocation(ctx context.Context, id int64) (executive.InvocationRecord, error) {
	value, err := a.Service.Get(ctx, id)
	if err != nil {
		return executive.InvocationRecord{}, err
	}
	return mapInvocation(value), nil
}

func (a Models) FindTaskAttemptInvocations(ctx context.Context, taskID, attemptID int64) ([]executive.InvocationRecord, error) {
	values, err := a.Service.FindTaskAttempt(ctx, taskID, attemptID)
	if err != nil {
		return nil, err
	}
	out := make([]executive.InvocationRecord, 0, len(values))
	for _, value := range values {
		out = append(out, mapInvocation(value))
	}
	return out, nil
}

func (a Models) GetResult(ctx context.Context, id int64) (executive.InvocationResult, error) {
	value, err := a.Service.Result(ctx, id)
	if err != nil {
		return executive.InvocationResult{}, err
	}
	return executive.InvocationResult{
		InvocationID:  value.InvocationID,
		JSONOutput:    append([]byte(nil), value.JSONOutput...),
		TextOutput:    value.TextOutput,
		ToolIntents:   len(value.ToolIntents),
		ResponseHash:  value.ResponseHash,
		ResponseBytes: value.ResponseBytes,
	}, nil
}

func mapInvocation(value modelruntime.Invocation) executive.InvocationRecord {
	return executive.InvocationRecord{
		ID:            value.ID,
		TaskID:        value.TaskID,
		AttemptID:     value.AttemptID,
		SubjectRoleID: value.SubjectRoleID,
		Status:        string(value.Status),
		ErrorCode:     value.ErrorCode,
		CorrelationID: value.CorrelationID,
		CausationID:   value.CausationID,
	}
}

var _ executive.ModelCoordinator = Models{}
