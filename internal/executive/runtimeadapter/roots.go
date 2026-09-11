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
			tasks.StatusBlocked,
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
		// A blocked root only goes back to ResumeDurable if the reason it
		// blocked is one ResumeDurable itself is willing to reconsider (see
		// IsAutonomouslyReconsiderableBlockedReason). Every other blocked
		// reason -- a human decision, missing evidence, an unresolved
		// design/department review, an ambiguity with no policy resolution
		// yet -- must stay excluded here exactly as it always has: this is
		// discovery, not a second authority that decides what "blocked"
		// means. ResumeDurable still runs its own guards on every
		// reconsidered root and can leave it, or put it back, blocked.
		if detail.Task.Status == tasks.StatusBlocked {
			if detail.Task.StatusReasonCode == nil || !executive.IsAutonomouslyReconsiderableBlockedReason(*detail.Task.StatusReasonCode) {
				continue
			}
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
