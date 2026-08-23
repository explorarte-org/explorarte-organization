package executive

import (
	"errors"
	"strings"
	"testing"
)

// Partial supply is the dangerous case, and it is the shape R5 actually had:
// applications of both limits were in context, the declarations were not.
//
// Asked per subject, the preflight passes -- there IS something for
// MaxDesignRounds. The worker then cannot produce a definition nobody gave it,
// and the structural validator calls that a contract rejection. The design is
// blamed for the host's gap, which is the misattribution this mechanism exists
// to prevent, surviving in the one case that does not look empty.
func TestASubjectSuppliedOnlyInPartIsStillInsufficient(t *testing.T) {
	required := AdoptEvidenceRequirements([]EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition", "application"}},
	}, EvidenceFromAdjudication)

	onlyApplication := map[EvidenceSlot][]string{
		{Subject: "MaxDesignRounds", Relation: "application"}: {orchestratorRef},
	}

	err := ValidateEvidenceSupply(required, onlyApplication)
	if !errors.Is(err, ErrEvidenceInsufficient) {
		t.Fatalf("a half-supplied subject passed preflight, so a worker would be called and then blamed: %v", err)
	}
	if !strings.Contains(err.Error(), "MaxDesignRounds/definition") {
		t.Fatalf("the refusal does not name the missing slot: %v", err)
	}

	// And the design that could only cite the application must not be
	// charged with the omission.
	result := WorkerResult{SchemaVersion: WorkerResultSchemaVersionV2, Evidence: []EvidenceItem{
		{Subject: "MaxDesignRounds", Relation: EvidenceApplication, Claim: "stops the loop", Ref: orchestratorRef},
	}}
	structureErr := ValidateEvidenceStructure(result, required, onlyApplication)
	if !errors.Is(structureErr, ErrEvidenceInsufficient) {
		t.Fatalf("the worker was charged with a slot the host never supplied: %v", structureErr)
	}
}

// A v2 artifact must not smuggle untyped citations past the topology.
func TestV2CannotCarryCitationsThatGroundNoClaim(t *testing.T) {
	body := []byte(`{"schema_version":"worker-result/v2","summary":"s",
	  "evidence_refs":["` + typesRef + `","` + budgetRef + `"],
	  "evidence":[{"subject":"MaxDesignRounds","relation":"definition","claim":"declared","ref":"` + typesRef + `"}]}`)
	if _, err := ParseWorkerResult(body, DefaultLimits()); !errors.Is(err, ErrContractRejected) {
		t.Fatalf("an untyped citation reached downstream verification through evidence_refs: %v", err)
	}
}

// What downstream verifies is exactly what some claim rests on.
func TestWhatGetsVerifiedIsExactlyWhatGroundsAClaim(t *testing.T) {
	body := []byte(`{"schema_version":"worker-result/v2","summary":"s","evidence_refs":[],
	  "evidence":[
	    {"subject":"MaxDesignRounds","relation":"definition","claim":"declared","ref":"` + typesRef + `"},
	    {"subject":"MaxDesignRounds","relation":"application","claim":"applied","ref":"` + orchestratorRef + `"}]}`)
	parsed, err := ParseWorkerResult(body, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.EvidenceRefs) != 2 || parsed.EvidenceRefs[0] != typesRef || parsed.EvidenceRefs[1] != orchestratorRef {
		t.Fatalf("derived refs do not match the structured citations: %v", parsed.EvidenceRefs)
	}
}

// v1 keeps its flat list: it never claimed to carry a topology.
func TestV1IsUnaffected(t *testing.T) {
	body := []byte(`{"schema_version":"worker-result/v1","summary":"s","evidence_refs":["` + budgetRef + `"]}`)
	parsed, err := ParseWorkerResult(body, DefaultLimits())
	if err != nil || len(parsed.EvidenceRefs) != 1 {
		t.Fatalf("a v1 artifact was judged by v2 rules: %v %v", parsed.EvidenceRefs, err)
	}
}
