package coderunner

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

const SchemaVersion = "code-runner-execution/v1"

type OperationType string

const (
	ReadFile   OperationType = "READ_FILE"
	Search     OperationType = "SEARCH"
	ApplyPatch OperationType = "APPLY_PATCH"
	Gofmt      OperationType = "GOFMT"
	GoBuild    OperationType = "GO_BUILD"
	GoVet      OperationType = "GO_VET"
	GoTest     OperationType = "GO_TEST"
	GitDiff    OperationType = "GIT_DIFF"
	GitStatus  OperationType = "GIT_STATUS"
)

type Operation struct {
	Type        OperationType `json:"type"`
	Path        string        `json:"path,omitempty"`
	Pattern     string        `json:"pattern,omitempty"`
	Patch       string        `json:"patch,omitempty"`
	Packages    []string      `json:"packages,omitempty"`
	Race        bool          `json:"race,omitempty"`
	Integration bool          `json:"integration,omitempty"`
}
type Plan struct {
	SchemaVersion string      `json:"schema_version"`
	Operations    []Operation `json:"operations"`
}

func (p Plan) Validate() error {
	if p.SchemaVersion != SchemaVersion || len(p.Operations) == 0 || len(p.Operations) > 100 {
		return fmt.Errorf("invalid code-runner plan")
	}
	for _, op := range p.Operations {
		switch op.Type {
		case ReadFile, Search, ApplyPatch, Gofmt, GoBuild, GoVet, GoTest, GitDiff, GitStatus:
		default:
			return fmt.Errorf("unsupported operation %q", op.Type)
		}
		if op.Path != "" {
			if err := SafePath(op.Path); err != nil {
				return err
			}
		}
		for _, pkg := range op.Packages {
			if err := validatePackage(pkg); err != nil {
				return err
			}
		}
		if op.Type == ApplyPatch && strings.TrimSpace(op.Patch) == "" {
			return fmt.Errorf("patch required")
		}
	}
	return nil
}

func SafePath(value string) error {
	if value == "" || filepath.IsAbs(value) || value == "." || strings.Contains(filepath.Clean(value), "..") {
		return fmt.Errorf("unsafe workspace path")
	}
	return nil
}

func ParsePlan(data []byte) (Plan, error) {
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return p, err
	}
	if err := p.Validate(); err != nil {
		return p, err
	}
	return p, nil
}
