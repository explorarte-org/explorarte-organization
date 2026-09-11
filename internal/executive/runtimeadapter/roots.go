package runtimeadapter

import (
	"context"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// executiveRootDiscoveryPage bounds each round trip while paging through
// CEO-assigned candidate tasks -- see ListExecutableRoots. It intentionally
// does NOT bound correctness: unlike the old single-shot "fetch limit*4,
// then filter" query, ListExecutableRoots below keeps paging past however
// many candidates this page held until either it has enough roots or the
// candidate set itself is exhausted, so a structurally valid root can never
// become permanently invisible just because more recent CEO-assigned rows
// (any number of them) are discarded ahead of it by isExecutiveRoot or the
// blocked-reason check. The value only trades round-trip count against
// page size; either direction stays correct.
const executiveRootDiscoveryPage = 64

func (a Tasks) ListExecutableRoots(ctx context.Context, limit int) ([]int64, error) {
	if limit <= 0 {
		limit = 16
	}
	roots := make([]int64, 0, limit)
	for offset := 0; ; offset += executiveRootDiscoveryPage {
		// AssignedRoleID scopes this to CEO-assigned tasks only -- an
		// inherently small, always-indexed
		// (organization_id,assigned_role_id,status,...) subset of the whole
		// tasks table, never a table scan. Paging through all of it, worst
		// case, costs a handful of small round trips, not a full scan.
		values, err := a.Service.ListTasks(ctx, tasks.TaskFilter{
			OrganizationID: a.OrganizationID,
			Statuses: []tasks.Status{
				tasks.StatusReady,
				tasks.StatusPending,
				tasks.StatusAwaitingVerification,
				tasks.StatusBlocked,
			},
			AssignedRoleID: executive.CEORoleID,
			Limit:          executiveRootDiscoveryPage,
			Offset:         offset,
		})
		if err != nil {
			return nil, err
		}
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
				return roots, nil
			}
		}
		if len(values) < executiveRootDiscoveryPage {
			// Fewer rows than the page size means this page reached the end
			// of the candidate set -- nothing left to page through, whether
			// or not `limit` was reached.
			return roots, nil
		}
	}
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
