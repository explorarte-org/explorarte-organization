package adapter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This rule was copied into six adapters and two copies were wrong, which is
// what a duplicated decision costs: not the duplication itself, but that every
// copy is an independent chance to get it wrong, and nobody notices until one
// of them ends a campaign.
//
// It is asserted at the source rather than through behaviour because the
// failure mode is a NEW adapter, written later, that reaches the same branch
// and decides for itself. No behavioural test can fail for code that does not
// exist yet; this one fails the moment it is written.
func TestEveryAdapterDefersToTheSharedReadFailureRule(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(entry.Name(), "adapter.go")
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		source := string(body)
		// Only adapters that read an HTTP response body face this decision.
		// A CLI adapter has no such branch and must not be forced to import
		// a rule about something it never does.
		index := strings.Index(source, "if readErr != nil {")
		if index < 0 {
			continue
		}
		checked++
		branch := source[index:]
		if end := strings.Index(branch, "\n\t}\n"); end > 0 {
			branch = branch[:end]
		}
		if !strings.Contains(branch, "modelruntime.IsIncompleteRead(readErr)") {
			t.Errorf("%s decides for itself what a failed response read means. "+
				"A deadline or cancellation there leaves the call AMBIGUOUS -- the provider accepted "+
				"the request and may finish and bill it -- and calling it a clean rejection ended two "+
				"campaigns for transient failures. Use modelruntime.IsIncompleteRead.", path)
		}
	}
	// A guard that silently checks nothing is worse than no guard: it reports
	// success for a property it never examined.
	if checked < 5 {
		t.Fatalf("only %d adapters were examined; the guard is looking in the wrong place", checked)
	}
	t.Logf("%d HTTP adapters defer to the shared rule", checked)
}
