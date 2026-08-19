// Package missionplan is the host translator between a frozen implementation
// plan and a governed engineering mission.
//
// It exists because the gap it closes was a real one: CodeRunner parses a
// task's Instructions directly as its execution plan, so anything that reached
// Instructions became executable. A model that could write there could choose
// its own operations. Here, the model supplies CONTENT -- what it wants
// changed and the patch to do it -- and the host supplies AUTHORITY: which
// paths may be touched, which gates must pass, and which commit the work is
// based on. Neither half can produce a mission on its own.
//
// The package is pure: no I/O, no clock, no ports. The same plan always
// derives the same policy and the same operations, which is what lets a
// mission be re-derived and compared after the fact.
package missionplan

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/coderunner"
	"github.com/Mireuz13/explorarte-organization/internal/engineeringmission"
)

var (
	// ErrScopeDenied means a declared path is outside what this mission's
	// scope may ever touch.
	ErrScopeDenied = fmt.Errorf("missionplan: path is outside the mission scope")
	// ErrPathForbidden means a declared path is in the structural denylist --
	// governance, secrets, infrastructure. No scope widens it.
	ErrPathForbidden = fmt.Errorf("missionplan: path is structurally forbidden")
	// ErrPlanInvalid means the plan cannot produce a mission at all.
	ErrPlanInvalid = fmt.Errorf("missionplan: implementation plan is not usable")
)

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Scope is the host's own classification of how far a mission may reach. It is
// assigned by the host from the task, never proposed by the model: a scope the
// model could name is not a boundary.
type Scope string

const (
	// ScopeDocumentation may only write implementation documentation. It is
	// the scope an autonomy smoke runs under.
	ScopeDocumentation Scope = "documentation"
	// ScopeInternalCode may write Go under internal/ and cmd/, plus its own
	// documentation.
	ScopeInternalCode Scope = "internal_code"
)

// scopePrefixes is the positive allowlist per scope. A path must match one of
// these AND survive the denylist below.
var scopePrefixes = map[Scope][]string{
	ScopeDocumentation: {"docs/implementation/"},
	ScopeInternalCode:  {"internal/", "cmd/", "docs/implementation/"},
}

// forbiddenPrefixes is denied under every scope, without exception. These are
// the paths where a change is not "code" but a change to what the organization
// is permitted to do, or to the material that lets it do anything at all.
//
// docs/canonical, migrations, scripts and deployments are governance: the
// registry, the schema, the fitness gates and the runtime topology. Changing
// them through an autonomous mission would let the system widen its own
// authority in the same motion that uses it.
var forbiddenPrefixes = []string{
	".git/", ".github/", "secrets/", "docs/canonical/", "migrations/",
	"scripts/", "deployments/", "config/", ".claude/",
}

// forbiddenExact catches single files rather than trees.
var forbiddenExact = []string{
	"go.mod", "go.sum", "compose.yaml", "compose.integration.yaml",
	".env", ".env.example", "Dockerfile", "Makefile", "AGENT.md",
}

// Change is one file the plan wants to modify.
type Change struct {
	Path   string
	Intent string
	Patch  string
}

// Request is everything the translator is allowed to consider. BaseSHA and
// Scope are host inputs; Objective, Changes and AcceptanceCriteria come from
// the frozen plan.
type Request struct {
	TaskID             int64
	BaseSHA            string
	Scope              Scope
	Objective          string
	Changes            []Change
	AcceptanceCriteria []string
}

// Derived is the pair the mission is created from.
type Derived struct {
	Policy engineeringmission.MissionPolicy
	Plan   coderunner.Plan
}

const (
	maxChangesPerMission = 24
	maxPatchBytes        = 256 << 10
)

