package executive

import (
	"fmt"
	"unicode/utf8"
)

// How much of each design deliverable the reviewers are shown.
//
// Root 1351 (2026-09-26), the first run to reach adversarial review and adjudication, exhausted its
// design rounds on a finding the worker had already answered. The reviewer reported that the candidate
// never stated the target's success criterion (`go test ./internal/identifiers/...` with exit code 0)
// and that its summary stopped mid-sentence. Both were true of what it was shown and false of what the
// worker wrote: candidateBody cut every deliverable at MaxStringBytes (4000) with no mark, and a
// worker-result/v2 deliverable is JSON whose evidence precedes its summary, so a 5449-byte result lost
// the second half of its summary, criterion included. The adjudicator accepted the finding, correctly,
// and the run stopped.
//
// Nothing is cut silently any more. A design worker whose deliverable exceeds what the reviewers can
// be shown is refused at its attempt, with the size and what to do, while it can still answer. At
// assembly each deliverable gets a share of a fixed candidate envelope, and one that still does not
// fit (a result that predates this check, or more workers than the envelope serves in full) is cut on
// a character boundary and says so: the reviewer then knows what it did not see.
const (
	// candidateDeliverableBytes is the most of one design deliverable the reviewers are shown. The
	// worker-result summary alone may take MaxStringBytes (4000); this leaves room for its evidence.
	candidateDeliverableBytes = 12000
	// candidateDesignBytes is the envelope the whole candidate is sized against. It is what the old
	// fixed cut already allowed at seven deliverables (7 x 4000), so review and adjudication
	// instructions stay within sizes the task engine has already accepted.
	candidateDesignBytes = 28000
	// candidateDeliverableFloorBytes is the old fixed cut. No deliverable is shown less of itself
	// than it was before, whatever the number of deliverables.
	candidateDeliverableFloorBytes = 4000
)

// candidateShare is how many bytes of each deliverable the reviewers are shown when the candidate has
// the given number of deliverables.
func candidateShare(deliverables int) int {
	if deliverables < 1 {
		deliverables = 1
	}
	share := candidateDesignBytes / deliverables
	if share > candidateDeliverableBytes {
		share = candidateDeliverableBytes
	}
	if share < candidateDeliverableFloorBytes {
		share = candidateDeliverableFloorBytes
	}
	return share
}

// boundDeliverable returns body unchanged when it fits, and otherwise its first bytes up to max, cut
// on a character boundary, followed by a note saying how much of it the reviewers are shown.
func boundDeliverable(body string, max int) string {
	if max <= 0 || len(body) <= max {
		return body
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return body[:cut] + fmt.Sprintf("\n[deliverable cut by the host: the reviewers are shown its first %d of %d bytes; the rest was not reviewed]",
		cut, len(body))
}

// verifyDeliverableFitsTheReview refuses a design worker's result that is longer than the reviewers
// can be shown, while a design freeze is pending (the only phase in which it becomes part of a
// candidate). It measures deliverableBody, the exact text the assembly will present.
func verifyDeliverableFitsTheReview(root TaskRecord, result InvocationResult) error {
	if !designFreezePending(root) {
		return nil
	}
	size := len(deliverableBody(result))
	if size <= candidateDeliverableBytes {
		return nil
	}
	return fmt.Errorf("%w: this result is %d bytes and the design reviewers are shown at most %d bytes of a design deliverable, "+
		"so the rest would go unreviewed; return the same design in fewer words: keep every name, input, expected output and "+
		"criterion your task states, shorten or merge evidence claims, and do not repeat the summary inside the evidence",
		ErrContractRejected, size, candidateDeliverableBytes)
}
