package runtimeadapter

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

const (
	// executiveExecutionProfile and executiveModelPolicy are part of the run
	// identity digest: changing either makes every in-flight run drift and
	// restart under a new identity, so they are versioned constants rather
	// than configuration.
	executiveExecutionProfile = "executive/typed-task/v1"
	executiveModelPolicy      = "executive/typed-json/v1"

	// An Executive typed task is one question and one JSON answer. MaxTurns=1
	// is what makes "one task attempt, one model invocation" a property of the
	// run policy rather than an assertion after the fact, and MaxToolCalls=0
	// says the same thing about tools that the empty tool set already says.
	executiveMaxTurns     = 1
	executiveMaxToolCalls = 0
)

// HarnessModelExecutorFactory builds the Harness's model boundary for one
// output contract. It is a factory because the contract is per-run -- each
// Executive purpose validates against a different JSON schema -- while the
// provider stack underneath is constructed once, at bootstrap.
type HarnessModelExecutorFactory func(modelruntimeadapter.Config) (executionharness.ModelExecutor, error)

// Harness is the Executive's only execute side.
//
// It owns no provider machinery of its own: the model executor comes from the
// Model Runtime bootstrap seam, so routing, egress, execution identity,
// pricing and wallets are the same single stack production already had. What
// this adapter adds is the translation between one Executive run command and
// one Harness run spec, and the invariant check on the way back.
type Harness struct {
	OrganizationID   string
	Authority        executionharness.ExecutionAuthorityPort
	History          executionharness.ExecutionHistoryStore
	NewModelExecutor HarnessModelExecutorFactory
	Clock            executive.Clock
	// RequiredCapabilities is the capability set every executive model call
	// must be routed under. Empty preserves the pre-Harness behavior, where
	// the Executive never constrained routing.
	RequiredCapabilities []modelruntime.ModelCapability
}

func (h Harness) Execute(ctx context.Context, command executive.HarnessRunCommand) (executive.HarnessRunOutcome, error) {
	if h.Authority == nil || h.History == nil || h.NewModelExecutor == nil {
		return executive.HarnessRunOutcome{}, errors.New("executive harness adapter dependencies are incomplete")
	}
	if err := validateRunCommand(command); err != nil {
		return executive.HarnessRunOutcome{}, err
	}
	now := time.Now()
	if h.Clock != nil {
		now = h.Clock.Now()
	}
	ttl := command.Deadline.Sub(now)
	if ttl <= 0 {
		return executive.HarnessRunOutcome{}, fmt.Errorf("%w: execution deadline already passed", executive.ErrInvalidInput)
	}
	// The output contract is the Executive's: JSON, against this purpose's
	// schema. It reaches Model Runtime through the adapter config rather than
	// being post-parsed out of free text, and it participates in the durable
	// invocation's idempotency, so a re-entry under a different schema cannot
	// adopt the previous invocation.
	models, err := h.NewModelExecutor(modelruntimeadapter.Config{
		RequiredCapabilities:          append([]modelruntime.ModelCapability(nil), h.RequiredCapabilities...),
		MaxOutputTokens:               command.MaxOutputTokens,
		ThinkingMode:                  modelruntime.ThinkingOpaque,
		InvocationTTL:                 ttl,
		OutputMode:                    modelruntime.OutputJSON,
		OutputSchema:                  append([]byte(nil), command.OutputSchema...),
		ExecutionContractInstructions: command.ExecutionContract,
		// The invocation's durable purpose states whose execution this was:
		// the validated Executive enum value, not a projection digest. It is
		// what the ambiguity reconciler later reads to classify the run's
		// effect class. Identity validation never compares it.
		Purpose: string(command.Purpose),
	})
	if err != nil {
		return executive.HarnessRunOutcome{}, fmt.Errorf("build harness model executor: %w", err)
	}
	runtime, err := executionharness.New(h.Authority, models, executiveToolCatalog{}, executiveToolExecutor{}, h.History)
	if err != nil {
		return executive.HarnessRunOutcome{}, fmt.Errorf("build harness runtime: %w", err)
	}

	spec := executionharness.RunSpec{
		Identity: executionharness.RunIdentity{
			RunID:                command.RunID,
			OrganizationID:       h.OrganizationID,
			TaskID:               command.TaskID,
			AttemptID:            command.AttemptID,
			RoleID:               command.RoleID,
			ExecutionPrincipalID: command.ExecutionPrincipalID,
			CorrelationID:        command.CorrelationID,
			CausationID:          command.CausationID,
		},
		LeaseToken: command.LeaseToken,
		Context: executionharness.InitialContext{
			ID:      strconv.FormatInt(command.Context.ID, 10),
			Version: command.Context.Version,
			Digest:  command.Context.Digest,
			Content: command.Context.Content,
		},
		// No tools. Not an empty list that could later be filled in by
		// configuration: Executive typed tasks have never allowed a
		// model-selected tool, and the Harness turns any tool intent under an
		// empty set into a denial without entering a tool executor.
		Tools: nil,
		Policy: executionharness.RunPolicy{
			MaxTurns:           executiveMaxTurns,
			MaxToolCalls:       executiveMaxToolCalls,
			ExecutionProfileID: executiveExecutionProfile,
			ModelPolicyRef:     executiveModelPolicy,
		},
	}

	result := runtime.Execute(ctx, spec)
	outcome, err := h.mapResult(ctx, command, result)
	if err != nil {
		return executive.HarnessRunOutcome{}, err
	}
	// The invariant check runs here, at the boundary, so a contradictory
	// outcome can never become a task state.
	if err = outcome.Validate(); err != nil {
		return executive.HarnessRunOutcome{}, err
	}
	return outcome, nil
}