// Derive turns a frozen implementation plan into a mission policy and a
// validated CodeRunner plan. It fails closed on anything it cannot justify.
func Derive(request Request) (Derived, error) {
	if request.TaskID <= 0 {
		return Derived{}, fmt.Errorf("%w: task id is required", ErrPlanInvalid)
	}
	// A 40-char SHA and nothing else. HEAD, a branch name or "latest" would
	// make the mission's base whatever the repository happened to be when it
	// ran, which is not a base at all.
	if !fullSHA.MatchString(request.BaseSHA) {
		return Derived{}, fmt.Errorf("%w: base sha must be a full 40-character commit id", ErrPlanInvalid)
	}
	prefixes, known := scopePrefixes[request.Scope]
	if !known {
		return Derived{}, fmt.Errorf("%w: unknown scope %q", ErrPlanInvalid, request.Scope)
	}
	if strings.TrimSpace(request.Objective) == "" || len(request.Objective) > 4096 {
		return Derived{}, fmt.Errorf("%w: objective is missing or oversized", ErrPlanInvalid)
	}
	if len(request.Changes) == 0 || len(request.Changes) > maxChangesPerMission {
		return Derived{}, fmt.Errorf("%w: %d changes", ErrPlanInvalid, len(request.Changes))
	}
	if len(request.AcceptanceCriteria) == 0 {
		return Derived{}, fmt.Errorf("%w: acceptance criteria are required", ErrPlanInvalid)
	}

	seen := map[string]struct{}{}
	allowed := make([]string, 0, len(request.Changes))
	operations := make([]coderunner.Operation, 0, len(request.Changes)+4)
	touchesGo := false

	for i, change := range request.Changes {
		clean, err := normalizePath(change.Path)
		if err != nil {
			return Derived{}, fmt.Errorf("change[%d]: %w", i, err)
		}
		if err = denied(clean); err != nil {
			return Derived{}, fmt.Errorf("change[%d] %q: %w", i, clean, err)
		}
		if !withinScope(clean, prefixes) {
			return Derived{}, fmt.Errorf("change[%d] %q: %w (%s)", i, clean, ErrScopeDenied, request.Scope)
		}
		if strings.TrimSpace(change.Patch) == "" || len(change.Patch) > maxPatchBytes {
			return Derived{}, fmt.Errorf("%w: change[%d] patch is empty or oversized", ErrPlanInvalid, i)
		}
		if _, duplicate := seen[clean]; duplicate {
			return Derived{}, fmt.Errorf("%w: change[%d] repeats path %q", ErrPlanInvalid, i, clean)
		}
		seen[clean] = struct{}{}
		allowed = append(allowed, clean)
		operations = append(operations, coderunner.Operation{Type: coderunner.ApplyPatch, Patch: change.Patch})
		if strings.HasSuffix(clean, ".go") {
			touchesGo = true
		}
	}
	sort.Strings(allowed)

	// gofmt only where Go actually changed, and only after every patch has
	// been applied, so formatting never runs against a half-applied tree.
	if touchesGo {
		operations = append(operations, coderunner.Operation{Type: coderunner.Gofmt})
	}
	// The gates are appended by the host, in this order, always. They are not
	// derived from anything the model said and cannot be reduced by it.
	operations = append(operations,
		coderunner.Operation{Type: coderunner.GoBuild},
		coderunner.Operation{Type: coderunner.GoVet},
		coderunner.Operation{Type: coderunner.GoTest},
	)

	policy := engineeringmission.MissionPolicy{
		TaskID:             request.TaskID,
		BaseSHA:            request.BaseSHA,
		Objective:          request.Objective,
		AllowedPaths:       allowed,
		AcceptanceCriteria: append([]string(nil), request.AcceptanceCriteria...),
		RequiredGates:      RequiredGates(),
	}
	normalized, err := policy.Normalize()
	if err != nil {
		return Derived{}, fmt.Errorf("%w: %v", ErrPlanInvalid, err)
	}

	plan := coderunner.Plan{SchemaVersion: coderunner.SchemaVersion, Operations: operations}
	// Round-trip the plan through CodeRunner's own parser before anything is
	// persisted. A plan that only the producer can read is how the previous
	// seam shipped a mission that could never execute.
	encoded, err := encodePlan(plan)
	if err != nil {
		return Derived{}, err
	}
	if _, err = coderunner.ParsePlan(encoded); err != nil {
		return Derived{}, fmt.Errorf("%w: generated plan rejected by CodeRunner: %v", ErrPlanInvalid, err)
	}
	return Derived{Policy: normalized, Plan: plan}, nil
}

// RequiredGates is the host's fixed gate set. It is a function rather than a
// variable so no caller can mutate the shared slice, and it is deliberately
// not parameterised: a mission that could choose to skip vet is a mission that
// will eventually skip vet.
func RequiredGates() []engineeringmission.RequiredGate {
	return []engineeringmission.RequiredGate{
		{Type: engineeringmission.GateBuild},
		{Type: engineeringmission.GateVet},
		{Type: engineeringmission.GateTest},
	}
}

// EncodePlan renders the CodeRunner plan exactly as it will be persisted.
func EncodePlan(plan coderunner.Plan) ([]byte, error) { return encodePlan(plan) }

func normalizePath(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%w: empty path", ErrPlanInvalid)
	}
	if strings.ContainsAny(trimmed, "\\\x00") {
		return "", fmt.Errorf("%w: %q", ErrPathForbidden, raw)
	}
	if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "~") {
		return "", fmt.Errorf("%w: absolute path %q", ErrPathForbidden, raw)
	}
	clean := path.Clean(trimmed)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: traversal in %q", ErrPathForbidden, raw)
	}
	return clean, nil
}

func denied(clean string) error {
	for _, exact := range forbiddenExact {
		if clean == exact {
			return fmt.Errorf("%w: %s", ErrPathForbidden, clean)
		}
	}
	for _, prefix := range forbiddenPrefixes {
		if strings.HasPrefix(clean+"/", prefix) || strings.HasPrefix(clean, prefix) {
			return fmt.Errorf("%w: %s", ErrPathForbidden, clean)
		}
	}
	// A dotfile at the repository root is configuration until proven
	// otherwise.
	if strings.HasPrefix(path.Base(clean), ".") {
		return fmt.Errorf("%w: dotfile %s", ErrPathForbidden, clean)
	}
	return nil
}

func withinScope(clean string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(clean, prefix) {
			return true
		}
	}
	return false
}
