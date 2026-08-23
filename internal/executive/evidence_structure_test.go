package executive

import (
	"errors"
	"strings"
	"testing"
)

const (
	typesRef        = "repository://org@sha/internal/executive/types.go#L121-L169"
	orchestratorRef = "repository://org@sha/internal/executive/orchestrator.go#L592-L640"
	budgetRef       = "repository://org@sha/internal/executive/budget.go#L31-L65"
)

func rounds() []EvidenceRequirement {
	return []EvidenceRequirement{{Subject: "MaxDesignRounds", Relations: []string{EvidenceDefinition, EvidenceApplication}}}
}

func supplied() map[EvidenceSlot][]string {
	return map[EvidenceSlot][]string{
		{Subject: "MaxDesignRounds", Relation: EvidenceDefinition}:  {typesRef},
		{Subject: "MaxDesignRounds", Relation: EvidenceApplication}: {orchestratorRef, budgetRef},
	}
}

// The form the artifact could not express before.
func TestASeparatedDesignIsAcceptedWithoutAnAdjudicator(t *testing.T) {
	result := WorkerResult{SchemaVersion: WorkerResultSchemaVersionV2, Evidence: []EvidenceItem{
		{Subject: "MaxDesignRounds", Relation: EvidenceDefinition, Claim: "declared in Limits", Ref: typesRef},
		{Subject: "MaxDesignRounds", Relation: EvidenceApplication, Claim: "stops the design loop", Ref: orchestratorRef},
	}}
	if err := ValidateEvidenceStructure(result, rounds(), supplied()); err != nil {
		t.Fatalf("a design that separated its citations was refused: %v", err)
	}
}

// R5's actual shape: one range offered for both roles.
func TestOneRangeCannotStandForBothRoles(t *testing.T) {
	result := WorkerResult{SchemaVersion: WorkerResultSchemaVersionV2, Evidence: []EvidenceItem{
		{Subject: "MaxDesignRounds", Relation: EvidenceDefinition, Claim: "declared", Ref: budgetRef},
		{Subject: "MaxDesignRounds", Relation: EvidenceApplication, Claim: "applied", Ref: budgetRef},
	}}
	err := ValidateEvidenceStructure(result, rounds(), supplied())
	if !errors.Is(err, ErrContractRejected) {
		t.Fatalf("the same range grounded two different roles: %v", err)
	}
}

// worker-result/v1 cannot express the relation at all, so demanding it of a v1
// artifact must fail on the contract, not on the worker's diligence.
func TestAnArtifactThatCannotExpressRelationsIsRefusedAsSuch(t *testing.T) {
	result := WorkerResult{SchemaVersion: WorkerResultSchemaVersion, EvidenceRefs: []string{typesRef, orchestratorRef}}
	err := ValidateEvidenceStructure(result, rounds(), supplied())
	if !errors.Is(err, ErrContractRejected) || !strings.Contains(err.Error(), "cannot express") {
		t.Fatalf("a v1 artifact was judged as if it could have answered: %v", err)
	}
}

// The one that matters most: the host showed the worker nothing for this
// subject. Blaming the design here would turn repository blindness into
// contract blindness.
func TestASlotTheHostNeverSuppliedIsNotTheWorkersFailure(t *testing.T) {
	result := WorkerResult{SchemaVersion: WorkerResultSchemaVersionV2, Evidence: []EvidenceItem{
		{Subject: "MaxDepartmentReplans", Relation: EvidenceApplication, Claim: "applied", Ref: budgetRef},
	}}
	required := []EvidenceRequirement{{Subject: "MaxDesignRounds", Relations: []string{EvidenceDefinition}}}
	err := ValidateEvidenceStructure(result, required, map[EvidenceSlot][]string{})
	if !errors.Is(err, ErrEvidenceInsufficient) {
		t.Fatalf("an unfillable slot was charged to the worker: %v", err)
	}
	if errors.Is(err, ErrContractRejected) {
		t.Fatal("host insufficiency must not also read as a contract rejection")
	}
}

// A citation the host never showed cannot ground a claim, even in v2 form.
func TestACitationThatWasNeverSuppliedDoesNotFillASlot(t *testing.T) {
	result := WorkerResult{SchemaVersion: WorkerResultSchemaVersionV2, Evidence: []EvidenceItem{
		{Subject: "MaxDesignRounds", Relation: EvidenceDefinition, Claim: "declared", Ref: "repository://org@sha/internal/invented/file.go#L1-L9"},
		{Subject: "MaxDesignRounds", Relation: EvidenceApplication, Claim: "applied", Ref: orchestratorRef},
	}}
	if err := ValidateEvidenceStructure(result, rounds(), supplied()); !errors.Is(err, ErrContractRejected) {
		t.Fatalf("an unsupplied citation filled a slot: %v", err)
	}
}

// Structured evidence must reach the citation verifier, which reads the flat
// list: a claim grounded in a ref nobody verifies is not grounded.
func TestStructuredEvidenceReachesTheRefsThatGetVerified(t *testing.T) {
	body := []byte(`{"schema_version":"worker-result/v2","summary":"s","evidence_refs":[],"evidence":[
	  {"subject":"MaxDesignRounds","relation":"definition","claim":"declared","ref":"` + typesRef + `"}]}`)
	parsed, err := ParseWorkerResult(body, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.EvidenceRefs) != 1 || parsed.EvidenceRefs[0] != typesRef {
		t.Fatalf("the structured citation never reached evidence_refs: %v", parsed.EvidenceRefs)
	}
}
