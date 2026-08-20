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
// It already will not: it repromotes blocked tasks solely for the three
// OPERATIONAL reason codes it knows how to resolve, and a coordination hold is
// not an operational problem -- nothing the reconciler can observe tells it
// whether a child's budget and delegation exist. That is a property of one SQL
// predicate, and a property nobody restates is a property nobody notices
// losing, so this test states it.
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
	// The promotable set is pinned as a whole. Adding a fourth reason code is
	// a deliberate act that has to be argued for here first.
	const promotable = "status='blocked' AND status_reason_code IN ('dependency_unsatisfied','dependency_terminal','assignee_unavailable')"
	if !strings.Contains(source, promotable) {
		t.Fatal("the set of blocked reason codes the reconciler repromotes changed; a coordination hold must never be added to it, and any other addition needs its own justification")
	}
}
