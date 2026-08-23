package executive

import (
	"errors"
	"strings"
	"testing"
)

// THE architectural property: retrieval must not change what must be proved.
//
// If the selector could create requirements, the sensor would decide which
// facts it is obliged to sense. AUTONOMY-SMOKE-017-R5 is the worked example:
// internal/executive/types.go never reached the worker, so a
// retrieval-derived contract would have stopped demanding a definition and
// declared the design complete. Repository blindness would have become
// undetectable by construction.
func TestRemovingEvidenceDoesNotRemoveTheObligation(t *testing.T) {
	required := AdoptEvidenceRequirements([]EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition", "application"}},
	}, EvidenceFromAdjudication)

	withTypes := map[string][]string{"MaxDesignRounds": {typesRef, orchestratorRef}}
	withoutTypes := map[string][]string{}

	// The obligation is the same object in both worlds.
	if err := ValidateEvidenceSupply(required, withTypes); err != nil {
		t.Fatalf("a supplied world was called insufficient: %v", err)
	}
	err := ValidateEvidenceSupply(required, withoutTypes)
	if !errors.Is(err, ErrEvidenceInsufficient) {
		t.Fatalf("removing the evidence removed the obligation instead of failing: %v", err)
	}
	if !strings.Contains(err.Error(), "MaxDesignRounds") {
		t.Fatalf("the failure does not name what could not be grounded: %v", err)
	}
	// And it is still demanded, not quietly dropped.
	if len(required) != 1 || len(required[0].Relations) != 2 {
		t.Fatalf("the requirement set changed with the world: %+v", required)
	}
}

// Insufficiency must be discovered before the worker runs, not while its
// artifact is being judged -- which is where the temptation to blame the
// artifact lives.
func TestSupplyIsCheckedBeforeAnyWorkerRuns(t *testing.T) {
	required := AdoptEvidenceRequirements([]EvidenceRequirementProposal{
		{Subject: "MaxDepartmentReplans", Relations: []string{"definition"}},
	}, EvidenceFromOwnerAcceptance)
	if err := ValidateEvidenceSupply(required, map[string][]string{"MaxDesignRounds": {typesRef}}); !errors.Is(err, ErrEvidenceInsufficient) {
		t.Fatalf("a subject with no citations passed preflight: %v", err)
	}
}

// Obligations carry provenance, because the answer to "who may change this?"
// depends on where it came from.
func TestAnObligationRecordsWhereItCameFrom(t *testing.T) {
	adopted := AdoptEvidenceRequirements([]EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"application", "definition"}},
	}, EvidenceFromAdjudication)
	if adopted[0].Source != EvidenceFromAdjudication {
		t.Fatalf("provenance was lost: %q", adopted[0].Source)
	}
	// Canonical order, so the same proposals always produce the same contract.
	if adopted[0].Relations[0] != "application" || adopted[0].Relations[1] != "definition" {
		t.Fatalf("relations are not canonical: %v", adopted[0].Relations)
	}
}

// A reviewer proposes; it does not legislate. Only a revise opens a round, so
// only a revise may bind one.
func TestOnlyAReviseCanBindTheNextRound(t *testing.T) {
	body := func(verdict string) []byte {
		return []byte(`{"schema_version":"design-adjudication/v1","verdict":"` + verdict + `",` +
			`"accepted_findings":[],"rejected_findings":[],"required_changes":[],` +
			`"unresolved_owner_decisions":[],"evidence_refs":[],"design_digest":"1111111111111111111111111111111111111111111111111111111111111111",` +
			`"evidence_requirements":[{"subject":"MaxDesignRounds","relations":["definition"]}]}`)
	}
	if _, err := ParseDesignAdjudication(body("freeze"), testDesign(), DefaultLimits()); !errors.Is(err, ErrContractRejected) {
		t.Fatalf("a freeze bound a round that will never happen: %v", err)
	}
	if _, err := ParseDesignAdjudication(body("reject"), testDesign(), DefaultLimits()); !errors.Is(err, ErrContractRejected) {
		t.Fatalf("a reject bound a round that will never happen: %v", err)
	}
}

// The vocabulary is the host's. A model that could invent relations would be
// writing the exam as well as sitting it.
func TestAProposalCannotInventItsOwnVocabulary(t *testing.T) {
	for _, proposals := range [][]EvidenceRequirementProposal{
		{{Subject: "MaxDesignRounds", Relations: []string{"vibes"}}},
		{{Subject: "MaxDesignRounds", Relations: nil}},
		{{Subject: "  ", Relations: []string{"definition"}}},
		{{Subject: "X", Relations: []string{"definition"}}, {Subject: "X", Relations: []string{"application"}}},
		{{Subject: "X", Relations: []string{"definition", "definition"}}},
	} {
		if err := validateEvidenceRequirementProposals(proposals, DefaultLimits()); err == nil {
			t.Errorf("accepted a malformed proposal: %+v", proposals)
		}
	}
}
