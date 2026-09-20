package executive

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
)

// ExecutionMode is the host-owned decision of HOW a campaign is allowed to
// work. It is a typed field on SubmitRequest, decided by a caller that already
// holds owner authority, and it is the ONLY way a campaign is opted into the
// governed implementation path.
//
// What it is deliberately not: text in the goal, an entry in
// OwnerGoal.Requirements, a field of a campaign proposal, something the CEO
// says, or something a model infers. A model that writes "design-freeze" or
// "code_runner_execution_evidence" in a prompt has produced a string, and a
// string activates nothing: the requirement keys below are read by the
// orchestrator from durable root requirements that only this translation (or
// a direct owner Submit) writes.
type ExecutionMode string

const (
	// ExecutionModeAnalysisOnly is today's behavior and the meaning of the
	// zero value: the campaign plans, delegates, reviews and closes, and no
	// governed phase (design freeze, mission, code execution) runs.
	ExecutionModeAnalysisOnly ExecutionMode = "analysis_only"
	// ExecutionModeGovernedImplementation runs the design freeze, provisions
	// a governed engineering mission and refuses closure until real
	// code-runner execution evidence exists.
	ExecutionModeGovernedImplementation ExecutionMode = "governed_implementation"
)

// ErrInvalidExecutionMode reports a mode that is neither of the known values.
// It is wrapped with ErrInvalidInput so existing input handling still applies.
var ErrInvalidExecutionMode = errors.New("invalid execution mode")

// ParseExecutionMode is the single decoder for a mode arriving as text (a CLI
// flag). The empty string is analysis_only: absence never opts in.
func ParseExecutionMode(raw string) (ExecutionMode, error) {
	switch mode := ExecutionMode(strings.TrimSpace(raw)); mode {
	case "", ExecutionModeAnalysisOnly:
		return ExecutionModeAnalysisOnly, nil
	case ExecutionModeGovernedImplementation:
		return mode, nil
	default:
		return "", fmt.Errorf("%w: %w: %q", ErrInvalidInput, ErrInvalidExecutionMode, raw)
	}
}

// Normalized maps the zero value to analysis_only.
func (m ExecutionMode) Normalized() ExecutionMode {
	if m == "" {
		return ExecutionModeAnalysisOnly
	}
	return m
}

// Governed reports whether the mode opts into the governed implementation path.
func (m ExecutionMode) Governed() bool { return m == ExecutionModeGovernedImplementation }

// ExecutionModeRequirements is the canonical requirement bundle the host writes
// on the root for a mode. It is the only translation from mode to requirement
// keys; callers name a mode, never a key, so the keys are not an authority API.
//
// The bundle includes the internal-code mission scope. Its absence is the
// documentation-only scope (docs/implementation/), under which a plan that asks to
// change Go code is rejected at mission derivation -- so a mode called
// governed_implementation without it could never implement anything. The reach
// stays bounded: the mission is limited to the paths the implementation plan and
// the policy derive (and never to the protected governance data), not to all of
// internal/ and cmd/. A governed pipeline that may only produce documentation
// would be a different mode, not a missing key.
func ExecutionModeRequirements(mode ExecutionMode) ([]RequirementProposal, error) {
	switch mode.Normalized() {
	case ExecutionModeAnalysisOnly:
		return nil, nil
	case ExecutionModeGovernedImplementation:
		return []RequirementProposal{
			{Key: designfreeze.RequirementKey, Type: "result", Description: "Design frozen by executive adjudication", Required: true},
			{Key: MissionRequirementKey, Type: "result", Description: "Governed engineering mission provisioned", Required: true},
			{Key: InternalCodeScopeRequirementKey, Type: "condition", Description: "Owner permits bounded internal code scope for the governed mission", Required: false},
			{Key: CodeRunnerExecutionEvidenceRequirementKey, Type: "result", Description: "Real code-runner execution evidence with all host gates", Required: true},
		}, nil
	default:
		return nil, fmt.Errorf("%w: %w: %q", ErrInvalidInput, ErrInvalidExecutionMode, string(mode))
	}
}

// executionModeKeys is every requirement key a mode writes, for conflict checks.
func executionModeKeys() map[string]struct{} {
	bundle, _ := ExecutionModeRequirements(ExecutionModeGovernedImplementation)
	keys := make(map[string]struct{}, len(bundle))
	for _, requirement := range bundle {
		keys[requirement.Key] = struct{}{}
	}
	return keys
}
