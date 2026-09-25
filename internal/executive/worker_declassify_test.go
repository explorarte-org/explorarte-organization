package executive

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
)

// See worker_declassify.go: the egress rule a candidate design is held to is enforced at the
// worker's attempt, where the worker can still correct it, and again at assembly.

const workerDocRef = "repository://explorarte-organization@" + targetSHA + "/internal/identifiers/identifiers.go#L1-L34"

const workerDocSource = "// ExtractDigitRuns returns each maximal run of ASCII digits, in order of occurrence, duplicates included,\n" +
	"// and must match the SQL function extract_digit_runs (migration 000029) for the same input.\n" +
	"func ExtractDigitRuns(text string) []string {\n\treturn nil\n}\n"

func workerResult(summary string) string {
	return `{"schema_version":"worker-result/v1","summary":` + mustJSONString(summary) + `,"evidence_refs":[]}`
}

func workerDocFixture(t *testing.T, summaryFor func(task TaskRecord) string) *wiringFixture {
	t.Helper()
	fixture := newWiringFixture(t, "freeze", []SnapshotSource{wiringSource(workerDocRef, workerDocSource)}, nil)
	fixture.harness.departmentWorkerBody = func(task TaskRecord) string { return workerResult(summaryFor(task)) }
	return fixture
}

func workerTaskOf(t *testing.T, f *wiringFixture) TaskRecord {
	t.Helper()
	all, err := f.tasks.ListByCorrelation(context.Background(), f.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range all {
		if strings.Contains(task.IdempotencyKey, ":worker:ingenieria_ia:design-1") {
			return task
		}
	}
	t.Fatal("the design worker task does not exist")
	return TaskRecord{}
}

// The loop on a campaign: the first attempt restates the docstring, is refused with feedback naming the
// reference, and the retry paraphrases -- inside one design round, with no reviewer spent.
func TestAWorkerThatRestatesADocstringIsCorrectedInsideTheRound(t *testing.T) {
	const copied = "returns each maximal run of ASCII digits, in order of occurrence, duplicates included"
	fixture := workerDocFixture(t, func(task TaskRecord) string {
		for _, attempt := range task.Attempts {
			if strings.Contains(attempt.ResultSummary, "reproduces") {
				return "Design: the new case exercises digits that sit between letters; runs are delimited by non-digits."
			}
		}
		return "Justification against the contract: the documentation states that it " + copied + ", matching the SQL function."
	})

	driveCapability(t, fixture, 40)

	worker := workerTaskOf(t, fixture)
	if len(worker.Attempts) != 2 {
		t.Fatalf("worker attempts=%d, want the refused one and the corrected one", len(worker.Attempts))
	}
	first := worker.Attempts[0]
	if first.State != "failed" {
		t.Fatalf("the reproducing attempt closed as %q", first.State)
	}
	for _, want := range []string{"reproduces", workerDocRef, "paraphrase that passage in your own words", "48 or more characters"} {
		if !strings.Contains(first.ResultSummary, want) {
			t.Errorf("feedback lacks %q: %s", want, first.ResultSummary)
		}
	}
	if strings.Contains(first.ResultSummary, copied) || strings.Contains(first.ResultSummary, "maximal run") {
		t.Fatalf("the refusal repeats the copied source: %s", first.ResultSummary)
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

// What is always allowed stays allowed: path, symbol, line range, commit and the citation.
func TestAWorkerThatOnlyCitesSourceIsNotRefused(t *testing.T) {
	fixture := workerDocFixture(t, func(TaskRecord) string {
		return "The case targets ExtractDigitRuns in internal/identifiers/identifiers.go lines 1-34 at " + targetSHA +
			", grounded by " + workerDocRef + "; its parity with the SQL function is stated as intent."
	})

	driveCapability(t, fixture, 40)

	worker := workerTaskOf(t, fixture)
	if len(worker.Attempts) != 1 || worker.Status != "completed" {
		t.Fatalf("a citing worker was refused: attempts=%d status=%s", len(worker.Attempts), worker.Status)
	}
}

func TestTheAttemptCheckIsScopedAndFailsClosed(t *testing.T) {
	copying := InvocationResult{JSONOutput: []byte(workerResult(
		"the documentation says it returns each maximal run of ASCII digits, in order of occurrence, duplicates included, ok"))}
	pending := TaskRecord{Requirements: []RequirementRecord{{Key: designfreeze.RequirementKey, Status: "pending"}}}
	satisfied := TaskRecord{Requirements: []RequirementRecord{{Key: designfreeze.RequirementKey, Status: "satisfied"}}}
	shown := []SnapshotSource{{Kind: "repository_evidence", Reference: workerDocRef, Included: true, Content: workerDocSource}}
	check := func(root TaskRecord, sources SnapshotSourceReader, result InvocationResult) error {
		return (&Orchestrator{snapshotSources: sources}).verifyWorkerDoesNotReproduceSource(context.Background(), root, 7, result)
	}

	err := check(pending, stubSnapshotSources{sources: shown}, copying)
	if !errors.Is(err, ErrCandidateContaminated) || !errors.Is(err, ErrContractRejected) {
		t.Fatalf("a reproduction under a pending freeze was not refused as a contract rejection: %v", err)
	}
	// The refusal is persisted and read back by the next attempt: no 20-character window of the
	// source may appear in it, whatever part of the source was copied.
	message := strings.ToLower(err.Error())
	source := strings.ToLower(workerDocSource)
	for start := 0; start+20 <= len(source); start++ {
		if window := source[start : start+20]; strings.Contains(message, window) {
			t.Fatalf("the refusal carries source text %q: %s", window, err)
		}
	}
	if err := check(TaskRecord{}, stubSnapshotSources{sources: shown}, copying); err != nil {
		t.Errorf("a run with no design freeze was checked: %v", err)
	}
	if err := check(satisfied, stubSnapshotSources{sources: shown}, copying); err != nil {
		t.Errorf("a satisfied freeze was checked: %v", err)
	}
	// Only evidence the worker was actually shown counts, and only repository evidence.
	notShown := []SnapshotSource{{Kind: "repository_evidence", Reference: workerDocRef, Included: false, Content: workerDocSource}}
	if err := check(pending, stubSnapshotSources{sources: notShown}, copying); err != nil {
		t.Errorf("evidence that was not included in the context was held against the worker: %v", err)
	}
	otherKind := []SnapshotSource{{Kind: "goal", Reference: "goal", Included: true, Content: workerDocSource}}
	if err := check(pending, stubSnapshotSources{sources: otherKind}, copying); err != nil {
		t.Errorf("text that is not repository evidence was held against the worker: %v", err)
	}
	// Not being able to read what was shown is not "nothing was shown".
	if err := check(pending, stubSnapshotSources{err: errors.New("snapshot store unavailable")}, copying); err == nil || !strings.Contains(err.Error(), "snapshot store unavailable") {
		t.Errorf("an unreadable snapshot passed the check: %v", err)
	}
	// Nothing wired, nothing to compare against.
	if err := check(pending, nil, copying); err != nil {
		t.Errorf("without a snapshot reader the check refused: %v", err)
	}
	// The text judged is exactly what assembly judges.
	if got := deliverableBody(InvocationResult{TextOutput: "  text  ", JSONOutput: []byte(`{"a":1}`)}); got != "text" {
		t.Errorf("deliverableBody prefers text output: %q", got)
	}
	if got := deliverableBody(InvocationResult{JSONOutput: []byte(` {"a":1} `)}); got != `{"a":1}` {
		t.Errorf("deliverableBody falls back to JSON output: %q", got)
	}
}
