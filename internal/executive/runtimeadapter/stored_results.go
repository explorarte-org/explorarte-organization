package runtimeadapter

import (
	"context"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

// StoredResults is the SQL read side used by status, with no model bootstrap,
// credentials, provider adapter or assignment provisioning.
type StoredResults struct {
	OrganizationID string
	Store          interface {
		FindInvocationsByTaskAttempt(context.Context, string, int64, int64) ([]modelruntime.Invocation, error)
		GetInvocationResult(context.Context, int64) (modelruntime.InvocationResult, error)
	}
}

func (r StoredResults) FindTaskAttemptInvocations(ctx context.Context, task, attempt int64) ([]executive.InvocationRecord, error) {
	values, err := r.Store.FindInvocationsByTaskAttempt(ctx, r.OrganizationID, task, attempt)
	if err != nil {
		return nil, err
	}
	results := make([]executive.InvocationRecord, 0, len(values))
	for _, value := range values {
		results = append(results, mapInvocation(value))
	}
	return results, nil
}

func (r StoredResults) GetResult(ctx context.Context, id int64) (executive.InvocationResult, error) {
	value, err := r.Store.GetInvocationResult(ctx, id)
	if err != nil {
		return executive.InvocationResult{}, err
	}
	return executive.InvocationResult{InvocationID: value.InvocationID, JSONOutput: append([]byte(nil), value.JSONOutput...), TextOutput: value.TextOutput, ToolIntents: len(value.ToolIntents), ResponseHash: value.ResponseHash, ResponseBytes: value.ResponseBytes}, nil
}

var _ executive.StatusResultReader = StoredResults{}
