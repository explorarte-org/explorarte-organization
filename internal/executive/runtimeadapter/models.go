package runtimeadapter

import (
	"context"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

// Models is Model Runtime's read side, and only its read side.
//
// It used to carry EnsureInvocation, which is how the Executive created
// provider work directly. That method is gone rather than merely unused: with
// no execute-side operation on this adapter, a productive Executive path that
// tried to bypass the Harness would not compile.
type Models struct {
	Service        *modelruntime.InvocationService
	OrganizationID string
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

var _ executive.ModelInvocationReader = Models{}
