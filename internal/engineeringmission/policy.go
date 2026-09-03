package engineeringmission

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/coderunner"
)

const PolicySchema = "engineering-mission/v1"
const CodeRunnerRole = coderunner.RoleID

type GateType string

const (
	GateBuild   GateType = "GO_BUILD"
	GateVet     GateType = "GO_VET"
	GateTest    GateType = "GO_TEST"
	GateFitness GateType = "FITNESS"
)

type RequiredGate struct {
	Type        GateType `json:"type"`
	Packages    []string `json:"packages,omitempty"`
	Race        bool     `json:"race,omitempty"`
	Integration bool     `json:"integration,omitempty"`
}

type MissionPolicy struct {
	SchemaVersion      string         `json:"schema_version"`
	TaskID             int64          `json:"task_id"`
	BaseSHA            string         `json:"base_sha"`
	Objective          string         `json:"objective"`
	AllowedPaths       []string       `json:"allowed_paths"`
	AcceptanceCriteria []string       `json:"acceptance_criteria"`
	RequiredGates      []RequiredGate `json:"required_gates"`
}

var shaRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (p MissionPolicy) Normalize() (MissionPolicy, error) {
	p.SchemaVersion = strings.TrimSpace(p.SchemaVersion)
	if p.SchemaVersion == "" {
		p.SchemaVersion = PolicySchema
	}
	if p.SchemaVersion != PolicySchema || p.TaskID < 0 || !shaRE.MatchString(p.BaseSHA) || strings.TrimSpace(p.Objective) == "" || len(p.Objective) > 4096 {
		return MissionPolicy{}, fmt.Errorf("invalid engineering mission policy")
	}
	seen := map[string]bool{}
	paths := make([]string, 0, len(p.AllowedPaths))
	for _, raw := range p.AllowedPaths {
		x := path.Clean(strings.TrimSpace(raw))
		if x == "." || x == "" || strings.HasPrefix(x, "/") || x == ".." || strings.HasPrefix(x, "../") || strings.Contains(x, "\\") || x == ".git" || strings.HasPrefix(x, ".git/") || x == "go.mod" || x == "go.sum" {
			return MissionPolicy{}, fmt.Errorf("invalid allowed path %q", raw)
		}
		for _, part := range strings.Split(x, "/") {
			if part == ".git" || part == "go.mod" || part == "go.sum" {
				return MissionPolicy{}, fmt.Errorf("structurally denied allowed path %q", raw)
			}
		}
		if !seen[x] {
			seen[x] = true
			paths = append(paths, x)
		}
	}
	if len(paths) == 0 || len(p.AcceptanceCriteria) == 0 || len(p.RequiredGates) == 0 {
		return MissionPolicy{}, fmt.Errorf("mission policy requires paths, criteria and gates")
	}
	sort.Strings(paths)
	p.AllowedPaths = paths
	for i := range p.AcceptanceCriteria {
		p.AcceptanceCriteria[i] = strings.TrimSpace(p.AcceptanceCriteria[i])
		if p.AcceptanceCriteria[i] == "" || len(p.AcceptanceCriteria[i]) > 2048 {
			return MissionPolicy{}, fmt.Errorf("invalid acceptance criterion")
		}
	}
	for i := range p.RequiredGates {
		g := &p.RequiredGates[i]
		if g.Type != GateBuild && g.Type != GateVet && g.Type != GateTest && g.Type != GateFitness {
			return MissionPolicy{}, fmt.Errorf("unsupported required gate %q", g.Type)
		}
		if g.Type == GateFitness && (len(g.Packages) > 0 || g.Race || g.Integration) {
			return MissionPolicy{}, fmt.Errorf("fitness gate has no configurable fields")
		}
		for _, pkg := range g.Packages {
			if strings.TrimSpace(pkg) == "" || strings.HasPrefix(pkg, "-") || !strings.HasPrefix(pkg, "./") || strings.Contains(pkg, "../") || strings.ContainsAny(pkg, ";|&$`\\") {
				return MissionPolicy{}, fmt.Errorf("unsafe gate package %q", pkg)
			}
		}
	}
	return p, nil
}

func (p MissionPolicy) MarshalEvidence() (map[string]any, string, error) {
	n, err := p.Normalize()
	if err != nil {
		return nil, "", err
	}
	b, err := json.Marshal(n)
	if err != nil {
		return nil, "", err
	}
	h := sha256.Sum256(b)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, "", err
	}
	return m, hex.EncodeToString(h[:]), nil
}

func DecodeEvidence(m map[string]any) (MissionPolicy, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return MissionPolicy{}, err
	}
	var p MissionPolicy
	if err := json.Unmarshal(b, &p); err != nil {
		return MissionPolicy{}, err
	}
	return p.Normalize()
}

func PathAllowed(allowed []string, candidate string) bool {
	candidate = path.Clean(candidate)
	for _, a := range allowed {
		a = path.Clean(a)
		if candidate == a || strings.HasPrefix(candidate, a+"/") {
			return true
		}
	}
	return false
}

func ValidateMutationPaths(p MissionPolicy, patch string) error {
	paths, err := coderunner.ExtractPatchPaths(patch)
	if err != nil {
		return err
	}
	for _, x := range paths {
		if !PathAllowed(p.AllowedPaths, x) {
			return fmt.Errorf("mutation path %q outside allowed paths", x)
		}
	}
	return nil
}
