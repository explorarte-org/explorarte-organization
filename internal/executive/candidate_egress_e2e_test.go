package executive

import (
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
	fixture.drive(t)

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
