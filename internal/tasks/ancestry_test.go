package tasks

import (
	"errors"
	"reflect"
	"testing"
)

// Which ancestor states withdraw the work beneath them. Pinned per status so a
// new status cannot silently fall on either side.
func TestBlocksDescendantClaimByStatus(t *testing.T) {
	want := map[Status]bool{
		StatusBlocked: true, StatusCancelled: true, StatusRejected: true, StatusFailed: true, StatusDeadLetter: true,
		// COMPLETED must never block: planning tasks complete so their derived tasks can run.
		StatusCompleted: false, StatusNoAction: false,
		StatusPending: false, StatusReady: false, StatusLeased: false, StatusRunning: false,
		StatusAwaitingVerification: false, StatusRetryWait: false,
	}
	for status := range allStatuses {
		expected, known := want[status]
		if !known {
			t.Fatalf("status %q has no expectation: decide whether it withdraws descendants and add it here", status)
		}
		if got := status.BlocksDescendantClaim(); got != expected {
			t.Errorf("%s.BlocksDescendantClaim() = %v, want %v", status, got, expected)
		}
	}
	if got, want := BlockingAncestorStatuses(), []string{"blocked", "cancelled", "dead_letter", "failed", "rejected"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BlockingAncestorStatuses() = %v, want %v", got, want)
	}
}

// A conversation turn does not own the lifetime of what it requested.
func TestChatTurnDoesNotScopeItsDescendants(t *testing.T) {
	if TaskClassScopesDescendants(TaskClassCEOChatTurn) {
		t.Fatal("a failed chat turn must not withdraw the review it already requested")
	}
	for _, class := range []string{"owner.goal", "coordination.ceo_plan", "coordination.department_plan", "engineering.review", "campaign.financial_review"} {
		if !TaskClassScopesDescendants(class) {
			t.Errorf("%s must scope its descendants", class)
		}
	}
	if got := NonScopingTaskClasses(); len(got) != 1 || got[0] != TaskClassCEOChatTurn {
		t.Fatalf("NonScopingTaskClasses() = %v", got)
	}
}

func TestAncestorBlockBlocksOnlyWhenBothStatusAndScopeSay(t *testing.T) {
	cases := []struct {
		block AncestorBlock
		want  bool
	}{
		{AncestorBlock{TaskID: 1, Status: StatusBlocked, TaskClass: "owner.goal"}, true},
		{AncestorBlock{TaskID: 1, Status: StatusFailed, TaskClass: "owner.goal"}, true},
		{AncestorBlock{TaskID: 1, Status: StatusFailed, TaskClass: TaskClassCEOChatTurn}, false},
		{AncestorBlock{TaskID: 1, Status: StatusCompleted, TaskClass: "owner.goal"}, false},
		{AncestorBlock{TaskID: 1, Status: StatusRunning, TaskClass: "owner.goal"}, false},
	}
	for _, tc := range cases {
		if got := tc.block.Blocks(); got != tc.want {
			t.Errorf("%+v.Blocks() = %v, want %v", tc.block, got, tc.want)
		}
	}
	err := error(AncestorBlock{TaskID: 829, Status: StatusBlocked, TaskClass: "owner.goal"})
	if !errors.Is(err, ErrAncestorBlocked) {
		t.Fatal("an AncestorBlock must satisfy errors.Is(ErrAncestorBlocked)")
	}
}
