package executive

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The end-to-end guard the review demanded: source injected into a worker's
// summary must not reach the review task, and the host must refuse rather than
// quietly cleaning it.
//
// Refusing matters. Redacting would leave the reviewer judging sanitize(D)
// while the artifact digest asserts the designer produced D -- a fresh gap
// between what was decided and what was reviewed, which is the class of defect
// this subsystem exists to close.
func TestSourceCannotLeaveThroughTheCandidateDesign(t *testing.T) {
	const leaked = `func (o *Orchestrator) driveDepartments(ctx context.Context, root TaskRecord) (Run, bool, error) {
	revision, err := o.registry.CurrentRevision(ctx)
	if err != nil {
		return Run{}, false, err
	}
}`
	fixture := newMissionFixture(t, smokePath, false)
	// The designer was shown this excerpt...
	fixture.orchestrator.snapshotSources = stubSnapshotSources{sources: []SnapshotSource{
		{Kind: "repository_evidence", Reference: realCite, Version: targetSHA, Included: true, Content: leaked},
	}}
	// ...and copies it verbatim into its deliverable.
	fixture.harness.bodies[PurposeDepartmentWorker] = `{"schema_version":"worker-result/v1","summary":` +
		mustJSONString(leaked) + `,"evidence_refs":[]}`
	// The worker's own attempt is where the rule is now enforced first (see worker_declassify.go): it
	// is refused, retried, refused again, and its task dead-letters. That is a retryable contract
	// rejection at each attempt, so the driver tolerates it the way a worker loop does.
	for pass := 0; pass < 40; pass++ {
		run, err := fixture.orchestrator.Resume(context.Background(), fixture.root)
		if err != nil && !errors.Is(err, ErrRunBlocked) && !errors.Is(err, ErrModelResultContractRejected) {
			t.Fatalf("resume %d: %v", pass, err)
		}
		if run.State.Terminal() || run.State == StateBlocked {
			break
		}
	}

	// No review task may have been created carrying those bytes.
	for _, task := range fixture.tasks.tasks {
		if task.TaskClass != TaskClassCoordinationAdversarialReview {
			continue
		}
		if strings.Contains(task.Instructions, "CurrentRevision") {
			t.Fatal("organizational source reached the reviewer through the candidate design")
		}
	}
	// And the run must have stopped for that reason, not drifted past it.
	root := fixture.rootRecord(t)
	if root.Status != "blocked" {
		t.Fatalf("root=%q: a contaminated candidate must stop the run", root.Status)
	}
	// It stopped at the worker's attempt, where the worker could have corrected it -- with the
	// refusal recorded on the attempts, never carrying the copied text.
	refused := false
	for _, task := range fixture.tasks.tasks {
		for _, attempt := range task.Attempts {
			if strings.Contains(attempt.ResultSummary, "reproduces") {
				refused = true
				if strings.Contains(attempt.ResultSummary, "CurrentRevision") {
					t.Fatalf("the refusal carries the copied source: %s", attempt.ResultSummary)
				}
			}
		}
	}
	if !refused {
		t.Fatal("no attempt recorded the refusal; the run stopped for some other reason")
	}
}

func mustJSONString(value string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\n':
			out.WriteString(`\n`)
		case '\t':
			out.WriteString(`\t`)
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return out.String()
}

var _ = errors.Is
