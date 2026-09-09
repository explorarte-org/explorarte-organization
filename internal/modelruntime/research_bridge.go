package modelruntime

// =============================================================================
// Research bridge - deployment wiring for the Research model pool.
//
// FreeModelRouter (internal/search) depends only on the search.ModelInvoker
// seam; provider transport lives exclusively in the canonical runtime. The
// two packages cannot import each other, so this file provides the ONE
// sanctioned deployment mapping. The mapping carries NO provider-specific
// behavior. Provider and model come from the request as resolved by the
// host-side FreeModelRouter - never from LLM output. Typed errors, context
// cancellation, and deadlines pass through.
// =============================================================================

import (
	"context"
	"errors"
	"time"
)

// ResearchBridge adapts the canonical DispatchService to the model invoker
// seam consumed by FreeModelRouter/LLMQueryGenerator.
type ResearchBridge struct {
	dispatch *DispatchService
	Scope    ResearchOrgScope
	Clock    func() time.Time

	ModelProfileID        string
	ModelProfileVersionID int64
}

// ResearchOrgScope carries the host-owned identifiers required by the kernel
// request envelope. Zero values are rejected at the bridge boundary.
type ResearchOrgScope struct {
	OrganizationID         string
	OrganizationRevisionID int64
	TaskID                 int64
	AttemptID              int64
	DispatchActorRoleID    string
	SubjectRoleID          string
}

// ErrResearchBridgeScope is returned when the host scope is incomplete -
// a wiring bug, never a caller problem.
var ErrResearchBridgeScope = errors.New("modelruntime: research bridge org scope incomplete")

// NewResearchBridge validates the kernel dependency and host scope.
func NewResearchBridge(dispatch *DispatchService, scope ResearchOrgScope, profileID string, profileVersionID int64, clock func() time.Time) (*ResearchBridge, error) {
	if dispatch == nil {
		return nil, errors.New("modelruntime: nil dispatch service")
	}
	if scope.OrganizationID == "" || scope.OrganizationRevisionID == 0 ||
		scope.TaskID == 0 || scope.DispatchActorRoleID == "" || scope.SubjectRoleID == "" {
		return nil, ErrResearchBridgeScope
	}
	if profileID == "" || profileVersionID == 0 {
		return nil, errors.New("modelruntime: research bridge model profile incomplete")
	}
	if clock == nil {
		clock = time.Now
	}
	return &ResearchBridge{
		dispatch:              dispatch,
		Scope:                 scope,
		Clock:                 clock,
		ModelProfileID:        profileID,
		ModelProfileVersionID: profileVersionID,
	}, nil
}

// ResearchBridgeDispatch dispatches one research invocation through the
// canonical runtime. It is consumed by the deployment composition layer,
// which closes over it to produce the search.ModelInvoker seam.
func (b *ResearchBridge) Dispatch(ctx context.Context, invocationID int64) (DispatchResult, error) {
	return b.dispatch.Dispatch(ctx, invocationID)
}
