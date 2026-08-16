//go:build integration

package postgres_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
)

// Concurrency is the property the durable store exists for and the one the
// single-threaded tests cannot observe. Every racer is deliberately told the
// same current ordinal: if optimistic control were wrong, more than one would
// commit and the trajectory would become ambiguous.
func TestConcurrentAppendsCannotProduceAmbiguousHistory(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()

	const racers = 12
	const rounds = 8

	for round := 0; round < rounds; round++ {
		var wg sync.WaitGroup
		results := make([]error, racers)
		start := make(chan struct{})
		for i := 0; i < racers; i++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				<-start
				_, err := f.history.Append(f.ctx, "run-race", uint64(round), f.event("run-race", executionharness.EventModelRequestPrepared))
				results[index] = err
			}(i)
		}
		close(start)
		wg.Wait()

		winners, conflicts := 0, 0
		for _, err := range results {
			switch {
			case err == nil:
				winners++
			case errors.Is(err, executionharness.ErrHistoryConflict):
				conflicts++
			default:
				t.Fatalf("round %d: a contested append failed with an unexpected class: %v", round, err)
			}
		}
		if winners != 1 {
			t.Fatalf("round %d: %d writers committed the same ordinal, want exactly 1 (conflicts=%d)", round, winners, conflicts)
		}
	}

	events, err := f.history.Read(f.ctx, "run-race")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != rounds {
		t.Fatalf("history has %d events after %d contested rounds, want %d", len(events), rounds, rounds)
	}
	for index, event := range events {
		if event.Sequence != uint64(index+1) {
			t.Fatalf("hole or reorder at position %d: sequence=%d", index, event.Sequence)
		}
	}
	var rows int
	if err = f.store.Pool().QueryRow(f.ctx, `SELECT count(*) FROM execution_run_events WHERE run_id='run-race'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != rounds {
		t.Fatalf("the ledger holds %d rows, want %d: a losing writer still committed", rows, rounds)
	}
}

// No legitimate Harness path writes authority_unavailable as a terminal status,
// and the store does not currently refuse one at write time. This pins what
// actually protects the system: a history carrying that impossible state is
// rejected on reload instead of being trusted, and the run produces no side
// effects. If the store ever gains a write-time constraint this test still
// passes through its early branch.
func TestForgedAuthorityUnavailableTerminalFailsClosedOnReload(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()

	spec := restartSpec(f.taskID, f.attemptID)
	spec.Identity.RunID = "run-forged-terminal"

	// A legitimate, non-terminal history: the first check finds authority down.
	runtimeA, modelA, toolsA, closeA := openInstance(t, f.ctx, spec, &outageAuthority{stopAt: 1}, toolThenFinal())
	stopped := runtimeA.Execute(f.ctx, spec)
	closeA()
	if stopped.Status != executionharness.StatusAuthorityUnavailable || modelA.calls != 0 || toolsA.calls != 0 {
		t.Fatalf("setup=%+v model=%d tool=%d", stopped, modelA.calls, toolsA.calls)
	}

	forged := f.event(spec.Identity.RunID, executionharness.EventRunFailed)
	forged.TerminalStatus = executionharness.StatusAuthorityUnavailable
	forged.ErrorCode = "authority_unavailable"
	forged.Reason = "forged terminal"
	if _, err := f.history.Append(f.ctx, spec.Identity.RunID, 1, forged); err != nil {
		t.Logf("the store refused the forged terminal at write time, which is strictly stronger: %v", err)
		return
	}

	runtimeB, modelB, toolsB, closeB := openInstance(t, f.ctx, spec, &outageAuthority{}, toolThenFinal())
	defer closeB()
	got := runtimeB.Execute(f.ctx, spec)
	if got.Status != executionharness.StatusHistoryError {
		t.Fatalf("reload status=%q want history_error: an impossible terminal state was trusted", got.Status)
	}
	if got.Retryable {
		t.Fatal("a corrupt history was reported retryable")
	}
	if modelB.calls != 0 || toolsB.calls != 0 {
		t.Fatalf("a corrupt history produced side effects: model=%d tool=%d", modelB.calls, toolsB.calls)
	}
}
