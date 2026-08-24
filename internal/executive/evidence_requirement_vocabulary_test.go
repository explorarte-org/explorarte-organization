package executive

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/repositoryevidence"
)

// AUTONOMY-SMOKE-017-R9 closed as EVIDENCE REQUIREMENT CAPABILITY DRIFT: two
// concepts were sharing one vocabulary. A worker artifact may legitimately say
// test or context about a citation, but the repository sensor that must supply
// an obligation answers only definition or application -- ClassifyExcerpt has
// no third answer. An adjudication could therefore mint obligations no
// snapshot could ever fill, and round 2 died in the preflight by construction.
//
// These guards pin the split: the demandable vocabulary shrinks to what the
// sensor can prove, the artifact vocabulary keeps all four relations, and the
// provider-facing adjudicator schema offers exactly the demandable set.

// What a worker artifact SAYS was never the problem: test and context remain
// legitimate descriptions of a citation in worker-result/v2.
func TestWorkerArtifactStillAcceptsTestAndContextRelations(t *testing.T) {
	const (
		refTest    = "repository://explorarte-organization@" + targetSHA + "/internal/executive/limits_test.go#L10-L20"
		refContext = "repository://explorarte-organization@" + targetSHA + "/internal/executive/limits.go#L5-L15"
	)
	artifact := []byte(`{"schema_version":"worker-result/v2","summary":"Grounded.",` +
		`"evidence_refs":["` + refTest + `","` + refContext + `"],` +
		`"evidence":[` +
		`{"claim":"the bound holds under repetition","subject":"MaxDesignRounds","relation":"test","ref":"` + refTest + `"},` +
		`{"claim":"the bound lives beside its peers","subject":"MaxDesignRounds","relation":"context","ref":"` + refContext + `"}]}`)
	if _, err := ParseWorkerResult(artifact, DefaultLimits()); err != nil {
		t.Fatalf("worker-result/v2 lost its richer citation vocabulary: %v", err)
	}
}

// What an obligation DEMANDS is bounded by what can ever be supplied.
func TestRequirementProposalsCannotDemandRelationsNoSensorCanSupply(t *testing.T) {
	cases := []struct {
		name      string
		relations []string
	}{
		{name: "test-only", relations: []string{"test"}},
		{name: "context-only", relations: []string{"context"}},
		{name: "mixed-valid-then-impossible", relations: []string{"definition", "context"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEvidenceRequirementProposals(
				[]EvidenceRequirementProposal{{Subject: "MaxDesignRounds", Relations: tc.relations}},
				DefaultLimits())
			if !errors.Is(err, ErrContractRejected) {
				t.Fatalf("an unsuppliable obligation passed validation: %v", err)
			}
		})
	}

	mixed := validateEvidenceRequirementProposals(
		[]EvidenceRequirementProposal{{Subject: "MaxDesignRounds", Relations: []string{"definition", "test"}}},
		DefaultLimits())
	for _, want := range []string{
		"evidence_requirements[0].relations[1]",
		`relation "test" cannot be required`,
		"supported requirement relations: definition, application",
	} {
		if !strings.Contains(mixed.Error(), want) {
			t.Errorf("rejection feedback missing %q, got: %v", want, mixed)
		}
	}

	if err := validateEvidenceRequirementProposals(
		[]EvidenceRequirementProposal{{Subject: "MaxDesignRounds", Relations: []string{"definition", "application"}}},
		DefaultLimits()); err != nil {
		t.Fatalf("the supplyable relations must keep passing: %v", err)
	}
}

// The adjudicator's provider-facing contract must offer exactly what the host
// can demand-enforce: offering "test" in the enum would recreate the
// prompt-stricter-than-host drift this campaign family exists to remove.
func TestAdjudicationSchemaOffersOnlyTheDemandableRelations(t *testing.T) {
	var schema struct {
		Properties struct {
			EvidenceRequirements struct {
				Items struct {
					Properties struct {
						Relations struct {
							Items struct {
								Enum []string `json:"enum"`
							} `json:"items"`
						} `json:"relations"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"evidence_requirements"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(DesignAdjudicationOutputSchema(), &schema); err != nil {
		t.Fatalf("adjudication schema is not valid JSON: %v", err)
	}
	got := schema.Properties.EvidenceRequirements.Items.Properties.Relations.Items.Enum
	if len(got) != len(supportedEvidenceRequirementRelations) {
		t.Fatalf("schema enum=%v, want exactly %v", got, supportedEvidenceRequirementRelations)
	}
	for i, relation := range supportedEvidenceRequirementRelations {
		if got[i] != relation {
			t.Fatalf("schema enum=%v, want %v in order", got, supportedEvidenceRequirementRelations)
		}
	}
}

// THE R9 STATEMENT: set(demandable) == set(supplyable). The obligations the
// host accepts are worth exactly what its own classifier can answer for --
// never more, or a round becomes doomed at adoption; never fewer, or the
// contract under-demands the world it can actually see.
func TestDemandableRelationsEqualSupplyableRelations(t *testing.T) {
	supplyable := map[string]struct{}{}
	excerpts := map[string]string{
		"a declaration opens a line": "\n// MaxDesignRounds bounds design rounds.\nMaxDesignRounds int\n",
		"a use mentions it mid-line": "\tif round > o.limits.MaxDesignRounds {\n\t\treturn Run{}\n\t}\n",
	}
	for _, excerpt := range excerpts {
		if relation, mentions := repositoryevidence.ClassifyExcerpt(excerpt, "MaxDesignRounds"); mentions {
			supplyable[relation] = struct{}{}
		}
	}
	if len(supplyable) != len(supportedEvidenceRequirementRelations) {
		t.Fatalf("sensor supplies %v but the contract demands exactly %v",
			supplyable, supportedEvidenceRequirementRelations)
	}
	for _, relation := range supportedEvidenceRequirementRelations {
		if _, supplied := supplyable[relation]; !supplied {
			t.Fatalf("obligations may demand %q, which the sensor cannot classify into existence", relation)
		}
	}
}
