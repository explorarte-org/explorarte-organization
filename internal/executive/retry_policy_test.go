package executive

import "testing"

func TestNoRetriesPinsTaskAttemptsToOne(t *testing.T) {
	orchestrator := &Orchestrator{}
	if got := orchestrator.maxAttempts(3); got != 3 {
		t.Fatalf("default attempts=%d, want 3", got)
	}
	orchestrator.noRetries = true
	if got := orchestrator.maxAttempts(3); got != 1 {
		t.Fatalf("one-shot attempts=%d, want 1", got)
	}
}
