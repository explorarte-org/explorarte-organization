package corpusenrich

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// Store is resumable AND periodically checkpointed -- unlike
// internal/corpuscensus.StateStore's Flush-only-at-the-end design (a
// real gap logged as technical debt during this same corpus's work: a
// multi-hour run that crashes near the end loses all progress). This
// package's Orchestrator calls Flush after every batch, not once at the
// very end, specifically to fix that class of problem for a new
// component rather than repeat it.
type Store struct {
	path    string
	records map[string]AbstractRecord // keyed by PaperID
}

func OpenStore(path string) (*Store, error) {
	store := &Store{path: path, records: make(map[string]AbstractRecord)}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("corpusenrich: open store: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var record AbstractRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, fmt.Errorf("corpusenrich: decode store line: %w", err)
		}
		store.records[record.PaperID] = record
	}
	return store, scanner.Err()
}

func (s *Store) Has(paperID string) bool {
	_, ok := s.records[paperID]
	return ok
}

func (s *Store) Put(record AbstractRecord) { s.records[record.PaperID] = record }

func (s *Store) All() []AbstractRecord {
	out := make([]AbstractRecord, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r)
	}
	return out
}

func (s *Store) Len() int { return len(s.records) }

func (s *Store) Flush() error {
	tmpPath := s.path + ".tmp"
	file, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("corpusenrich: create store temp file: %w", err)
	}
	writer := bufio.NewWriter(file)
	for _, record := range s.records {
		line, err := json.Marshal(record)
		if err != nil {
			file.Close()
			return err
		}
		if _, err := writer.Write(line); err != nil {
			file.Close()
			return err
		}
		if err := writer.WriteByte('\n'); err != nil {
			file.Close()
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.path)
}
