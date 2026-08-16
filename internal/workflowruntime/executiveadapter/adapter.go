// Package executiveadapter keeps the V1 executive orchestrator available as a
// migration adapter behind the V2 workflow seam.
package executiveadapter

import (
	"context"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/workflowruntime"
)

type LegacyOrchestrator interface {
	Submit(context.Context, executive.SubmitRequest) (executive.Run, bool, error)
}

type Adapter struct{ Orchestrator LegacyOrchestrator }

func (a Adapter) Start(ctx context.Context, request workflowruntime.GoalRequest) (workflowruntime.ExecutiveStart, error) {
	requirements := make([]executive.RequirementProposal, 0, len(request.Requirements))
	for _, requirement := range request.Requirements {
		requirements = append(requirements, executive.RequirementProposal{
			Key: requirement.Key, Type: requirement.Type, Description: requirement.Description, Required: requirement.Required,
		})
	}
	run, reused, err := a.Orchestrator.Submit(ctx, executive.SubmitRequest{
		Goal:        executive.OwnerGoal{Goal: request.Goal, AcceptanceCriteria: append([]string(nil), request.AcceptanceCriteria...), Requirements: requirements},
		ActorRoleID: request.Actor.RoleID, IdempotencyKey: request.IdempotencyKey,
	})
	if err != nil {
		return workflowruntime.ExecutiveStart{}, err
	}
	return workflowruntime.ExecutiveStart{
		RootTaskID: run.RootTaskID, CorrelationID: run.CorrelationID, LegacyState: string(run.State), Reused: reused,
	}, nil
}

var _ workflowruntime.ExecutivePort = Adapter{}
