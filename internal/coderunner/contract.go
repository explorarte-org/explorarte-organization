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

// Mutates reports whether an operation of this type can change the staging
// workspace. It is a closed switch over the fixed operation surface (no
// dynamic tool registry): used to classify evidence, to decide which
// checks a mutation invalidates, and as the basis for any future
// read/write policy.
func (t OperationType) Mutates() bool {
	switch t {
	case ApplyPatch, Gofmt:
		return true
	case ReadFile, Search, GitDiff, GitStatus, GoBuild, GoVet, GoTest:
		return false
	default:
		// Fail closed: an operation type this switch does not recognize is
		// treated as mutating so it can never silently skip verification
		// invalidation.
		return true
	}
}

// isCheck reports whether an operation type can be used as proof of
// correctness for verification-invalidation purposes.
func (t OperationType) isCheck() bool {
	switch t {
	case GoBuild, GoVet, GoTest:
		return true
	default:
		return false
	}
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
			if structurallyDenied(op.Path, op.Type.Mutates()) {
				return fmt.Errorf("path %q is structurally denied for %s", op.Path, op.Type)
			}
		}
		for _, pkg := range op.Packages {
			if err := validatePackage(pkg); err != nil {
				return err
			}
		}
		if op.Type == ApplyPatch {
			if strings.TrimSpace(op.Patch) == "" {
				return fmt.Errorf("patch required")
			}
			if err := validatePatchPaths(op.Patch); err != nil {
				return err
			}
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
