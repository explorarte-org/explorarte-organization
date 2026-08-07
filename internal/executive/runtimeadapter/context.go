package runtimeadapter

import (
	"context"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

type Context struct {
	Service        contextengine.Service
	OrganizationID string
}

func (a Context) Build(ctx context.Context, request executive.ContextRequest) (int64, error) {
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
		return 0, err
	}
	return result.Snapshot.ID, nil
}

var _ executive.ContextCoordinator = Context{}
