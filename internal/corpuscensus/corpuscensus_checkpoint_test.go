package corpuscensus

import (
	"path/filepath"
	"testing"
)

func TestStateStoreResumeAfterFailureSkipsAlreadyTerminalRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.jsonl")
	store, err := OpenStateStore(path)
	if err != nil {
		t.Fatal(err)
	}
	store.Put(SilverRecord{ArtifactID: "doc-1", Decision: DecisionAccepted})
	store.Put(SilverRecord{ArtifactID: "doc-2", Decision: DecisionTimeout, PDF: PDFValidation{TimeoutPolicy: TimeoutHard}})
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenStateStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.isTerminal("doc-1") {
		t.Fatal("doc-1 (accepted) should be terminal")
	}
	if !reopened.isTerminal("doc-2") {
		t.Fatal("doc-2 (hard_timeout) should be terminal")
	}
	if reopened.isTerminal("doc-3") {
		t.Fatal("doc-3 was never processed, should not be terminal")
	}
}

func TestStateStoreRetryableTimeoutIsNotTerminal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.jsonl")
	store, err := OpenStateStore(path)
	if err != nil {
		t.Fatal(err)
	}
	store.Put(SilverRecord{ArtifactID: "doc-1", Decision: DecisionTimeout, PDF: PDFValidation{TimeoutPolicy: TimeoutRetryable}})
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStateStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.isTerminal("doc-1") {
		t.Fatal("a retryable_timeout record should not be terminal -- it must be retried on the next run")
	}
}

func TestStateStoreRerunIsIdempotentNotDuplicating(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.jsonl")
	store, err := OpenStateStore(path)
	if err != nil {
		t.Fatal(err)
	}
	store.Put(SilverRecord{ArtifactID: "doc-1", Decision: DecisionAccepted, Title: "v1"})
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	// Simulate a rerun: reopen, overwrite the same ArtifactID, flush again.
	reopened, err := OpenStateStore(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened.Put(SilverRecord{ArtifactID: "doc-1", Decision: DecisionAccepted, Title: "v2"})
	if err := reopened.Flush(); err != nil {
		t.Fatal(err)
	}

	final, err := OpenStateStore(path)
	if err != nil {
		t.Fatal(err)
	}
	all := final.All()
	if len(all) != 1 {
		t.Fatalf("expected exactly 1 record after rerun, got %d: %+v", len(all), all)
	}
	if all[0].Title != "v2" {
		t.Fatalf("expected the rerun's value to win, got %q", all[0].Title)
	}
}
