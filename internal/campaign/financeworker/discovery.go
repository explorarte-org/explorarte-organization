package financeworker

import (
	"context"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// financeDiscoveryPage bounds each round trip while paging through
// finance-reviewer-assigned candidate tasks, mirroring
// internal/executive/runtimeadapter.Tasks.ListExecutableRoots' own paging
// discipline: it does not bound correctness, only trades round-trip count
// against page size, so a structurally valid task can never become
// permanently invisible just because more recent candidates are filtered
// out ahead of it.
const financeDiscoveryPage = 64

// DiscoveryTaskSource is a real, index-backed campaign.financial_review
// discovery adapter over the canonical Task Engine. ReviewerRoleID must be
// resolved canonically (campaign.ReviewerRoleResolver, the same resolver
// FinanceService itself defaults to) by the caller wiring this up -- never
// a bare string literal written directly into this adapter, so discovery
// can never drift from whichever role the organization's own registry and
// capability grants actually name as the finance reviewer.
type DiscoveryTaskSource struct {
	Service        *tasks.Service
	OrganizationID string
	ReviewerRoleID string
}

// ListReadyFinanceTasks discovers campaign.financial_review tasks ready to
// claim, scoped to the resolved finance reviewer role. The (organization_id,
// assigned_role_id, status, ...) filter is backed by the tasks table's own
// tasks_assignee_status_idx index (the same one Executive's root discovery
// already relies on) -- never a table scan, never "fetch all tasks then
// filter in memory": TaskClass is the one predicate that index does not
// cover, so it is checked on the already org+role+status-narrowed page
// (typically tiny -- one financial review task per proposal), the exact
// same shape Executive's own isExecutiveRoot filter uses for its own
// extra predicates.
//
// A single "ready" status is the whole eligibility check: unlike an
// Executive root (which can sit blocked/awaiting-verification across an
// indefinite multi-turn lifetime), one financial review task is a single
// bounded claim-execute-finalize cycle -- it is either ready to claim or
// it is not this worker's concern (already running, already terminal).
func (d DiscoveryTaskSource) ListReadyFinanceTasks(ctx context.Context, limit int) ([]int64, error) {
	if limit <= 0 {
		limit = 16
	}
	ready := make([]int64, 0, limit)
	for offset := 0; ; offset += financeDiscoveryPage {
		values, err := d.Service.ListTasks(ctx, tasks.TaskFilter{
			OrganizationID: d.OrganizationID,
			Statuses:       []tasks.Status{tasks.StatusReady},
			AssignedRoleID: d.ReviewerRoleID,
			Limit:          financeDiscoveryPage,
			Offset:         offset,
		})
		if err != nil {
			return nil, err
		}
		for _, value := range values {
			if value.TaskClass != campaign.FinancialReviewTaskClass {
				continue
			}
			ready = append(ready, value.ID)
			if len(ready) == limit {
				return ready, nil
			}
		}
		if len(values) < financeDiscoveryPage {
			return ready, nil
		}
	}
}

var _ TaskSource = DiscoveryTaskSource{}
