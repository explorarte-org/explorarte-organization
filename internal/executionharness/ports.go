package executionharness

import "context"

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
