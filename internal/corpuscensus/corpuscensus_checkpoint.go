package corpuscensus

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// StateStore is this package's ONLY durable output: an append-only JSONL
// file, one SilverRecord per line, keyed by ArtifactID (the harvester's
// canonical_id). It is local to wherever this package runs -- never
// written to the organization's Postgres (owner decision: this stage
// produces a census/recommendation, not Knowledge state) and never
// mutates the harvester's own SQLite DB (Bronze stays immutable).
//
// Resumability (owner decision, section 11): Load() reads every prior
// run's records into memory; Orchestrator skips any ArtifactID already
// present with a terminal decision, so re-running after a crash at
// document 84 of however many does not restart from 1, and reprocessing
// an already-terminal document does not duplicate its state (Append
// overwrites the in-memory map entry and the file is fully rewritten on
// Flush, not blindly appended to, so a doc processed twice yields one
// line, not two).
type StateStore struct {
	path    string
	records map[string]SilverRecord // keyed by ArtifactID
}

func OpenStateStore(path string) (*StateStore, error) {
	store := &StateStore{path: path, records: make(map[string]SilverRecord)}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("corpuscensus: open state store: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var record SilverRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, fmt.Errorf("corpuscensus: decode state line: %w", err)
		}
		store.records[record.ArtifactID] = record
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("corpuscensus: scan state store: %w", err)
	}
	return store, nil
}

// isTerminal reports whether ArtifactID already has a decision this
// orchestrator should not redo. retryable_timeout is deliberately NOT
// terminal -- a document that hit the retry bound last run should be
// retried again on the next invocation, not stuck forever.
func (s *StateStore) isTerminal(artifactID string) bool {
	record, ok := s.records[artifactID]
	if !ok {
		return false
	}
	return record.PDF.TimeoutPolicy != TimeoutRetryable || record.Decision != DecisionTimeout
}

func (s *StateStore) Get(artifactID string) (SilverRecord, bool) {
	record, ok := s.records[artifactID]
	return record, ok
}

func (s *StateStore) Put(record SilverRecord) {
	s.records[record.ArtifactID] = record
}

func (s *StateStore) All() []SilverRecord {
	out := make([]SilverRecord, 0, len(s.records))
	for _, record := range s.records {
		out = append(out, record)
	}
	return out
}

// Flush rewrites the whole state file from the in-memory map -- simplest
// way to guarantee "reprocessing a document does not duplicate its
// line," at the cost of an O(n) rewrite per flush. Fine at this corpus's
// scale (~1,100 documents); would need revisiting well before six
// figures of documents.
func (s *StateStore) Flush() error {
	tmpPath := s.path + ".tmp"
	file, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("corpuscensus: create state store temp file: %w", err)
	}
	writer := bufio.NewWriter(file)
	for _, record := range s.records {
		line, err := json.Marshal(record)
		if err != nil {
			file.Close()
			return fmt.Errorf("corpuscensus: encode state record %s: %w", record.ArtifactID, err)
		}
		if _, err := writer.Write(line); err != nil {
			file.Close()
			return fmt.Errorf("corpuscensus: write state record %s: %w", record.ArtifactID, err)
		}
		if err := writer.WriteByte('\n'); err != nil {
			file.Close()
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		file.Close()
		return fmt.Errorf("corpuscensus: flush state store: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("corpuscensus: close state store temp file: %w", err)
	}
	return os.Rename(tmpPath, s.path)
}
