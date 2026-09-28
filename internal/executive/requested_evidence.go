package executive

import (
	"context"
	"regexp"
	"strings"
)

// A worker that could not see what its reasoning needed says so, and the host retrieves it for the
// next attempt at that work, instead of spending another round without it.
//
// External audit A5 (2026-09-27): in root 1848 two design workers in a row declined, honestly, to name a
// defect because the declarations, arguments and call sites it turned on were never shown; the round
// ended with nothing gained, because nothing carried what they missed to the next attempt. A worker
// result may now name what it needed (evidence_requests); a later worker of the same department --
// a replan's redo or the next design round -- is built with those ranges read verbatim and those
// identifiers searched first.

var requestedRangePattern = regexp.MustCompile(`^([A-Za-z0-9._/-]+\.go)#L(\d+)-L(\d+)$`)
var requestedIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?$`)

// requestedEvidenceFor returns the ranges (as repository citations at baseSHA) and identifiers that
// earlier workers of this worker's department asked for, in this design round and the one before.
func (o *Orchestrator) requestedEvidenceFor(ctx context.Context, root, task TaskRecord, purpose ExecutionPurpose, baseSHA string) (citations, subjects []string, err error) {
	if purpose != PurposeDepartmentWorker || baseSHA == "" || o.repositoryID == "" {
		return nil, nil, nil
	}
	all, err := o.tasks.ListByCorrelation(ctx, root.CorrelationID)
	if err != nil {
		return nil, nil, err
	}
	round := designRoundOf(task.IdempotencyKey)
	seen := map[string]bool{}
	for _, worker := range departmentWorkerTasks(all, root.ID, task.AssignedUnitID) {
		if worker.ID == task.ID || worker.ID > task.ID || worker.Status != "completed" {
			continue
		}
		if r := designRoundOf(worker.IdempotencyKey); r != round && r != round-1 {
			continue
		}
		result, ok := o.resultForCompletedTask(ctx, worker)
		if !ok {
			continue
		}
		parsed, parseErr := ParseWorkerResult(result.JSONOutput, o.limits)
		if parseErr != nil {
			continue
		}
		for _, request := range parsed.EvidenceRequests {
			request = strings.TrimSpace(request)
			if seen[request] || len(citations)+len(subjects) >= maxJudgedCitations {
				continue
			}
			seen[request] = true
			switch {
			case requestedRangePattern.MatchString(request):
				citations = append(citations, "repository://"+o.repositoryID+"@"+baseSHA+"/"+strings.TrimPrefix(request, "/"))
			case requestedIdentifierPattern.MatchString(request):
				subjects = append(subjects, request)
			}
		}
	}
	return citations, subjects, nil
}
