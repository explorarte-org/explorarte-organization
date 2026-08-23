package executive_test

import (
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/programbudget"
)

// programbudget restates the owner-goal class rather than importing the
// executive, so that it stays free of a dependency on the package whose work
// it budgets. This is the guard that keeps the two copies identical.
//
// A drift here would not fail anything visibly: budget lookups would quietly
// stop finding the campaign root by its declared class and fall back to
// "lowest id in the correlation", which is a statement about insertion order
// and not about who authorised the spend.
func TestOwnerGoalClassIsTheSameOnBothSides(t *testing.T) {
	if executive.TaskClassOwnerGoal != programbudget.TaskClassOwnerGoal {
		t.Fatalf("owner goal class drifted: executive=%q programbudget=%q",
			executive.TaskClassOwnerGoal, programbudget.TaskClassOwnerGoal)
	}
}
