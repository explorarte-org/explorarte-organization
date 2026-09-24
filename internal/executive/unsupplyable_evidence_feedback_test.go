package executive

import (
	"context"
	"strings"
	"testing"
)

// Root 1223: the adjudicator, judging a campaign about a test, demanded evidence of the
// test's symbol. The corpus excludes test files by policy, so the host could never supply
// it, and it answered three times with CAPACITY_CONFLICT -- naming a ceiling (ranges 2/16,
// bytes 3068/98304) nobody had reached. These pin the two repairs: the adjudicator is told
// what the corpus can deliver, and a slot it cannot deliver is refused with the correction.

// Regression A: the adjudication contract states that facts the owner put in
// campaign_target need no repository evidence, and that tests are outside the corpus.
func TestAdjudicationContractSaysTargetFactsNeedNoRepositoryEvidence(t *testing.T) {
	contract := executionContractFor(PurposeDesignAdjudication, nil)
	for _, want := range []string{
		"Supplyable-evidence rule",
		"test files (*_test.go) are outside that corpus by policy",
		"Facts the owner already stated in campaign_target",
		"the name of a test, the values of a case",
		"do not turn them into an evidence_requirements entry",
		"EVIDENCE_UNSUPPLYABLE",
		"do not repeat it unchanged",
	} {
		if !strings.Contains(contract, want) {
			t.Errorf("adjudication contract missing %q:\n%s", want, contract)
		}
	}
	// The rule rides only the adjudication run, and it does not weaken the existing-world
	// rule it is appended to.
	if !strings.Contains(contract, "Existing-world rule") {
		t.Error("the existing-world rule was displaced")
	}
	for _, purpose := range []ExecutionPurpose{PurposeDepartmentWorker, PurposeDepartmentReview, PurposeAdversarialReview} {
		if strings.Contains(executionContractFor(purpose, nil), "Supplyable-evidence rule") {
			t.Errorf("the adjudicator's rule leaked into %s", purpose)
		}
	}
	// It must not assert where a symbol is defined: discovery is approximate.
	if strings.Contains(contract, "defined only in a test") {
		t.Error("the guidance claims a symbol is defined only in a test")
	}
}

func testOnlyWorld() *probeWorldSource {
	world := r15World(true)
	world.worlds[targetSHA]["internal/digits/digits_test.go"] =
		"package digits\n\nfunc TestExtractDigitRunsCoreCases(t *testing.T) {\n\t_ = \"abc123def45\"\n}\n"
	return world
}

func testOnlyFixture(t *testing.T) *wiringFixture {
	t.Helper()
	sources := append(fullSupply(), r15ExtraSources()...)
	fixture := newWiringFixture(t, "revise", sources, []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition"}},
	}, WithRepositoryEvidenceSource("explorarte-organization", testOnlyWorld()))
	limitsDef := "repository://explorarte-organization@" + targetSHA + "/internal/executive/budget.go#L1-L8"
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Grounded.",` +
			`"evidence_refs":["` + wiringDefRef + `","` + limitsDef + `"],` +
			`"evidence":[` +
			`{"claim":"declared","subject":"MaxDesignRounds","relation":"definition","ref":"` + wiringDefRef + `"},` +
			`{"claim":"declared","subject":"DefaultLimits","relation":"definition","ref":"` + limitsDef + `"}]}`
	fixture.harness.adjudicationEvidence = `[{"subject":"TestExtractDigitRunsCoreCases","relations":["definition"]}]`
	return fixture
}

// Regression B: the exact root-1223 shape.
func TestATestOnlySymbolDemandIsRefusedAsUnsupplyableNotCapacity(t *testing.T) {
	fixture := testOnlyFixture(t)

	driveCapability(t, fixture, 24)

	task := adjudicationTaskOf(t, fixture)
	if task.ReasonCode != "model_result_contract_rejected" {
		t.Fatalf("adjudication closed as %q (%s)", task.ReasonCode, task.Reason)
	}
	for _, want := range []string{
		"EVIDENCE_UNSUPPLYABLE",
		"TestExtractDigitRunsCoreCases/definition",
		"Test files (*_test.go) are excluded from that corpus by policy",
		"Do not repeat this evidence requirement unchanged",
		"use campaign_target for facts the owner stated about a test",
		"request an eligible code symbol",
	} {
		if !strings.Contains(task.Reason, want) {
			t.Errorf("rejection missing %q, got: %q", want, task.Reason)
		}
	}
	if strings.Contains(task.Reason, "CAPACITY_CONFLICT") {
		t.Fatalf("an unsupplyable slot was mislabelled as capacity: %q", task.Reason)
	}
	// The message states what was measured, not where the symbol lives.
	if strings.Contains(task.Reason, "defined only in a test") {
		t.Fatalf("the rejection claims where the symbol is defined: %q", task.Reason)
	}
	if hasRoundRequirements(t, fixture, 2) {
		t.Fatal("an unsupplyable demand was adopted as a round obligation")
	}
}

// Regression F: the second attempt receives the durable feedback and can correct itself.
func TestTheRetryReadsTheUnsupplyableFeedbackAndCorrectsTheRequirement(t *testing.T) {
	fixture := testOnlyFixture(t)
	fixture.harness.adjudicationEvidenceAfterRejection = `[{"subject":"DefaultLimits","relations":["definition"]}]`

	driveCapability(t, fixture, 24)

	all, err := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	var first TaskRecord
	found := false
	for _, task := range all {
		if strings.Contains(task.IdempotencyKey, "design-adjudication:round:1") {
			first, found = task, true
		}
	}
	if !found {
		t.Fatal("the round-1 adjudication task does not exist")
	}
	if len(first.Attempts) != 2 {
		t.Fatalf("round-1 adjudication attempts=%d, want the rejected one and the corrected one", len(first.Attempts))
	}
	if first.Attempts[0].State != "failed" || !strings.Contains(first.Attempts[0].ResultSummary, "EVIDENCE_UNSUPPLYABLE") {
		t.Fatalf("the rejected attempt did not durably carry the feedback: %+v", first.Attempts[0])
	}
	switch first.Status {
	case "failed", "dead_letter", "retry_wait":
		t.Fatalf("the corrected retry did not land: status=%s (%s)", first.Status, first.Reason)
	}
	if !hasRoundRequirements(t, fixture, 2) {
		t.Fatal("the corrected requirement never bound round 2")
	}
}
