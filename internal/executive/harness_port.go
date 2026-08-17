package executive

import (
	"context"
	"encoding/json"
	"time"
)

// ExecutionPurpose is the closed set of cognitive executions the Executive
// performs. It is an enum rather than the free-text Purpose that reaches Model
// Runtime because it becomes part of a durable run identity: a value that can
// drift with a reworded string would silently split or merge run histories.
type ExecutionPurpose string

const (
	PurposeCEOPlan          ExecutionPurpose = "ceo-plan"
	PurposeDepartmentPlan   ExecutionPurpose = "department-plan"
	PurposeDepartmentWorker ExecutionPurpose = "department-worker"
	PurposeDepartmentReview ExecutionPurpose = "department-review"
	PurposeCEOClosure       ExecutionPurpose = "ceo-closure"
)

func (p ExecutionPurpose) Valid() bool {
	switch p {
	case PurposeCEOPlan, PurposeDepartmentPlan, PurposeDepartmentWorker, PurposeDepartmentReview, PurposeCEOClosure:
		return true
	}
	return false
}

// ModelInvocationReader is Model Runtime's read side.
//
// Evidence and recovery still need to see durable invocations: evidence is
// built from invocation results, and restart detection looks for a succeeded
// invocation whose lease is no longer adoptable. What they must NOT be able to
// do is start work. This interface therefore has no EnsureInvocation, no
// Create and no Dispatch, and that absence is the point: with execution
// reachable only through HarnessExecutor, "the Executive cannot bypass the
// Harness" stops being a convention someone has to remember and becomes a
// property of the type.
type ModelInvocationReader interface {
	GetInvocation(context.Context, int64) (InvocationRecord, error)
	FindTaskAttemptInvocations(context.Context, int64, int64) ([]InvocationRecord, error)
	GetResult(context.Context, int64) (InvocationResult, error)
}

// HarnessExecutor is the only execute side the Executive can reach.
type HarnessExecutor interface {
	Execute(context.Context, HarnessRunCommand) (HarnessRunOutcome, error)
}

// HarnessRunCommand is what the Executive knows about a cognitive execution.
// The Harness derives its own run spec from this; the Executive does not build
// executionharness types, so the two contracts can move independently.
type HarnessRunCommand struct {
	RunID                string
	TaskID               int64
	AttemptID            int64
	RoleID               string
	ExecutionPrincipalID string
	LeaseToken           string
	ContextSnapshotID    int64
	Purpose              ExecutionPurpose
	OutputSchema         json.RawMessage
	MaxOutputTokens      int
	CorrelationID        string
	CausationID          string
	Deadline             time.Time
}

// HarnessRunFailure is the minimum the Executive must be able to tell apart to
// decide what happens to a task attempt. It is deliberately not a mirror of
// every Harness status: the Executive should not acquire a dependency on the
// Harness's internal event vocabulary.
//
// model_outcome_ambiguous is absent on purpose. The durable owner of that state
// is Model Runtime, not the Harness, so it is read back through
// ModelInvocationReader rather than smuggled through this type.
type HarnessRunFailure string

const (
	HarnessFailureNone HarnessRunFailure = ""
	// HarnessFailureAuthorityUnavailable means authority could not be
	// consulted. It is not a denial and the run was left resumable.
	HarnessFailureAuthorityUnavailable HarnessRunFailure = "authority_unavailable"
	// HarnessFailureAuthorizationDenied means authority was consulted and
	// refused: the principal or the lease lost standing.
	HarnessFailureAuthorizationDenied HarnessRunFailure = "authorization_denied"
	HarnessFailureModelError          HarnessRunFailure = "model_error"
	// HarnessFailureToolRejected is what a model tool intent becomes for an
	// Executive run, which exposes no tools at all.
	HarnessFailureToolRejected HarnessRunFailure = "tool_rejected"
	// HarnessFailureIndeterminateTool means a tool may or may not have run and
	// nobody can tell. It requires reconciliation, never an automatic retry.
	HarnessFailureIndeterminateTool HarnessRunFailure = "indeterminate_tool_execution"
	HarnessFailureCancelled         HarnessRunFailure = "cancelled"
	HarnessFailureHistoryError      HarnessRunFailure = "history_error"
	HarnessFailureLimitReached      HarnessRunFailure = "limit_reached"
	HarnessFailureIdentityDrift     HarnessRunFailure = "identity_drift"
)

// HarnessRunOutcome is the semantic minimum the Executive needs.
type HarnessRunOutcome struct {
	// Completed is true only for a run that reached a final model answer.
	Completed bool
	// FinalOutput is the canonical JSON the output contract produced.
	FinalOutput string
	// InvocationID is the durable Model Runtime reference for the last model
	// turn, so evidence keeps pointing at the same row it always did. Zero
	// when no turn was recorded.
	InvocationID int64
	Failure      HarnessRunFailure
	// Retryable reports that the run stopped without a terminal verdict and
	// the same run identity may be entered again.
	Retryable         bool
	TerminationReason string
}
