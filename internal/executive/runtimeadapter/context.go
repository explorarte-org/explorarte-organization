package runtimeadapter

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

type Context struct {
	Service        contextengine.Service
	OrganizationID string
}

// Build returns the snapshot together with its render. The render is not a
// convenience: Model Runtime requires an invocation's stable prefix to be the
// byte-exact rendered context of the snapshot it references, so a caller that
// only knew the ID could not construct a valid model input at all.
func (a Context) Build(ctx context.Context, request executive.ContextRequest) (executive.ContextSnapshot, error) {
	result, err := a.Service.Build(ctx, contextengine.BuildRequest{
		OrganizationID:         a.OrganizationID,
		OrganizationRevisionID: request.OrganizationRevisionID,
		ActorRoleID:            request.ActorRoleID,
		Purpose:                request.Purpose,
		TaskRef:                request.TaskRef,
		IdempotencyKey:         request.IdempotencyKey,
		CorrelationID:          request.CorrelationID,
		CausationID:            request.CausationID,
	})
	if err != nil {
		return executive.ContextSnapshot{}, err
	}
	rendered, err := a.Service.Render(ctx, result.Snapshot.ID)
	if err != nil {
		return executive.ContextSnapshot{}, fmt.Errorf("render context snapshot %d: %w", result.Snapshot.ID, err)
	}
	// RenderedHash is the snapshot's own durable digest of these bytes. It is
	// passed through rather than recomputed so a render that ever drifted from
	// its persisted hash fails the Harness identity check instead of being
	// silently re-blessed here.
	return executive.ContextSnapshot{
		ID:      result.Snapshot.ID,
		Version: strconv.FormatInt(result.Snapshot.Version, 10),
		Digest:  result.Snapshot.RenderedHash,
		Content: string(rendered),
	}, nil
}

var _ executive.ContextCoordinator = Context{}
