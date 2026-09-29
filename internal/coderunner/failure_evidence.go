package coderunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// stagingWorkspaceNone stands for "nothing was sealed": a failed attempt has no candidate.
var stagingWorkspaceNone = staging.Workspace{}

// A failed attempt keeps what it did, and says what kind of failure it was.
//
// External audit A7 (2026-09-27): on an execution error the worker recorded execution_failed as
// retryable and returned before building any durable evidence, so the operations that ran, their
// argv, exit codes and output digests were reduced to an error string -- and a patch that does not
// apply or a test that fails deterministically was retried like a transient fault (roots 1020 and
// 1103 dead-lettered after five attempts). A failed attempt now records structured evidence under
// its own reference (never the success evidence reference: nothing is sealed or presented as a
// candidate), and its failure is classified: only what can change on its own is retried.

const failureEvidenceSchemaVersion = "code-runner-attempt-failure/v1"

// FailureEvidenceReferencePrefix is where failure evidence is recorded, distinct from the success
// evidence reference so no verifier can mistake a failure for a verified candidate.
const FailureEvidenceReferencePrefix = "code-runner-attempt-failure://"

// Failure classes.
const (
	FailurePatchNotApplicable   = "patch_not_applicable"
	FailureCheckFailed          = "check_failed"
	FailureInvalidOperation     = "invalid_operation"
	FailureOutputBudgetExceeded = "output_budget_exceeded"
	FailureOperationTimeout     = "operation_timeout"
	FailureExecution            = "execution_failed"
	FailureIndeterminate        = "indeterminate_code_execution"
)

type failureEvidence struct {
	SchemaVersion      string              `json:"schema_version"`
	TaskID             int64               `json:"task_id"`
	AttemptID          int64               `json:"attempt_id"`
	FailureClass       string              `json:"failure_class"`
	Retryable          bool                `json:"retryable"`
	FailedOperation    *operationEvidence  `json:"failed_operation,omitempty"`
	FailedCommand      []string            `json:"failed_command,omitempty"`
	OperationsExecuted []operationEvidence `json:"operations_executed"`
	ChecksRun          []checkEvidence     `json:"checks_run"`
	Error              string              `json:"error"`
}

// classifyExecutionFailure says what kind of failure execErr was and whether retrying the same plan
// can change it. results are the operations that ran; a failed operation is the last of them.
func classifyExecutionFailure(results []Result, execErr error) (class string, retryable bool) {
	switch {
	case errors.Is(execErr, ErrIndeterminateExecution):
		return FailureIndeterminate, false
	case strings.Contains(execErr.Error(), "plan output budget exceeded"):
		return FailureOutputBudgetExceeded, false
	case errors.Is(execErr, context.DeadlineExceeded):
		return FailureOperationTimeout, true
	}
	if len(results) > 0 && !results[len(results)-1].Success {
		failed := results[len(results)-1]
		if failed.Type == ApplyPatch {
			return FailurePatchNotApplicable, false
		}
		if failed.Type.isCheck() || failed.Type == Gofmt {
			return FailureCheckFailed, false
		}
	}
	if errors.Is(execErr, ErrInvalidOperation) {
		return FailureInvalidOperation, false
	}
	return FailureExecution, true
}

func buildFailureEvidence(taskID, attemptID int64, ops []Operation, results []Result, class string, retryable bool, execErr error) failureEvidence {
	executed := buildAttemptEvidence(taskID, attemptID, ops, results, stagingWorkspaceNone, executionEnvironment{})
	ev := failureEvidence{
		SchemaVersion: failureEvidenceSchemaVersion, TaskID: taskID, AttemptID: attemptID,
		FailureClass: class, Retryable: retryable,
		OperationsExecuted: executed.OperationsExecuted, ChecksRun: executed.ChecksRun,
		Error: boundedFailureError(execErr.Error()),
	}
	if ev.OperationsExecuted == nil {
		ev.OperationsExecuted = []operationEvidence{}
	}
	if ev.ChecksRun == nil {
		ev.ChecksRun = []checkEvidence{}
	}
	if len(results) > 0 && !results[len(results)-1].Success {
		last := executed.OperationsExecuted[len(executed.OperationsExecuted)-1]
		ev.FailedOperation = &last
		ev.FailedCommand = results[len(results)-1].Command
	}
	return ev
}

func boundedFailureError(text string) string {
	const limit = 2000
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && (text[cut]&0xC0) == 0x80 {
		cut--
	}
	return text[:cut] + fmt.Sprintf(" [cut: first %d of %d bytes]", cut, len(text))
}

func recordFailureEvidence(ctx context.Context, queue Queue, recordedBy string, ev failureEvidence) error {
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	var metadata map[string]any
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	_, err = queue.RecordEvidence(ctx, tasks.RecordEvidenceCommand{
		TaskID:     ev.TaskID,
		Type:       tasks.RequirementResult,
		Reference:  fmt.Sprintf("%stask/%d/attempt/%d", FailureEvidenceReferencePrefix, ev.TaskID, ev.AttemptID),
		Digest:     hex.EncodeToString(sum[:]),
		RecordedBy: recordedBy,
		Metadata:   metadata,
	})
	return err
}
