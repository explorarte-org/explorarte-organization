package executiveadapter

import (
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/workflowruntime"
)

type captureLegacy struct{ request executive.SubmitRequest }

func (f *captureLegacy) Submit(_ context.Context, request executive.SubmitRequest) (executive.Run, bool, error) {
	f.request = request
	return executive.Run{RootTaskID: 42, CorrelationID: "executive:goal-42", State: executive.StateAccepted}, false, nil
}

func TestAdapterCarriesOwnerGoalThroughV2Seam(t *testing.T) {
	legacy := &captureLegacy{}
	adapter := Adapter{Orchestrator: legacy}
	result, err := adapter.Start(context.Background(), workflowruntime.GoalRequest{
		Actor: workflowruntime.Actor{OrganizationID: "explorarte", RoleID: executive.OwnerRoleID, ActorID: "owner"},
		Goal:  "deliver a verified result", AcceptanceCriteria: []string{"evidence exists"},
		Requirements:   []workflowruntime.RequirementSpec{{Key: "artifact", Type: "artifact", Description: "deliverable", Required: true}},
		IdempotencyKey: "goal-42",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RootTaskID != 42 || result.CorrelationID != "executive:goal-42" || result.LegacyState != string(executive.StateAccepted) {
		t.Fatalf("result=%+v", result)
	}
	if legacy.request.ActorRoleID != executive.OwnerRoleID || legacy.request.Goal.Goal != "deliver a verified result" || len(legacy.request.Goal.Requirements) != 1 {
		t.Fatalf("legacy request=%+v", legacy.request)
	}
}
