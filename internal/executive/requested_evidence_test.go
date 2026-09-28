package executive

import (
	"slices"
	"strings"
	"testing"
)

// External audit A5: a worker that could not see what it needed names it (evidence_requests), and the
// redo of that work is built with those ranges read verbatim and those identifiers searched first.
func TestARedoIsShownWhatThePreviousWorkerSaidItNeeded(t *testing.T) {
	fixture := eFixture(t)
	fixture.eOwnership = ownerEntry("RC:1:1", eOwnerKey) + "," + ownerEntry("RC:1:2", eOwnerKey)
	fixture.eReviewVerdict = "needs_replan"
	fixture.eOutcomes = `[` +
		`{"required_change_id":"RC:1:1","status":"conflicted","canonical_resolution":"","conflicting_task_refs":["task:a","task:b"]},` +
		`{"required_change_id":"RC:1:2","status":"resolved","canonical_resolution":"cited","conflicting_task_refs":[]}]`
	fixture.eFollowups = `[{"client_key":"reconcile_mdr","assigned_role_id":"ingenieria_ia/qa","task_class":"engineering.review",` +
		`"title":"Reconcile MDR granularity","instructions":"One falsifiable claim.","acceptance_criteria":["Cite"],"dependencies":[]}]`
	fixture.eFollowupOwnership = "[" + ownerEntry("RC:1:1", "reconcile_mdr") + "," + ownerEntry("RC:1:2", "reconcile_mdr") + "]"
	baseReview := fixture.harness.departmentReviewBody
	fixture.harness.departmentReviewBody = func(task TaskRecord) string {
		if strings.Contains(task.IdempotencyKey, ":replan:") {
			return eReplanReviewBody([]string{"RC:1:1", "RC:1:2"})
		}
		return baseReview(task)
	}
	baseWorker := fixture.harness.departmentWorkerBody
	fixture.harness.departmentWorkerBody = func(task TaskRecord) string {
		body := ""
		if baseWorker != nil {
			body = baseWorker(task)
		}
		if body == "" {
			body = fixture.harness.bodies[PurposeDepartmentWorker]
		}
		if designRoundOf(task.IdempotencyKey) == 2 && strings.HasSuffix(task.IdempotencyKey, ":"+eOwnerKey) {
			body = strings.Replace(body, `{"schema_version":"worker-result/v2",`,
				`{"schema_version":"worker-result/v2","evidence_requests":["internal/executive/driver/driver.go#L160-L240","ListExecutableRoots","not a request"],`, 1)
		}
		return body
	}

	driveCapability(t, fixture, 40)

	root := fixture.rootRecord(t)
	all, err := fixture.tasks.ListByCorrelation(t.Context(), root.CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	redo, found := TaskRecord{}, false
	for _, worker := range roundWorkers(all, root.ID, "ingenieria_ia", 2) {
		if strings.Contains(worker.IdempotencyKey, ":reconcile_mdr") {
			redo, found = worker, true
		}
	}
	if !found {
		t.Fatal("scenario: the redo never ran")
	}
	fixture.harness.mu.Lock()
	commands := append([]HarnessRunCommand(nil), fixture.harness.commands...)
	fixture.harness.mu.Unlock()
	for _, command := range commands {
		if command.TaskID != redo.ID {
			continue
		}
		request := fixture.harness.contexts.requests[command.Context.ID]
		wantRange := "repository://explorarte-organization@" + targetSHA + "/internal/executive/driver/driver.go#L160-L240"
		if !slices.Contains(request.RepositoryCitations, wantRange) {
			t.Fatalf("the redo was not shown the requested range %s: %v", wantRange, request.RepositoryCitations)
		}
		if !slices.Contains(request.RepositorySubjects, "ListExecutableRoots") || slices.Contains(request.RepositorySubjects, "not a request") {
			t.Fatalf("the redo's subjects %v: want ListExecutableRoots and nothing unrecognisable", request.RepositorySubjects)
		}
		return
	}
	t.Fatal("the redo made no model call")
}

func TestEvidenceRequestsAreBounded(t *testing.T) {
	requests := make([]string, MaxEvidenceRequests+1)
	for i := range requests {
		requests[i] = "Symbol"
	}
	body := `{"schema_version":"worker-result/v1","summary":"s","evidence_refs":[],"evidence_requests":["` + strings.Join(requests, `","`) + `"]}`
	if _, err := ParseWorkerResult([]byte(body), DefaultLimits()); err == nil {
		t.Fatalf("%d evidence requests were accepted, at most %d", len(requests), MaxEvidenceRequests)
	}
}