func (h Harness) mapResult(ctx context.Context, command executive.HarnessRunCommand, result executionharness.RunResult) (executive.HarnessRunOutcome, error) {
	outcome := executive.HarnessRunOutcome{TerminationReason: result.TerminationReason}
	// The durable model reference is read back from the run's own history
	// rather than from the result, because a resumed run reports its terminal
	// state from history and never re-enters the model at all -- there would
	// be nothing in the return value to carry the invocation ID.
	invocationID, err := h.lastInvocationID(ctx, command.RunID)
	if err != nil {
		return executive.HarnessRunOutcome{}, err
	}
	outcome.InvocationID = invocationID

	switch result.Status {
	case executionharness.StatusCompleted:
		outcome.Status = executive.HarnessRunSucceeded
		outcome.FinalOutput = result.FinalOutput
		return outcome, nil
	case executionharness.StatusAuthorityUnavailable:
		outcome.Failure = executive.HarnessFailureAuthorityUnavailable
	case executionharness.StatusAuthorizationDenied:
		outcome.Failure = executive.HarnessFailureAuthorizationDenied
	case executionharness.StatusModelError:
		outcome.Failure = executive.HarnessFailureModelError
	case executionharness.StatusToolError:
		outcome.Failure = executive.HarnessFailureToolRejected
	case executionharness.StatusIndeterminateToolExecution:
		outcome.Failure = executive.HarnessFailureIndeterminateTool
	case executionharness.StatusCancelled:
		outcome.Failure = executive.HarnessFailureCancelled
	case executionharness.StatusHistoryError:
		outcome.Failure = executive.HarnessFailureHistoryError
	case executionharness.StatusLimitReached:
		outcome.Failure = executive.HarnessFailureLimitReached
	case executionharness.StatusIdentityDrift:
		outcome.Failure = executive.HarnessFailureIdentityDrift
	default:
		return executive.HarnessRunOutcome{}, fmt.Errorf("%w: unknown harness run status %q", executive.ErrContractRejected, result.Status)
	}
	outcome.Status = executive.HarnessRunFailed
	outcome.Retryable = result.Retryable
	if outcome.TerminationReason == "" {
		outcome.TerminationReason = string(result.Status)
	}
	return outcome, nil
}

