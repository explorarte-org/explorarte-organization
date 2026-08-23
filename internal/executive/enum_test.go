package executive

import (
	"encoding/json"
	"strings"
	"testing"
)

func adjudicationFindingArrays(t *testing.T, schema json.RawMessage) (accepted, rejected json.RawMessage) {
	t.Helper()
	var root struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema, &root); err != nil {
		t.Fatal(err)
	}
	return root.Properties["accepted_findings"], root.Properties["rejected_findings"]
}

// The static schema typed these arrays as plain strings while the host held
// them to an identifier pattern. The contract the model was shown was looser
// than the one it was judged by, so a sentence -- a perfectly valid string --
// passed the schema and failed the parse. A campaign died at adjudication
// with an explanatory clause recorded as a finding reference.
//
// The enum closes it at the only place that can: the provider.
func TestTheAdjudicatorMayOnlyCiteFindingsTheReviewRaised(t *testing.T) {
	accepted, rejected := adjudicationFindingArrays(t, DesignAdjudicationOutputSchemaFor([]string{"AR-001", "AR-002"}))
	for name, arr := range map[string]json.RawMessage{"accepted_findings": accepted, "rejected_findings": rejected} {
		var node struct {
			Items struct {
				Type string   `json:"type"`
				Enum []string `json:"enum"`
			} `json:"items"`
		}
		if err := json.Unmarshal(arr, &node); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(node.Items.Enum) != 2 || node.Items.Enum[0] != "AR-001" || node.Items.Enum[1] != "AR-002" {
			t.Fatalf("%s must enumerate exactly the review's findings, got %v", name, node.Items.Enum)
		}
	}
}

// With no findings there is nothing to enumerate, and the supported schema
// subset has no maxItems, so the field falls back to a described string. That
// is exactly why the host invariant exists rather than being left to the
// schema: the provider can only help when there is something to enumerate,
// and something must refuse a citation when it cannot.
func TestWithNoFindingsTheHostInvariantCarriesItAlone(t *testing.T) {
	accepted, _ := adjudicationFindingArrays(t, DesignAdjudicationOutputSchemaFor(nil))
	if strings.Contains(string(accepted), "enum") {
		t.Error("an empty enum is not valid JSON Schema and must not be emitted")
	}
	if !strings.Contains(string(accepted), "description") {
		t.Error("the fallback must at least tell the model what the field is for")
	}
	// And the host refuses what the schema could not.
	err := AssertFindingsExist(DesignAdjudication{AcceptedFindings: []string{"AR-001"}}, nil)
	if err == nil {
		t.Fatal("a review that raised nothing leaves nothing to accept")
	}
	if !strings.Contains(err.Error(), "never raised") {
		t.Fatalf("the refusal must say why: %v", err)
	}
}

// A well-formed identifier is not the same as a real one. "AR-009" satisfies
// every syntactic rule and still refers to nothing, and an adjudication that
// accepts findings nobody made is a verdict about a review that does not
// exist. The pattern check alone never caught this.
func TestACitationMustNameAFindingThatExists(t *testing.T) {
	raised := []string{"AR-001", "AR-002"}
	if err := AssertFindingsExist(DesignAdjudication{
		AcceptedFindings: []string{"AR-001"}, RejectedFindings: []string{"AR-002"},
	}, raised); err != nil {
		t.Fatalf("citing findings that exist must be allowed: %v", err)
	}
	for _, tc := range []struct {
		name string
		adj  DesignAdjudication
	}{
		{"accepted", DesignAdjudication{AcceptedFindings: []string{"AR-009"}}},
		{"rejected", DesignAdjudication{RejectedFindings: []string{"AR-009"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := AssertFindingsExist(tc.adj, raised)
			if err == nil || !strings.Contains(err.Error(), `"AR-009" was never raised`) {
				t.Fatalf("a syntactically valid identifier that refers to nothing must be refused: %v", err)
			}
		})
	}
}

// A malformed identifier from anywhere must not reach the provider, because a
// schema the provider rejects fails the whole call before any model sees it.
func TestMalformedIdentifiersNeverReachTheSchema(t *testing.T) {
	accepted, _ := adjudicationFindingArrays(t, DesignAdjudicationOutputSchemaFor(
		[]string{"AR-001", "not an id", "AR-001", ""}))
	var node struct {
		Items struct {
			Enum []string `json:"enum"`
		} `json:"items"`
	}
	if err := json.Unmarshal(accepted, &node); err != nil {
		t.Fatal(err)
	}
	if len(node.Items.Enum) != 1 || node.Items.Enum[0] != "AR-001" {
		t.Fatalf("only well-formed identifiers, deduplicated, may be enumerated: %v", node.Items.Enum)
	}
}

// Whatever the enum, the schema must stay strictly valid.
func TestTheDynamicSchemaStaysStrict(t *testing.T) {
	for _, ids := range [][]string{nil, {"AR-001"}, {"AR-001", "SEC-42"}} {
		assertStrictObject(t, "design-adjudication/v1", DesignAdjudicationOutputSchemaFor(ids))
	}
}

// The parse and the schema must agree on what a reference is: anything the
// enum permits must survive validation, and the prose that killed the
// campaign must not.
func TestTheEnumAgreesWithTheParser(t *testing.T) {
	if !findingIDPattern.MatchString("AR-001") {
		t.Fatal("an identifier the schema would enumerate must pass the parser")
	}
	if findingIDPattern.MatchString("AR-001 es valido: el bundle no contiene un artefacto") {
		t.Fatal("the prose that killed a campaign must still be rejected")
	}
}
