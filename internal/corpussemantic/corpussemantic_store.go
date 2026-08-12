package corpussemantic

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

type Store struct {
	path    string
	records map[string]EmbeddingRecord
}

func OpenStore(path string) (*Store, error) {
	store := &Store{path: path, records: make(map[string]EmbeddingRecord)}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("corpussemantic: open store: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var record EmbeddingRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, fmt.Errorf("corpussemantic: decode store line: %w", err)
		}
		store.records[record.WorkID] = record
	}
	return store, scanner.Err()
}

// Valid reports whether WorkID already has an embedding computed under
// the exact same InputHash -- resumability with automatic invalidation
// if the semantic input changed (e.g. a title/abstract correction).
func (s *Store) Valid(workID, inputHash string) (EmbeddingRecord, bool) {
	record, ok := s.records[workID]
	if !ok || record.InputHash != inputHash {
		return EmbeddingRecord{}, false
	}
	return record, true
}

func (s *Store) Put(record EmbeddingRecord) { s.records[record.WorkID] = record }

func (s *Store) All() []EmbeddingRecord {
	out := make([]EmbeddingRecord, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r)
	}
	return out
}

func (s *Store) Flush() error {
	tmpPath := s.path + ".tmp"
	file, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("corpussemantic: create store temp file: %w", err)
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
