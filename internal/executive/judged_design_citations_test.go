package executive

import (
	"slices"
	"testing"
)

// Local smoke #34 (root 1773): the adjudicator was issued other windows of the files the design
// stood on and refused the design's claims about "unseen ranges". The department review and the
// adjudication now build their context with the ranges the judged design cites; the worker, which
// judges nothing, builds its context as before.
func TestJudgesAreShownTheRangesTheDesignCites(t *testing.T) {
	fixture := eFixture(t)
	driveCapability(t, fixture, 40)

	fixture.harness.mu.Lock()
	commands := append([]HarnessRunCommand(nil), fixture.harness.commands...)
	fixture.harness.mu.Unlock()
	shown := map[ExecutionPurpose]bool{}
	for _, command := range commands {
		request := fixture.harness.contexts.requests[command.Context.ID]
		switch command.Purpose {
		case PurposeDepartmentWorker:
			if len(request.RepositoryCitations) != 0 {
				t.Fatalf("a worker judges nothing but was handed citations: %v", request.RepositoryCitations)
			}
		case PurposeDepartmentReview, PurposeDesignAdjudication:
			if slices.Contains(request.RepositoryCitations, wiringDefRef) && slices.Contains(request.RepositoryCitations, wiringAppRef) {
				shown[command.Purpose] = true
			}
		}
	}
	for _, purpose := range []ExecutionPurpose{PurposeDepartmentReview, PurposeDesignAdjudication} {
		if !shown[purpose] {
			t.Errorf("no %s was shown the ranges the design cites", purpose)
		}
	}
}
