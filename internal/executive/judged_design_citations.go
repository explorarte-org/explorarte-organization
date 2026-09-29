package executive

import (
	"context"
	"sort"
	"strings"
)

// maxJudgedCitations bounds the citations handed to one judge's context build; the evidence
// provider bounds what it reads again (half its range budget).
const maxJudgedCitations = 16

// judgedDesignCitations returns the repository citations of the deliverables a department review
// or a design adjudication judges: the round's workers that still hold authority
// (departmentReviewScope), for the reviewed department or, for an adjudication, every department
// that worked in the round.
//
// Local smoke #34 (root 1773, 2026-09-27): the designer stood on mission_phase.go lines 131-179 and
// 325-373 and ports.go 64-112; the adjudicator's own searches issued it lines 1-48 of both files,
// and it sent the design back for claims about "unseen ranges" until the rounds ran out. The judge
// is now shown the ranges the design cites, next to whatever its own exploration finds.
func (o *Orchestrator) judgedDesignCitations(ctx context.Context, root, task TaskRecord, purpose ExecutionPurpose) ([]string, error) {
	if purpose != PurposeDepartmentReview && purpose != PurposeDesignAdjudication {
		return nil, nil
	}
	all, err := o.tasks.ListByCorrelation(ctx, root.CorrelationID)
	if err != nil {
		return nil, err
	}
	round := designRoundOf(task.IdempotencyKey)
	units := []string{task.AssignedUnitID}
	if purpose == PurposeDesignAdjudication {
		round = o.activeDesignRound(ctx, all, root.ID)
		units = unitsThatWorkedInRound(all, round)
	}
	seen := map[string]bool{}
	citations := []string{}
	for _, unit := range units {
		_, reviewed, scopeErr := o.departmentReviewScope(ctx, all, root.ID, unit, round)
		if scopeErr != nil {
			return nil, scopeErr
		}
		for _, worker := range reviewed {
			if worker.Status != "completed" {
				continue
			}
			result, ok := o.resultForCompletedTask(ctx, worker)
			if !ok {
				continue
			}
			for _, citation := range citationPattern.FindAllString(string(result.JSONOutput), -1) {
				if !seen[citation] && len(citations) < maxJudgedCitations {
					seen[citation] = true
					citations = append(citations, citation)
				}
			}
		}
	}
	return citations, nil
}

// unitsThatWorkedInRound lists, in order, the departments with a worker task in round.
func unitsThatWorkedInRound(all []TaskRecord, round int) []string {
	seen := map[string]bool{}
	for _, task := range all {
		if task.AssignedUnitID == "" || designRoundOf(task.IdempotencyKey) != round || !isWorkerKey(task.IdempotencyKey) {
			continue
		}
		seen[task.AssignedUnitID] = true
	}
	units := make([]string, 0, len(seen))
	for unit := range seen {
		units = append(units, unit)
	}
	sort.Strings(units)
	return units
}

func isWorkerKey(key string) bool { return strings.Contains(key, ":worker:") }