// lastInvocationID returns the Model Runtime invocation of the run's most
// recent model turn, or zero when no turn was ever recorded.
func (h Harness) lastInvocationID(ctx context.Context, runID string) (int64, error) {
	events, err := h.History.Read(ctx, runID)
	if err != nil {
		return 0, fmt.Errorf("read harness history for %s: %w", runID, err)
	}
	var invocationID int64
	for _, event := range events {
		// A recorded response carries the reference; a failed run carries it
		// on the event itself, because there was no response to put it in.
		// Reading only the first meant the reference was available exactly
		// when it did not matter and absent exactly when it did.
		reference := ""
		switch {
		case event.Type == executionharness.EventModelResponseRecorded && event.ModelResult != nil:
			reference = strings.TrimSpace(event.ModelResult.InvocationRef)
		case event.Type == executionharness.EventRunFailed:
			reference = strings.TrimSpace(event.InvocationRef)
		}
		if reference == "" {
			continue
		}
		parsed, parseErr := strconv.ParseInt(reference, 10, 64)
		if parseErr != nil || parsed <= 0 {
			return 0, fmt.Errorf("%w: harness history references invocation %q", executive.ErrContractRejected, reference)
		}
		invocationID = parsed
	}
	return invocationID, nil
}

func validateRunCommand(command executive.HarnessRunCommand) error {
	switch {
	case strings.TrimSpace(command.RunID) == "":
		return fmt.Errorf("%w: harness run requires a deterministic run ID", executive.ErrInvalidInput)
	case command.TaskID <= 0 || command.AttemptID <= 0:
		return fmt.Errorf("%w: harness run is not bound to a task attempt", executive.ErrInvalidInput)
	case strings.TrimSpace(command.RoleID) == "":
		return fmt.Errorf("%w: harness run requires a subject role", executive.ErrInvalidInput)
	case strings.TrimSpace(command.ExecutionPrincipalID) == "":
		return fmt.Errorf("%w: harness run requires an execution principal", executive.ErrExecutionPrincipalUnusable)
	case strings.TrimSpace(command.LeaseToken) == "":
		return fmt.Errorf("%w: harness run requires the attempt's lease token", executive.ErrInvalidInput)
	case !command.Purpose.Valid():
		return fmt.Errorf("%w: unknown execution purpose %q", executive.ErrContractRejected, command.Purpose)
	case command.Context.ID <= 0 || command.Context.Content == "" || len(command.Context.Digest) != 64:
		return fmt.Errorf("%w: harness run requires a rendered context snapshot", executive.ErrContractRejected)
	case len(command.OutputSchema) == 0:
		return fmt.Errorf("%w: executive runs require a JSON output schema", executive.ErrContractRejected)
	case command.MaxOutputTokens <= 0:
		return fmt.Errorf("%w: harness run requires an output token budget", executive.ErrInvalidInput)
	}
	return nil
}

// executiveToolCatalog knows no tools, because Executive typed tasks have
// none. A model tool intent therefore fails the Harness's catalog lookup and
// is denied before any executor is reached.
type executiveToolCatalog struct{}

func (executiveToolCatalog) Lookup(context.Context, string) (executionharness.ToolDefinition, bool) {
	return executionharness.ToolDefinition{}, false
}

func (executiveToolCatalog) ValidateArguments(context.Context, executionharness.ToolDefinition, []byte) error {
	return errors.New("executive typed tasks expose no tools")
}

// executiveToolExecutor exists only to satisfy the Harness's constructor. If
// it is ever entered, something upstream stopped denying tool intents, and
// failing loudly is better than performing an external side effect the
// Executive never authorized.
type executiveToolExecutor struct{}

func (executiveToolExecutor) Execute(context.Context, executionharness.RunIdentity, executionharness.ToolRequest) (executionharness.ToolExecutionResult, error) {
	return executionharness.ToolExecutionResult{}, errors.New("executive typed tasks execute no tools")
}

var (
	_ executive.HarnessExecutor     = Harness{}
	_ executionharness.ToolCatalog  = executiveToolCatalog{}
	_ executionharness.ToolExecutor = executiveToolExecutor{}
)
