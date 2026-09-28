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
			ranges, identifiers := parseEvidenceRequest(request)
			for _, rangeRef := range ranges {
				citations = append(citations, "repository://"+o.repositoryID+"@"+baseSHA+"/"+rangeRef)
			}
			subjects = append(subjects, identifiers...)
		}
	}
	return citations, subjects, nil
}

var requestedRangeInProse = regexp.MustCompile(`\b([A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+)*\.go)#L(\d+)-L(\d+)\b`)

// compoundIdentifier is a Go identifier with an interior capital -- governedTaskAttempts,
// InvalidateProofs, DesignAdjudication.Verdict -- which prose around it does not produce.
var compoundIdentifier = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*[a-z0-9][A-Z][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?\b`)

// parseEvidenceRequest reads one evidence request: a bare path#Lstart-Lend or Go identifier, or prose
// that names them. Local smoke #38 (root 1916, 2026-09-28): the worker wrote its requests as sentences
// ("Go identifier: governedTaskAttempts -- its declaration, to confirm ..."), none matched the bare
// forms, and the adjudication then asked the next round to settle exactly those from "the requested
// evidence". Paths with a range and compound identifiers are taken from the prose.
func parseEvidenceRequest(request string) (ranges, identifiers []string) {
	request = strings.TrimSpace(request)
	if requestedRangePattern.MatchString(request) {
		return []string{strings.TrimPrefix(request, "/")}, nil
	}
	if requestedIdentifierPattern.MatchString(request) {
		return nil, []string{request}
	}
	for _, match := range requestedRangeInProse.FindAllString(request, -1) {
		ranges = append(ranges, strings.TrimPrefix(match, "/"))
	}
	seen := map[string]bool{}
	for _, match := range compoundIdentifier.FindAllString(request, -1) {
		if !seen[match] && !strings.Contains(match, "/") {
			seen[match] = true
			identifiers = append(identifiers, match)
		}
	}
	return ranges, identifiers
}
