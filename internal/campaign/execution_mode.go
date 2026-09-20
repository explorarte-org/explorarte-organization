package campaign

import (
	"errors"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// ExecutionMode is the host-owned decision of how an approved campaign may
// work. It is Executive's type, re-exported so Campaign callers do not import
// the requirement vocabulary behind it.
type ExecutionMode = executive.ExecutionMode

const (
	ExecutionModeAnalysisOnly           = executive.ExecutionModeAnalysisOnly
	ExecutionModeGovernedImplementation = executive.ExecutionModeGovernedImplementation
)

var (
	// ErrExecutionModeNotAuthorized is returned when a mode is requested
	// without a grant issued by the owner promoter for THIS approval and actor.
	ErrExecutionModeNotAuthorized = errors.New("execution mode is not authorized for this promotion")

	// ErrExecutionModeConflict is returned when an approval was already
	// promoted under a different mode than the one now requested. The durable
	// promotion is immutable: the mode an owner approved once is the mode the
	// campaign runs under, across retries, replans and re-invocations.
	ErrExecutionModeConflict = errors.New("approval was already promoted under a different execution mode")
)

// ExecutionModeGrant is the proof that the owner promoter -- and only it --
// chose an execution mode for one specific approval.
//
// Its fields are unexported, so no other package can construct a non-zero
// grant: not the CEO tool, not a model-produced argument, not a CLI flag
// handler. The zero value means "no mode was chosen", which promotes as
// analysis_only and never conflicts with an existing promotion. That is what
// makes the mode a host-owned decision instead of a parameter anyone can pass.
type ExecutionModeGrant struct {
	mode        ExecutionMode
	approvalID  int64
	ownerRoleID string
}

// grantExecutionMode is called only by OwnerPromoter, after it has resolved the
// canonical owner and checked the promotion capability.
func grantExecutionMode(mode ExecutionMode, approvalID int64, ownerRoleID string) (ExecutionModeGrant, error) {
	if _, err := executive.ExecutionModeRequirements(mode); err != nil {
		return ExecutionModeGrant{}, err
	}
	if approvalID <= 0 || ownerRoleID == "" {
		return ExecutionModeGrant{}, fmt.Errorf("%w: a grant names an approval and an owner", ErrInvalidInput)
	}
	return ExecutionModeGrant{mode: mode.Normalized(), approvalID: approvalID, ownerRoleID: ownerRoleID}, nil
}

// Chosen reports whether a mode was explicitly chosen (any non-zero grant).
func (g ExecutionModeGrant) Chosen() bool { return g.approvalID != 0 }

// Mode is the granted mode; the zero grant is analysis_only.
func (g ExecutionModeGrant) Mode() ExecutionMode { return g.mode.Normalized() }

// authorizes reports whether the grant was issued for exactly this promotion.
func (g ExecutionModeGrant) authorizes(params PromoteToExecutiveParams) bool {
	return g.Chosen() && g.approvalID == params.OwnerApprovalID && g.ownerRoleID == params.PromotedByRoleID
}
