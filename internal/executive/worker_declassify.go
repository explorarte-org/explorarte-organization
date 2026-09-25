package executive

import (
	"context"
	"fmt"
)

// The egress rule a design candidate is held to is told to the worker in its contract
// (candidateDeclassificationGuidance: no contiguous span of declassifyMinimumRun characters taken from
// repository text it was shown, comments included) -- and until now enforced only when the candidate
// was ASSEMBLED, after every worker had finished. A violation there is fatal: no worker is left to
// correct it, so the campaign stops (R11, R13).
//
// Root 1326 (2026-09-25), the second run after the departments moved to a new model route, died exactly that way. Its
// worker added a "justification" that restated the docstring of the function it was designing a test
// for -- " function extract_digit_runs (migration 000029) for the same input", 66 characters in common
// with the source it had been shown -- and DeclassifyCandidate refused the assembled candidate. One
// more attempt of that worker, told which passage and why, would have fixed it for the price of one
// call.
//
// So the same rule, over the same text, against the evidence THAT worker was shown, runs at the
// attempt: a violation is a contract rejection the worker reads on its next attempt of the same round.
// It is the early check, not the boundary. DeclassifyCandidate at assembly stays exactly as it was:
// it judges the union of everything every contributing deliverable was shown, which no single
// attempt can see, and it is the gate a passage must pass to leave for a reviewer.

// verifyWorkerDoesNotReproduceSource refuses a design worker's result that reproduces repository
// source it was shown. It applies only while a design freeze is pending, the only phase in which the
// worker's result becomes part of a candidate design.
func (o *Orchestrator) verifyWorkerDoesNotReproduceSource(ctx context.Context, root TaskRecord, snapshotID int64, result InvocationResult) error {
	if !designFreezePending(root) || o.snapshotSources == nil || snapshotID == 0 {
		return nil
	}
	body := deliverableBody(result)
	if body == "" {
		return nil
	}
	shown, err := o.snapshotSources.SnapshotSources(ctx, snapshotID)
	if err != nil {
		// Not "nothing was shown": the check could not be made. The attempt is retried rather than
		// passed on an unverified result.
		return fmt.Errorf("read what snapshot %d showed the worker, to check its result against it: %w", snapshotID, err)
	}
	organizational := make([]OrganizationalSource, 0, len(shown))
	for _, source := range shown {
		if source.Kind == "repository_evidence" && source.Included && source.Content != "" {
			organizational = append(organizational, OrganizationalSource{Reference: source.Reference, Content: source.Content})
		}
	}
	if err = DeclassifyCandidate(body, organizational); err != nil {
		// The refusal names the reference and the length, never the copied text. What the worker
		// needs to read is what to do about it.
		return fmt.Errorf("%w; paraphrase that passage in your own words: a contiguous span of %d or more characters taken from "+
			"repository text you were shown, comments included, cannot cross to the reviewers (paths, symbol names, line ranges, commits "+
			"and repository:// references always can)", err, declassifyMinimumRun)
	}
	return nil
}
