package executionharness

import (
	"context"
	"errors"
	"strings"
)

type AuthorityRequest struct {
	Identity   RunIdentity
	LeaseToken string
}

// ExecutionAuthorityPort verifies current task/attempt/role/principal/lease
// authority. The Harness invokes it before every model or tool side effect.
type ExecutionAuthorityPort interface {
	AuthorizeExecution(context.Context, AuthorityRequest) error
}

// ModelExecutor is the sole model boundary visible to the Harness. A real
// adapter must enter modelruntime, which remains responsible for dispatch,
// egress, execution identity, provider transport, and economic gates.
type ModelExecutor interface {
	Invoke(context.Context, RunIdentity, NormalizedModelRequest) (ModelResult, error)
}

type ToolCatalog interface {
	Lookup(context.Context, string) (ToolDefinition, bool)
	ValidateArguments(context.Context, ToolDefinition, jsonRaw) error
}

type ToolExecutor interface {
	Execute(context.Context, RunIdentity, ToolRequest) (ToolExecutionResult, error)
}

// jsonRaw is an alias kept private to prevent ports from acquiring provider-
// specific request types.
type jsonRaw = []byte

type ExecutionHistoryStore interface {
	Append(context.Context, string, uint64, Event) (Event, error)
	Read(context.Context, string) ([]Event, error)
}

// InvocationFailure reports a model call that failed AFTER a durable
// invocation existed, and names it.
//
// Without it the reference dies at the only place that ever held it. The
// adapter creates the invocation, dispatches it, and on failure returns an
// empty result and a bare error -- so the Harness records a failed run with
// nothing to point at, and the Executive, which decides retryability by
// asking Model Runtime about a specific invocation, has nothing to ask
// about. Model Runtime had already recorded the answer; the question could
// not be formed.
type InvocationFailure struct {
	// Ref is the durable invocation reference, in the same form a recorded
	// response carries.
	Ref string
	Err error
}

func (e *InvocationFailure) Error() string { return e.Err.Error() }
func (e *InvocationFailure) Unwrap() error { return e.Err }

// WithInvocationRef attaches an invocation reference to a failure, when one
// exists. A failure that never reached an invocation is returned unchanged:
// naming an invocation that does not exist would be worse than naming none.
func WithInvocationRef(ref string, err error) error {
	if err == nil || strings.TrimSpace(ref) == "" {
		return err
	}
	return &InvocationFailure{Ref: strings.TrimSpace(ref), Err: err}
}

// InvocationRefOf recovers the reference a failure carries, or "".
func InvocationRefOf(err error) string {
	var failure *InvocationFailure
	if errors.As(err, &failure) {
		return failure.Ref
	}
	return ""
}
