package executive

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
)

// See design_candidate_budget.go: no design deliverable reaches the reviewers cut without saying so.

const root1351Criterion = "after design freeze and the implementation plan, success requires the code runner's durable evidence of `go test ./internal/identifiers/...` with exit code 0"

// root1351Deliverable has the shape that lost root 1351 its criterion: a worker-result JSON whose
// evidence precedes a summary that states the criterion past its first 4000 bytes of the whole body.
func root1351Deliverable() string {
	evidence := `[` + strings.Repeat(`{"claim":"ExtractDigitRuns returns the runs of ASCII digits in the order they occur","ref":"`+workerDocRef+`"},`, 12)
	evidence = strings.TrimSuffix(evidence, ",") + `]`
	summary := "Design: add one case named \"digits adjacent to letters\" with text \"abc123def45\" and want []string{\"123\", \"45\"}. " +
		strings.Repeat("The case exercises digit runs delimited by letters on both sides. ", 25) + root1351Criterion + "."
	return `{"evidence":` + evidence + `,"evidence_refs":[],"schema_version":"worker-result/v2","summary":` + mustJSONString(summary) + `}`
}

func TestRoot1351TheReviewerIsShownTheWholeDeliverable(t *testing.T) {
	deliverable := root1351Deliverable()
	if len(deliverable) <= 4000 || strings.Index(deliverable, root1351Criterion) < 4000 {
		t.Fatalf("fixture does not reproduce root 1351: %d bytes, criterion at %d", len(deliverable), strings.Index(deliverable, root1351Criterion))
	}
	if len(deliverable) > candidateDeliverableBytes {
		t.Fatalf("fixture is %d bytes, over the deliverable limit", len(deliverable))
	}
	o, _ := bodyOrchestrator(map[int64]InvocationResult{
		100: {InvocationID: 100, JSONOutput: []byte(deliverable), ResponseHash: "aaa"},
		101: {InvocationID: 101, TextOutput: "fine", ResponseHash: "bbb"},
	})
	body, err := o.candidateBody(context.Background(), twoUnitArtifact())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, root1351Criterion) {
		t.Fatal("the criterion the worker wrote did not reach the reviewer")
	}
	if !strings.Contains(body, deliverable) {
		t.Fatal("the deliverable was not shown whole")
	}
	if strings.Contains(body, "deliverable cut by the host") {
		t.Fatalf("a deliverable that fits was marked as cut")
	}
}

func TestEachDeliverableGetsAShareThatIsNeverLessThanBefore(t *testing.T) {
	for deliverables, want := range map[int]int{0: 12000, 1: 12000, 2: 12000, 3: 9333, 4: 7000, 7: 4000, 8: 4000, 24: 4000} {
		if got := candidateShare(deliverables); got != want {
			t.Errorf("candidateShare(%d) = %d, want %d", deliverables, got, want)
		}
		if got := candidateShare(deliverables); got < candidateDeliverableFloorBytes {
			t.Errorf("candidateShare(%d) = %d is below the old fixed cut", deliverables, got)
		}
		if deliverables >= 1 && deliverables <= 7 && deliverables*candidateShare(deliverables) > candidateDesignBytes {
			t.Errorf("%d deliverables of %d bytes exceed the %d-byte envelope", deliverables, candidateShare(deliverables), candidateDesignBytes)
		}
	}
}

func TestADeliverableThatDoesNotFitIsCutVisiblyOnACharacterBoundary(t *testing.T) {
	// 'é' is two bytes: a cut at an odd offset would land inside one.
	body := strings.Repeat("é", 10)
	cut := boundDeliverable(body, 7)
	if !utf8.ValidString(cut) {
		t.Fatalf("the cut split a character: %q", cut)
	}
	if !strings.HasPrefix(cut, strings.Repeat("é", 3)+"\n[") {
		t.Fatalf("the cut did not keep the whole characters before the limit: %q", cut)
	}
	if !strings.Contains(cut, "first 6 of 20 bytes") || !strings.Contains(cut, "the rest was not reviewed") {
		t.Fatalf("the cut does not say what the reviewers were not shown: %q", cut)
	}
	if got := boundDeliverable("short", 7); got != "short" {
		t.Fatalf("a body that fits was changed: %q", got)
	}

	o, _ := bodyOrchestrator(map[int64]InvocationResult{
		100: {InvocationID: 100, TextOutput: strings.Repeat("x", candidateDeliverableBytes+500), ResponseHash: "aaa"},
		101: {InvocationID: 101, TextOutput: "fine", ResponseHash: "bbb"},
	})
	assembled, err := o.candidateBody(context.Background(), twoUnitArtifact())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(assembled, "x") != candidateDeliverableBytes {
		t.Fatalf("the reviewers were shown %d bytes of the deliverable, want %d", strings.Count(assembled, "x"), candidateDeliverableBytes)
	}
	if !strings.Contains(assembled, "deliverable cut by the host: the reviewers are shown its first 12000 of 12500 bytes") {
		t.Fatalf("the cut is silent: %s", assembled[len(assembled)-300:])
	}
	if !strings.HasSuffix(assembled, "\nfine") {
		t.Fatal("the next deliverable was lost after a cut one")
	}
}

