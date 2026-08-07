package modelruntime

import (
	"context"
	"fmt"
)

type InvocationResultReader interface {
	GetInvocationResult(context.Context, int64) (InvocationResult, error)
	FindInvocationsByTaskAttempt(context.Context, string, int64, int64) ([]Invocation, error)
}

func (s *InvocationService) Result(ctx context.Context, invocationID int64) (InvocationResult, error) {
	if invocationID <= 0 {
		return InvocationResult{}, fmt.Errorf("%w: invalid invocation ID", ErrInvalidRequest)
	}
	reader, ok := s.store.(InvocationResultReader)
	if !ok {
		return InvocationResult{}, fmt.Errorf("%w: invocation result reader unavailable", ErrDatabaseUnavailable)
	}
	return reader.GetInvocationResult(ctx, invocationID)
}

func (s *InvocationService) FindTaskAttempt(ctx context.Context, taskID, attemptID int64) ([]Invocation, error) {
	if taskID <= 0 || attemptID <= 0 {
		return nil, fmt.Errorf("%w: task and attempt IDs must be positive", ErrInvalidRequest)
	}
	reader, ok := s.store.(InvocationResultReader)
	if !ok {
		return nil, fmt.Errorf("%w: invocation result reader unavailable", ErrDatabaseUnavailable)
	}
	return reader.FindInvocationsByTaskAttempt(ctx, s.organizationID, taskID, attemptID)
}
