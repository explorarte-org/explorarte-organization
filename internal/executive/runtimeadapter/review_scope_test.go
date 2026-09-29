package runtimeadapter

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// Local smoke #33 (root 1742): the bundle of the review after a round-2 replan
// carried every worker of the department in every round and the round-1 plan's
// criteria. A scoped review's bundle carries its round's plan and exactly the
// workers the scope names.
func TestAScopedReviewBundleCarriesOnlyItsRoundsPlanAndWorkers(t *testing.T) {
	const dept = "ingenieria_ia"
	all := []executive.TaskRecord{
		{ID: 1744, AssignedUnitID: dept, Status: "completed", IdempotencyKey: "executive:1742:leader-plan:" + dept},
		{ID: 1745, AssignedUnitID: dept, Status: "completed", IdempotencyKey: "executive:1742:worker:" + dept + ":audit"},
		{ID: 1754, AssignedUnitID: dept, Status: "completed", IdempotencyKey: "executive:1742:leader-plan:" + dept + ":design-round:2"},
		{ID: 1755, AssignedUnitID: dept, Status: "completed", IdempotencyKey: "executive:1742:worker:" + dept + ":design-round:2:floor"},
		{ID: 1761, AssignedUnitID: dept, Status: "completed", IdempotencyKey: "executive:1742:worker:" + dept + ":design-round:2:floor_redo-replan:1"},
	}
	scope := &executive.DepartmentReviewScope{PlanTaskID: 1754, WorkerTaskIDs: []int64{1761}}

	ids := func(tasks []executive.TaskRecord) []int64 {
		out := []int64{}
		for _, task := range tasks {
			out = append(out, task.ID)
		}
		return out
	}
	if got := ids(bundledWorkers(all, dept, scope)); !slices.Equal(got, []int64{1761}) {
		t.Fatalf("scoped bundle workers = %v, want only the redo 1761", got)
	}
	if plan := bundledPlan(all, dept, scope.PlanTaskID); plan == nil || plan.ID != 1754 {
		t.Fatalf("scoped bundle plan = %+v, want the round-2 plan 1754", plan)
	}
	// Unscoped, the historical shape: every worker and the first plan.
	if got := ids(bundledWorkers(all, dept, nil)); !slices.Equal(got, []int64{1745, 1755, 1761}) {
		t.Fatalf("unscoped bundle workers = %v", got)
	}
	if plan := bundledPlan(all, dept, 0); plan == nil || plan.ID != 1744 {
		t.Fatalf("unscoped bundle plan = %+v, want the first plan", plan)
	}
}

// Local smoke #34 (root 1773): the review saw the first 1200 bytes of a design summary the worker
// was allowed 8000 for. A validated summary now reaches the review whole, and several whole
// summaries fit one bundle.
func TestAReviewSeesAValidatedWorkerSummaryWhole(t *testing.T) {
	limit := executive.DefaultLimits().WorkerSummaryBytes()
	summary := strings.Repeat("diseño ", limit/len("diseño "))
	if got := boundedWorkerSummary(summary, limit); got != summary {
		t.Fatalf("a %d-byte summary within the %d-byte limit was cut to %d bytes", len(summary), limit, len(got))
	}
	over := summary + strings.Repeat("ñ", 40)
	cut := boundedWorkerSummary(over, limit)
	if !utf8.ValidString(cut) || !strings.Contains(cut, "[cut by the host: first ") {
		t.Fatalf("an over-limit summary must be cut on a rune boundary and say so: %q", cut[len(cut)-80:])
	}
	workers := make([]projectedWorker, 3)
	for i := range workers {
		workers[i] = projectedWorker{TaskID: int64(i + 1), Summary: summary, EvidenceRefs: []string{}, TaskEvidence: []string{}}
	}
	body, err := json.Marshal(departmentEvidenceBundle{SchemaVersion: executiveEvidenceSchema, Workers: workers})
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > executiveEvidenceBundleBytes {
		t.Fatalf("three whole summaries make a %d-byte bundle, over the %d-byte bound", len(body), executiveEvidenceBundleBytes)
	}
}
