package executive

import (
	"context"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
)

// Production root 906 (2026-09-20), kept as a permanent fixture. A design-phase
// department worker -- a model that can only return text -- reported that it had
// created two files and that `go test` had exited 0. Nothing had been created or
// run. The independent adversarial reviewer caught the claim, the adjudicator
// sent the design back, and the design never froze.
//
// This is the property the governed path exists for: a claim of execution with
// no executable evidence must not become a frozen design, a mission, or a
// completed root. The scripted models below reproduce that run: the worker
// fabricates, the reviewer rejects it, the adjudicator revises.
func TestAWorkersFabricatedExecutionClaimNeverFreezesADesign(t *testing.T) {
	fixture := newFreezeFixture(t, "revise", true)
	const fabricated = "Created internal/governedsmoke/sum.go and sum_test.go; ran go test ./internal/governedsmoke/... which exited with code 0."
	fixture.harness.bodies[PurposeDepartmentWorker] = `{"schema_version":"worker-result/v1","summary":"` + fabricated + `","evidence_refs":[]}`
	fixture.harness.bodies[PurposeAdversarialReview] = `{"schema_version":"adversarial-review/v1","verdict":"revise",` +
		`"findings":[{"id":"AR-001","severity":"high","claim":"The deliverable asserts files were created and go test exited 0 with no execution evidence.",` +
		`"affected_requirement":"design before implementation","required_correction":"Retract the claim or support it with host-authorized execution evidence.",` +
		`"evidence_refs":[]}],"contradictions":[],"unverified_assumptions":[],"security_findings":[],` +
		`"authority_findings":[],"recovery_findings":[],"memory_epistemic_findings":[],"evidence_refs":[]}`

	run := fixture.drive(t)

	// The reviewer was shown the claim it rejected.
	all, err := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	review, ok := findTaskByKey(all, childKey(fixture.root, "design-review:round:1"))
	if !ok || review.Status != "completed" {
		t.Fatal("the independent adversarial review never ran")
	}
	if !strings.Contains(review.Instructions, "exited with code 0") {
		t.Fatal("the adversarial reviewer was not shown the worker's claim, so its rejection proves nothing")
	}

	// The design did not freeze, the run did not complete, and no worker's claim
	// became the executable evidence the root requires.
	root := fixture.rootRecord(t)
	if status := requirementStatus(root, designfreeze.RequirementKey); status == "satisfied" {
		t.Fatal("a design resting on a fabricated execution claim was frozen")
	}
	if run.State == StateCompleted {
		t.Fatal("a root completed on a fabricated execution claim")
	}
	if status := requirementStatus(root, CodeRunnerExecutionEvidenceRequirementKey); status == "satisfied" {
		t.Fatal("a model's claim satisfied the code-runner execution evidence requirement")
	}
}
