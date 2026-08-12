package corpuscuration

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// InputHashOf is the one function that decides "has this Work/cluster
// changed since it was last curated" (owner decision, section 29): a
// deterministic hash of exactly the fields a curation decision actually
// depends on. Changing anything else about a Work (e.g. its
// ArtifactBytes) must NOT invalidate a valid prior curation.
func InputHashOf(workID, clusterID, title string, memberWorkIDs []string) string {
	sorted := append([]string(nil), memberWorkIDs...)
	sort.Strings(sorted)
	payload := struct {
		WorkID    string   `json:"work_id"`
		ClusterID string   `json:"cluster_id"`
		Title     string   `json:"title"`
		Members   []string `json:"members"`
	}{workID, clusterID, title, sorted}
	body, _ := json.Marshal(payload)
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

// Store is this package's resumable, append-only-in-effect JSONL state
// (same discipline as internal/corpuscensus.StateStore -- local file,
// never the organization's Postgres, since a Gold Candidate Set is a
// recommendation, not approved Knowledge yet). Keyed by (WorkID,
// ClusterID) so a Work re-clustered differently gets a fresh curation
// slot rather than silently keeping a stale one.
type Store struct {
	path    string
	records map[string]WorkCuration
}

func recordKey(workID, clusterID string) string { return workID + "\x00" + clusterID }

func OpenStore(path string) (*Store, error) {
	store := &Store{path: path, records: make(map[string]WorkCuration)}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("corpuscuration: open store: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var record WorkCuration
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, fmt.Errorf("corpuscuration: decode store line: %w", err)
		}
		store.records[recordKey(record.WorkID, record.ClusterID)] = record
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("corpuscuration: scan store: %w", err)
	}
	return store, nil
}

// Valid reports whether a stored curation for (workID, clusterID) exists
// AND matches the current InputHash/version pins -- if any version
// changed, the caller must re-curate this Work (owner decision, section
// 30: "invalidar solamente lo necesario", never the whole corpus).
func (s *Store) Valid(workID, clusterID, inputHash string) (WorkCuration, bool) {
	record, ok := s.records[recordKey(workID, clusterID)]
	if !ok {
		return WorkCuration{}, false
	}
	if record.InputHash != inputHash ||
		record.CurationSchemaVersion != CurrentVersions.CurationSchema ||
		record.TaxonomyVersion != CurrentVersions.Taxonomy ||
		record.ClusterAlgorithmVersion != CurrentVersions.ClusterAlgorithm ||
		record.RubricVersion != CurrentVersions.Rubric {
		return WorkCuration{}, false
	}
	return record, true
}

func (s *Store) Put(record WorkCuration) {
	s.records[recordKey(record.WorkID, record.ClusterID)] = record
}

func (s *Store) All() []WorkCuration {
	out := make([]WorkCuration, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r)
	}
	return out
}

func (s *Store) Flush() error {
	tmpPath := s.path + ".tmp"
	file, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("corpuscuration: create store temp file: %w", err)
	}
	writer := bufio.NewWriter(file)
	for _, record := range s.records {
		line, err := json.Marshal(record)
		if err != nil {
			file.Close()
			return fmt.Errorf("corpuscuration: encode record %s/%s: %w", record.WorkID, record.ClusterID, err)
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
		return fmt.Errorf("corpuscuration: flush store: %w", err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.path)
}
