package coderunner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// External audit A7: a failed attempt lost its operations to an error string, and every failure was
// retried alike. Deterministic failures are not retried, transient ones are, and each keeps its
// structured evidence under the failure reference.
func TestExecutionFailuresAreClassified(t *testing.T) {
	failedPatch := []Result{{Type: ApplyPatch, Success: false, ExitCode: 1}}
	failedTest := []Result{{Type: ApplyPatch, Success: true}, {Type: GoTest, Success: false, ExitCode: 1, Command: []string{"go", "test", "./..."}}}
	for name, c := range map[string]struct {
		results   []Result
		err       error
		class     string
		retryable bool
	}{
		"patch":         {failedPatch, errors.New("operation APPLY_PATCH failed"), FailurePatchNotApplicable, false},
		"test":          {failedTest, errors.New("operation GO_TEST failed"), FailureCheckFailed, false},
		"invalid":       {nil, fmt.Errorf("%w: path required", ErrInvalidOperation), FailureInvalidOperation, false},
		"budget":        {nil, errors.New("plan output budget exceeded"), FailureOutputBudgetExceeded, false},
		"timeout":       {nil, fmt.Errorf("go test: %w", context.DeadlineExceeded), FailureOperationTimeout, true},
		"indeterminate": {nil, ErrIndeterminateExecution, FailureIndeterminate, false},
		"infra":         {nil, errors.New("no space left on device"), FailureExecution, true},
	} {
		class, retryable := classifyExecutionFailure(c.results, c.err)
		if class != c.class || retryable != c.retryable {
			t.Errorf("%s: class %q retryable %v, want %q %v", name, class, retryable, c.class, c.retryable)
		}
	}
}

type failingExec struct {
	results []Result
	err     error
}

func (f failingExec) Execute(context.Context, Plan) ([]Result, error) { return f.results, f.err }

func TestAFailedAttemptKeepsItsEvidenceAndIsNotRetriedWhenDeterministic(t *testing.T) {
	q := &queueFake{}
	exec := failingExec{
		results: []Result{{Type: GitStatus, Success: true}, {Type: GoTest, Success: false, ExitCode: 1, Command: []string{"go", "test", "./internal/x/..."}, OutputDigest: "abc"}},
		err:     errors.New("operation GO_TEST failed with exit code 1: --- FAIL: TestX"),
	}
	w := Worker{Queue: q, Executor: exec, Workspace: workspaceFake{}, WorkerID: "runner-1", HolderPrincipalID: "42", LeaseDuration: time.Second}
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if q.lastResult.Outcome != tasks.OutcomeNonRetryableFailure || q.lastResult.FailureCode != FailureCheckFailed {
		t.Fatalf("result %+v, want a non-retryable %s", q.lastResult, FailureCheckFailed)
	}
	if !strings.HasPrefix(q.lastEvidence.Reference, FailureEvidenceReferencePrefix) {
		t.Fatalf("failure evidence recorded under %q", q.lastEvidence.Reference)
	}
	failed, _ := q.lastEvidence.Metadata["failed_operation"].(map[string]any)
	command, _ := q.lastEvidence.Metadata["failed_command"].([]any)
	executed, _ := q.lastEvidence.Metadata["operations_executed"].([]any)
	if failed["type"] != string(GoTest) || len(command) != 3 || len(executed) != 2 || q.lastEvidence.Metadata["failure_class"] != FailureCheckFailed {
		t.Fatalf("failure evidence lost what ran: %+v", q.lastEvidence.Metadata)
	}
	if _, sealed := q.lastEvidence.Metadata["candidate_revision"]; sealed {
		t.Fatal("a failed attempt's evidence presents a candidate")
	}
}

func TestATransientFailureIsStillRetried(t *testing.T) {
	q := &queueFake{}
	w := Worker{Queue: q, Executor: failingExec{err: errors.New("no space left on device")}, Workspace: workspaceFake{}, WorkerID: "runner-1", HolderPrincipalID: "42", LeaseDuration: time.Second}
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if q.lastResult.Outcome != tasks.OutcomeRetryableFailure || q.lastResult.FailureCode != FailureExecution || q.evidenced != 1 {
		t.Fatalf("result %+v evidenced %d, want a retryable %s with its evidence", q.lastResult, q.evidenced, FailureExecution)
	}
}
