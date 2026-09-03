package executive

import (
	"encoding/json"
	"strings"
	"testing"
)

// Prompt and schema are two views of one host-owned answer key. Keeping this
// assertion at the same fixture boundary prevents a future change from
// teaching the worker one set of refs while the provider schema accepts a
// different set.
func TestWorkerEvidencePromptAndSchemaShareAvailableRefs(t *testing.T) {
	required := []EvidenceRequirement{
		{Subject: "MaxDesignRounds", Relations: []string{"definition"}},
		{Subject: "MaxDepartmentReplans", Relations: []string{"application"}},
	}
	available := map[EvidenceSlot][]string{
		{Subject: "MaxDesignRounds", Relation: "definition"}: {
			"repository://explorarte-organization@pin/internal/executive/types.go#L1-L8",
		},
		{Subject: "MaxDepartmentReplans", Relation: "application"}: {
			"repository://explorarte-organization@pin/internal/executive/orchestrator.go#L20-L28",
		},
	}
	proofs := map[EvidenceSlot]EvidenceProof{}
	contract := executionContractForWithSupply(PurposeDepartmentWorker, required, proofs, available)
	schema := WorkerResultOutputSchemaForSlots(DefaultLimits(), required, available, proofs)

	var document struct {
		Properties struct {
			Evidence struct {
				Items struct {
					Properties struct {
						Ref struct {
							Enum []string `json:"enum"`
						} `json:"ref"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"evidence"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema, &document); err != nil {
		t.Fatalf("worker schema is not valid JSON: %v", err)
	}

	for slot, refs := range available {
		for _, ref := range refs {
			if !strings.Contains(contract, ref) {
				t.Errorf("available ref %q for %+v is missing literally from execution contract", ref, slot)
			}
			found := false
			for _, enumRef := range document.Properties.Evidence.Items.Properties.Ref.Enum {
				if enumRef == ref {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("available ref %q for %+v is missing from schema enum %v", ref, slot, document.Properties.Evidence.Items.Properties.Ref.Enum)
			}
		}
	}
}
