package runtimeadapter

import (
	"context"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

func (a Tasks) ListExecutableRoots(ctx context.Context, limit int) ([]int64, error) {
	if limit <= 0 {
		limit = 16
	}
	values, err := a.Service.ListTasks(ctx, tasks.TaskFilter{
		OrganizationID: a.OrganizationID,
		Statuses: []tasks.Status{
			tasks.StatusReady,
			tasks.StatusPending,
			tasks.StatusAwaitingVerification,
		},
		AssignedRoleID: executive.CEORoleID,
		Limit:          limit * 4,
	})
	if err != nil {
		return nil, err
	}
	roots := make([]int64, 0, limit)
	for _, value := range values {
		detail, getErr := a.Service.GetTask(ctx, value.ID)
		if getErr != nil {
			return nil, getErr
		}
		if !isExecutiveRoot(detail) {
			continue
		}
		roots = append(roots, value.ID)
		if len(roots) == limit {
			break
		}
	}
	return roots, nil
}

func isExecutiveRoot(detail tasks.TaskDetail) bool {
	if detail.Task.AssignedRoleID != executive.CEORoleID || detail.Task.RequestedByRoleID == nil || *detail.Task.RequestedByRoleID != executive.OwnerRoleID {
		return false
	}
	for _, requirement := range detail.Requirements {
		if requirement.Required && requirement.Key == "executive_closure_verified" && requirement.Type == tasks.RequirementResult {
			return true
		}
	}
	return false
}

var _ executive.RootSource = Tasks{}
