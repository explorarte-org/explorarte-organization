package postrun

import "github.com/Mireuz13/explorarte-organization/internal/memory"

// Kind classifies what ProcessRun actually did, so a caller (an operator
// running the CLI, or a future batch driver) can tell "nothing was wrong"
// apart from "this role can never propose here" apart from "a candidate now
// exists."
type Kind string

const (
	// KindProposed: a new memory candidate was created.
	KindProposed Kind = "proposed"
	// KindReused: the same run was already processed — ProcessRun is
	// idempotent, so this is a no-op, not an error.
	KindReused Kind = "reused"
	// KindSkippedPass: completion re-verified as a clean pass. There is no
	// problem to record — memory.Entry requires an honest Problem, and a
	// pass has none.
	KindSkippedPass Kind = "skipped_pass"
	// KindSkippedRoleNotEligible: the task's assigned role does not hold
	// memory.propose under docs/canonical/capability-matrix.yaml (e.g. the
	// CEO's own closure attempts). This is the capability matrix working as
	// designed, not a failure.
	KindSkippedRoleNotEligible Kind = "skipped_role_not_eligible"
)

type Outcome struct {
	Kind  Kind
	Entry *memory.Entry `json:",omitempty"` // nil unless Kind == KindProposed or KindReused
}
