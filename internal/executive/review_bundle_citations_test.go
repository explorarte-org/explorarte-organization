package executive

import (
	"encoding/json"
	"strings"
	"testing"
)

// P12: the reviewer receives verified citation REFERENCES and never the source
// behind them.
//
// Its context admits public and sanitized data while repository evidence is
// organizational. Widening that so it could read code would be an egress
// decision taken as a side effect of a convenience, so the bundle carries the
// set of claims it may treat as grounded and nothing else.
// P12: the reviewer receives verified citation REFERENCES, bound to the
// deliverable entitled to use them, and never the source behind them.
//
// The first version of this test asserted only that three code-looking strings
// were absent and two instruction sentences present. It passed against a
// fixture with no snapshot reader at all -- that is, against exactly the
// system that in production would never authorize a single citation. Absence
// of source is not evidence of authorization.
func TestTheReviewBundleCarriesVerifiedCitationsPerDeliverable(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)

	// The designer saw one excerpt; the host must be able to confirm it.
	const cited = "repository://explorarte-organization@" + targetSHA + "/internal/executive/validator.go#L52-L92"
	fixture.orchestrator.snapshotSources = stubSnapshotSources{sources: []SnapshotSource{
		{Kind: "repository_evidence", Reference: cited, Version: targetSHA, Included: true},
	}}
	fixture.harness.bodies[PurposeDepartmentWorker] = `{"schema_version":"worker-result/v1",` +
		`"summary":"The validator already refuses this, see ` + cited + ` .","evidence_refs":[]}`
	fixture.drive(t)

	var bundle string
	for _, task := range fixture.tasks.tasks {
		if task.TaskClass == TaskClassCoordinationAdversarialReview {
			bundle = task.Instructions
		}
	}
	if bundle == "" {
		t.Fatal("no adversarial review ran, so this proves nothing")
	}

	var decoded struct {
		Deliverables []struct {
			TaskID                 int64    `json:"task_id"`
			InvocationID           int64    `json:"invocation_id"`
			VerifiedRepositoryRefs []string `json:"verified_repository_refs"`
		} `json:"deliverables"`
	}
	body := bundle[strings.Index(bundle, "{"):]
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("the bundle must be readable as the contract it declares: %v", err)
	}
	if len(decoded.Deliverables) == 0 {
		t.Fatal("the bundle authorizes nothing per deliverable: the circuit never closed")
	}
	authorized := 0
	for _, deliverable := range decoded.Deliverables {
		if deliverable.TaskID == 0 || deliverable.InvocationID == 0 {
			t.Fatalf("an authorization with no owner is a laundered one: %+v", deliverable)
		}
		for _, reference := range deliverable.VerifiedRepositoryRefs {
			if reference != cited {
				t.Fatalf("an unverified reference was authorized: %q", reference)
			}
			authorized++
		}
	}
	if authorized == 0 {
		t.Fatal("the designer cited evidence it was given and the bundle authorized none of it")
	}

	// And still no source: references only.
	for _, forbidden := range []string{"package executive", "func (o *Orchestrator)"} {
		if strings.Contains(bundle, forbidden) {
			t.Fatalf("the review bundle carries repository source: %q", forbidden)
		}
	}
	for _, needed := range []string{"unverifiable_repository_claim", "deliverables[].verified_repository_refs"} {
		if !strings.Contains(bundle, needed) {
			t.Fatalf("the reviewer is not told the rule: missing %q", needed)
		}
	}
}

// P13: the contract the reviewer answers under has a place to put that
// finding, and the host will accept it.
func TestTheReviewerCanReportAnUngroundedRepositoryClaim(t *testing.T) {
	body := `{"schema_version":"adversarial-review/v1","verdict":"revise",` +
		`"findings":[{"id":"AR-001","kind":"unverifiable_repository_claim","severity":"high",` +
		`"claim":"The design asserts driveDepartments has two responsibilities with no citation.",` +
		`"affected_requirement":"design names exact files",` +
		`"required_correction":"Cite an authorized repository:// reference or drop the claim.",` +
		`"evidence_refs":[]}],` +
		`"contradictions":[],"unverified_assumptions":[],"security_findings":[],` +
		`"authority_findings":[],"recovery_findings":[],"memory_epistemic_findings":[],"evidence_refs":[]}`

	review, err := ParseAdversarialReview([]byte(body), DefaultLimits())
	if err != nil {
		t.Fatalf("the reviewer must be able to report an ungrounded claim: %v", err)
	}
	if len(review.Findings) != 1 || review.Findings[0].Kind != FindingUnverifiableRepositoryClaim {
		t.Fatalf("finding kind was not carried: %+v", review.Findings)
	}

	// And the schema the provider enforces actually offers that value: a
	// contract the host accepts but the provider never allows would produce
	// a finding no model could emit.
	var schema map[string]any
	if err := json.Unmarshal(adversarialReviewOutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	properties := schema["properties"].(map[string]any)
	findings := properties["findings"].(map[string]any)
	items := findings["items"].(map[string]any)
	itemProps := items["properties"].(map[string]any)
	kind, ok := itemProps["kind"].(map[string]any)
	if !ok {
		t.Fatal("the schema offers no place for a finding kind")
	}
	values, _ := kind["enum"].([]any)
	found := false
	for _, value := range values {
		if value == "unverifiable_repository_claim" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the schema does not offer unverifiable_repository_claim: %v", values)
	}
	// Strict structured outputs require every property to be required.
	required, _ := items["required"].([]any)
	hasKind := false
	for _, value := range required {
		if value == "kind" {
			hasKind = true
		}
	}
	if !hasKind {
		t.Fatal("kind is a property the provider will refuse unless it is also required")
	}
}
