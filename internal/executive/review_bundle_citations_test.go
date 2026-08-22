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
func TestTheReviewBundleCarriesReferencesAndNoSource(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)
	fixture.drive(t)

	// The bundle travels as the reviewer task's instructions, which is where
	// it is durable and auditable -- not inside the context snapshot.
	var bundles []string
	for _, task := range fixture.tasks.tasks {
		if task.TaskClass == TaskClassCoordinationAdversarialReview {
			bundles = append(bundles, task.Instructions)
		}
	}
	if len(bundles) == 0 {
		t.Fatal("no adversarial review ran, so this proves nothing")
	}
	for _, bundle := range bundles {
		// Nothing that looks like source may reach the reviewer.
		for _, forbidden := range []string{"package executive", "func (o *Orchestrator)", "\tif err != nil {"} {
			if strings.Contains(bundle, forbidden) {
				t.Fatalf("the review bundle carries repository source: %q", forbidden)
			}
		}
		// And the rule it must apply is stated to it.
		if !strings.Contains(bundle, "unverifiable_repository_claim") {
			t.Fatal("the reviewer is not told what to do with an ungrounded repository claim")
		}
		if !strings.Contains(bundle, "authorized repository:// evidence reference") {
			t.Fatal("the reviewer is not told that repository claims require an authorized citation")
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
