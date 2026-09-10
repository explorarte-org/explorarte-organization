package postgres

import (
	"os"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// The readiness reconciler is the one actor that can make a task claimable
// without anybody asking it to, so the publication barrier only holds if the
// reconciler will not lift it.
//
// It already will not: it repromotes blocked tasks solely for the OPERATIONAL
// reason codes it knows how to resolve, and a coordination hold is not an
// operational problem -- nothing the reconciler can observe tells it whether
// a child's budget and delegation exist. That is a property of one SQL
// predicate, and a property nobody restates is a property nobody notices
// losing, so this test states it.
//
// 'capacity' (CAPACITY_EXHAUSTION_SCHEDULING_V1) was added as the fourth
// reason code deliberately: it is exactly as operational as the other three
// -- a purely system-derived signal (pool candidate availability, re-read
// fresh from durable capacity state at repromotion time), never a human or
// business decision the way a coordination hold's release is. Repromoting
// it here is what lets a temporarily-capacity-exhausted task wake through
// the SAME durable, available_at-gated path every other blocked reason
// already wakes through, with no separate timer or poller.
//
// It reads the query text because the behaviour lives in SQL and this package
// has no database in unit tests. That is a real limit: it proves the predicate
// still says what it said, not that Postgres executes it that way. The
// integration suite covers execution; this covers the edit that would silently
// widen the predicate.
func TestReadinessReconcilerDoesNotLiftTheCoordinationHold(t *testing.T) {
	body, err := os.ReadFile("reconcile.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	if strings.Contains(source, tasks.ReasonCodeCoordinationHold) {
		t.Fatalf("the readiness reconciler now mentions %q: publishing a held child is its creator's decision, never a background sweep's", tasks.ReasonCodeCoordinationHold)
	}
	// The promotable set is pinned as a whole. Adding a further reason code is
	// a deliberate act that has to be argued for here first.
	const promotable = "status='blocked' AND status_reason_code IN ('dependency_unsatisfied','dependency_terminal','assignee_unavailable','capacity')"
	if !strings.Contains(source, promotable) {
		t.Fatal("the set of blocked reason codes the reconciler repromotes changed; a coordination hold must never be added to it, and any other addition needs its own justification")
	}
}
