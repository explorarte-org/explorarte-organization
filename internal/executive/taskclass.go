package executive

import "regexp"

// Host-assigned TaskClass values (M1.3 section 6): deterministic, stable,
// tested, assigned by the Executive orchestrator itself at task-creation
// time -- never derived from a task's title later, and never something a
// model chooses for these specific coordination steps.
const (
	TaskClassOwnerGoal              = "owner.goal"
	TaskClassCoordinationCEOPlan    = "coordination.ceo_plan"
	TaskClassCoordinationDeptPlan   = "coordination.department_plan"
	TaskClassCoordinationDeptReview = "coordination.department_review"
	TaskClassCoordinationCEOClosure = "coordination.ceo_closure"
	// TaskClassGeneralWork is the safe default a worker task proposal
	// receives when its Leader did not propose a TaskClass (M1.3 section
	// 4). Mirrors internal/tasks.TaskClassGeneralWork by value -- these
	// two packages deliberately do not import each other (see
	// runtimeadapter, which is the only bridge between them), the same
	// pattern already used for ProviderRenderTelemetry-style plain DTOs
	// elsewhere in this codebase.
	TaskClassGeneralWork = "general.work"
	// TaskClassLegacyUnspecified mirrors internal/tasks.
	// TaskClassLegacyUnspecified: EXCLUSIVELY the one-time historical
	// migration marker (M1.3 section 3). Never a value a Leader may
	// propose for a new task -- ValidTaskClass rejects it explicitly
	// below, the same way tasks.ValidateCreateRequest does at the host
	// boundary.
	TaskClassLegacyUnspecified = "legacy.unspecified"
)

// taskClassPattern is the identical syntax contract internal/tasks.
// ValidTaskClass enforces -- see that function's doc comment. Kept as an
// independent definition, not an import, for the same reason
// TaskClassGeneralWork above is duplicated rather than imported.
var taskClassPattern = regexp.MustCompile(`^[a-z0-9]+(?:_[a-z0-9]+)*(?:\.[a-z0-9]+(?:_[a-z0-9]+)*)+$`)

const maxTaskClassBytes = 100

// ValidTaskClass reports whether s is syntactically a valid, host-
// acceptable TaskClass. This is the HOST validation a Leader-proposed
// WorkerTaskProposal.TaskClass must pass before it is ever forwarded into
// a CreateTaskCommand -- a model naming a class does not make it so.
func ValidTaskClass(s string) bool {
	if s == TaskClassLegacyUnspecified {
		return false
	}
	return len(s) > 0 && len(s) <= maxTaskClassBytes && taskClassPattern.MatchString(s)
}