func TestTheSizeCheckIsScopedToAPendingFreeze(t *testing.T) {
	pending := TaskRecord{Requirements: []RequirementRecord{{Key: designfreeze.RequirementKey, Status: "pending"}}}
	satisfied := TaskRecord{Requirements: []RequirementRecord{{Key: designfreeze.RequirementKey, Status: "satisfied"}}}
	over := InvocationResult{TextOutput: strings.Repeat("x", candidateDeliverableBytes+1)}
	atLimit := InvocationResult{TextOutput: strings.Repeat("x", candidateDeliverableBytes)}

	err := verifyDeliverableFitsTheReview(pending, over)
	if !errors.Is(err, ErrContractRejected) {
		t.Fatalf("an oversized design deliverable was not refused as a contract rejection: %v", err)
	}
	for _, want := range []string{"12001 bytes", "at most 12000 bytes", "fewer words"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q: %s", want, err)
		}
	}
	if err := verifyDeliverableFitsTheReview(pending, atLimit); err != nil {
		t.Errorf("a deliverable at the limit was refused: %v", err)
	}
	if err := verifyDeliverableFitsTheReview(satisfied, over); err != nil {
		t.Errorf("a satisfied freeze was checked: %v", err)
	}
	if err := verifyDeliverableFitsTheReview(TaskRecord{}, over); err != nil {
		t.Errorf("a run with no design freeze was checked: %v", err)
	}
	// It measures what the assembly will present: the text output, or the JSON when there is none.
	jsonOver := InvocationResult{JSONOutput: []byte(`{"summary":"` + strings.Repeat("x", candidateDeliverableBytes) + `"}`)}
	if err := verifyDeliverableFitsTheReview(pending, jsonOver); !errors.Is(err, ErrContractRejected) {
		t.Errorf("an oversized JSON deliverable was not refused: %v", err)
	}
}

// The loop on a campaign: the first attempt is longer than the reviewers can be shown, is refused with
// its size, and the shorter retry completes inside the same design round.
func TestAnOversizedDesignDeliverableIsCorrectedInsideTheRound(t *testing.T) {
	const design = "Design: the new case exercises digits that sit between letters; runs are delimited by non-digits."
	fixture := newWiringFixture(t, "freeze", []SnapshotSource{wiringSource(workerDocRef, workerDocSource)}, nil)
	fixture.harness.departmentWorkerBody = func(task TaskRecord) string {
		for _, attempt := range task.Attempts {
			if strings.Contains(attempt.ResultSummary, "fewer words") {
				return workerResult(design)
			}
		}
		// Valid JSON, but padded the way a pretty-printing model pads it: every byte is shown.
		return `{"schema_version":"worker-result/v1",` + strings.Repeat(" ", candidateDeliverableBytes) +
			`"summary":` + mustJSONString(design) + `,"evidence_refs":[]}`
	}

	driveCapability(t, fixture, 40)

	worker := workerTaskOf(t, fixture)
	if len(worker.Attempts) != 2 {
		t.Fatalf("worker attempts=%d, want the refused one and the corrected one", len(worker.Attempts))
	}
	if first := worker.Attempts[0]; first.State != "failed" || !strings.Contains(first.ResultSummary, "at most 12000 bytes") {
		t.Fatalf("the oversized attempt closed as %q with %q", first.State, first.ResultSummary)
	}
	if worker.Status != "completed" {
		t.Fatalf("the corrected worker did not complete: %s", worker.Status)
	}
	all, err := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range all {
		if designRoundOf(task.IdempotencyKey) >= 2 {
			t.Fatalf("the correction cost a design round: %s", task.IdempotencyKey)
		}
	}
	if designFreezePending(fixture.rootRecord(t)) {
		t.Fatal("the corrected design never reached the freeze")
	}
}
