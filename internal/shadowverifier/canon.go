package shadowverifier

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// LeaderWorkerDepartment mirrors one leader-worker-map.yaml departments
// entry: the canonical statement of which leader delegates over which
// workers, used as the cross-check source of truth for may_delegate.
type LeaderWorkerDepartment struct {
	ID      string   `yaml:"id"`
	Leader  string   `yaml:"leader"`
	Workers []string `yaml:"workers"`
}

// LeaderWorkerMapFact mirrors leader-worker-map.yaml. Transversal units are
// accepted (they carry no leader) but contribute no delegation pairs.
type LeaderWorkerMapFact struct {
	SchemaVersion  string                   `yaml:"schema_version"`
	DocumentStatus string                   `yaml:"document_status"`
	Departments    []LeaderWorkerDepartment `yaml:"departments"`
	Transversal    map[string][]string      `yaml:"transversal"`
}

// LoadLeaderWorkerMap reads leader-worker-map.yaml with the shadow's own
// parser (never registry's), failing closed on structural problems.
func LoadLeaderWorkerMap(canonicalDir string) (LeaderWorkerMapFact, error) {
	path := strings.TrimRight(canonicalDir, "/") + "/leader-worker-map.yaml"
	body, err := os.ReadFile(path)
	if err != nil {
		return LeaderWorkerMapFact{}, fmt.Errorf("%w: %v", ErrMatrixUnavailable, err)
	}
	var file LeaderWorkerMapFact
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil {
		return LeaderWorkerMapFact{}, fmt.Errorf("%w: parse leader-worker-map: %v", ErrMatrixInvalid, err)
	}
	if len(file.Departments) == 0 {
		return LeaderWorkerMapFact{}, fmt.Errorf("%w: leader-worker-map lists no departments", ErrMatrixInvalid)
	}
	return file, nil
}
