// Package completionadapter maps internal/completion's independent verifier
// onto the V2 completion permission port.
package completionadapter

import (
	"context"

	"github.com/Mireuz13/explorarte-organization/internal/completion"
	"github.com/Mireuz13/explorarte-organization/internal/workflowruntime"
)

type Adapter struct{ Service *completion.Service }

func (a Adapter) Verify(ctx context.Context, taskID, attemptID int64) (workflowruntime.CompletionDecision, error) {
	result, err := a.Service.Verify(ctx, completion.VerificationRequest{TaskID: taskID, AttemptID: attemptID})
	if err != nil {
		return workflowruntime.CompletionDecision{}, err
	}
	disposition := workflowruntime.CompletionBlocked
	switch result.Verdict {
	case completion.VerdictPass:
		disposition = workflowruntime.CompletionAllow
	case completion.VerdictFail:
		disposition = workflowruntime.CompletionDeny
	case completion.VerdictInconclusive:
		disposition = workflowruntime.CompletionBlocked
	}
	reason := ""
	for _, obligation := range result.Obligations {
		if obligation.Label == completion.LabelContradicted || obligation.Label == completion.LabelUnknown {
			reason = obligation.Detail
			break
		}
	}
	return workflowruntime.CompletionDecision{
		TaskID: taskID, AttemptID: attemptID, Disposition: disposition, Reason: reason,
		Provenance: "internal/completion",
	}, nil
}

var _ workflowruntime.CompletionPort = Adapter{}
