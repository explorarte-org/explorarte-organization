package modelruntime

import (
	"context"
	"fmt"
	"strings"
)

type InvocationResultReader interface {
	GetInvocationResult(context.Context, int64) (InvocationResult, error)
	FindInvocationsByTaskAttempt(context.Context, string, int64, int64) ([]Invocation, error)
}

// InvocationOutcomeReader reconstructs a completed dispatch from the durable
// invocation, result, and usage rows. It exists so an idempotently reused
// invocation never needs a second provider dispatch after a caller crash.
type InvocationOutcomeReader interface {
	GetInvocationOutcome(context.Context, int64) (DispatchResult, error)
}

type IdempotentInvocationReader interface {
	GetInvocationByIdempotency(context.Context, string, string) (Invocation, PreparedModelInput, error)
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

func (s *InvocationService) Outcome(ctx context.Context, invocationID int64) (DispatchResult, error) {
	if invocationID <= 0 {
		return DispatchResult{}, fmt.Errorf("%w: invalid invocation ID", ErrInvalidRequest)
	}
	reader, ok := s.store.(InvocationOutcomeReader)
	if !ok {
		return DispatchResult{}, fmt.Errorf("%w: invocation outcome reader unavailable", ErrDatabaseUnavailable)
	}
	return reader.GetInvocationOutcome(ctx, invocationID)
}

func (s *InvocationService) FindIdempotent(ctx context.Context, idempotencyKey string) (Invocation, PreparedModelInput, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" || len(idempotencyKey) > 200 {
		return Invocation{}, PreparedModelInput{}, fmt.Errorf("%w: invalid idempotency key", ErrInvalidRequest)
	}
	reader, ok := s.store.(IdempotentInvocationReader)
	if !ok {
		return Invocation{}, PreparedModelInput{}, fmt.Errorf("%w: idempotent invocation reader unavailable", ErrDatabaseUnavailable)
	}
	return reader.GetInvocationByIdempotency(ctx, s.organizationID, idempotencyKey)
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
