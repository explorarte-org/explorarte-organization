package executive

import (
	"context"
	"encoding/json"
	"fmt"
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

// LegacyPurpose is the free-text purpose the Context Engine and Model Runtime
// have always seen. It is kept byte-identical to the pre-Harness strings so
// context snapshots, durable invocation rows and every consumer that keys on
// them do not silently split into two vocabularies.
func (p ExecutionPurpose) LegacyPurpose() string {
	switch p {
	case PurposeCEOPlan:
		return "executive_ceo_plan"
	case PurposeDepartmentPlan:
		return "department_plan"
	case PurposeDepartmentWorker:
		return "department_worker"
	case PurposeDepartmentReview:
		return "department_review"
	case PurposeCEOClosure:
		return "executive_ceo_closure"
	}
	return ""
}

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
	// Context is the whole snapshot, render included, not just its ID. Model
	// Runtime requires an invocation's stable prefix to be the byte-exact
	// render of the snapshot it references, and the run identity digest binds
	// the context bytes, so a snapshot ID alone cannot describe a run.
	Context         ContextSnapshot
	Purpose         ExecutionPurpose
	OutputSchema    json.RawMessage
	MaxOutputTokens int
	CorrelationID   string
	CausationID     string
	Deadline        time.Time
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

// HarnessRunStatus is the outcome's single source of truth about whether the
// run produced an answer. It replaces a boolean that could disagree with the
// failure field: "completed, authorization denied, retryable" was a
// representable value, and no consumer could tell which third of it to trust.
type HarnessRunStatus string

const (
	HarnessRunSucceeded HarnessRunStatus = "succeeded"
	HarnessRunFailed    HarnessRunStatus = "failed"
)

// HarnessRunOutcome is the semantic minimum the Executive needs. Its fields
// are not independent: see Validate, which every adapter must satisfy before
// the value reaches the Executive.
type HarnessRunOutcome struct {
	Status HarnessRunStatus
	// FinalOutput is the canonical JSON the output contract produced. It is
	// non-empty exactly when Status is succeeded.
	FinalOutput string
	// InvocationID is the durable Model Runtime reference for the last model
	// turn, so evidence keeps pointing at the same row it always did. Zero
	// when no turn was recorded.
	InvocationID int64
	Failure      HarnessRunFailure
	// Retryable reports that the run stopped without a terminal verdict and
	// the same run identity may be entered again. That is true of exactly one
	// failure -- authority that could not be consulted -- because every other
	// status wrote a terminal event to the durable history.
	Retryable         bool
	TerminationReason string
}

// Validate rejects outcomes that cannot describe anything that happened. It is
// the reason the Executive can branch on one field instead of reconciling
// three, and it runs at the adapter boundary so a malformed outcome never
// becomes a task state.
func (o HarnessRunOutcome) Validate() error {
	switch o.Status {
	case HarnessRunSucceeded:
		switch {
		case o.Failure != HarnessFailureNone:
			return fmt.Errorf("%w: a succeeded run cannot carry failure %q", ErrContractRejected, o.Failure)
		case o.Retryable:
			return fmt.Errorf("%w: a succeeded run is never retryable", ErrContractRejected)
		case o.FinalOutput == "":
			return fmt.Errorf("%w: a succeeded run must carry its final output", ErrContractRejected)
		case o.InvocationID <= 0:
			// A final answer came from a model turn, and that turn is a
			// durable Model Runtime row. Without its ID there is no evidence
			// to record and no invocation to inspect for ambiguity.
			return fmt.Errorf("%w: a succeeded run must reference its durable model invocation", ErrContractRejected)
		}
		return nil
	case HarnessRunFailed:
		switch {
		case o.Failure == HarnessFailureNone:
			return fmt.Errorf("%w: a failed run must say why", ErrContractRejected)
		case o.FinalOutput != "":
			return fmt.Errorf("%w: a failed run cannot carry a final output", ErrContractRejected)
		case o.Retryable != (o.Failure == HarnessFailureAuthorityUnavailable):
			return fmt.Errorf("%w: only unavailable authority leaves a run retryable, got %q retryable=%v", ErrContractRejected, o.Failure, o.Retryable)
		}
		return nil
	default:
		return fmt.Errorf("%w: unknown harness run status %q", ErrContractRejected, o.Status)
	}
}
