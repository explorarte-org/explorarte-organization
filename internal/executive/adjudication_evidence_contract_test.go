package executive

import (
	"context"
	"encoding/json"

	"strings"
	"testing"
)

// Checkpoint C -- Existing-World Evidence Requirements.
//
// R14's adjudicator, twice, proposed creating "MaxModelCalls" and demanded
// definition/application evidence FOR it. The host's probe refused correctly
// both times -- a symbol a design proposes to create does not exist at the
// frozen pin and never will in this campaign's rounds -- but nothing had
// told the adjudicator that boundary existed before she answered. Three
// attempts died measuring a contract they had never been handed; the task
// went dead_letter and the campaign with it.
//
// The repair is one textual authority (adjudicationExistingWorldRule)
// rendered in both places that must never drift: the adjudication run's
// ExecutionContract and the output schema's own field description. The host's
// probe stays exactly as it was.

func TestAdjudicationContractStatesTheExistingWorldRule(t *testing.T) {
	guidance := adjudicationEvidenceContractGuidance()
	for _, want := range []string{
		"Existing-world rule",
		"frozen DesignBaseSHA pin",
		"the host probes every proposal against that pinned repository",
		"required_changes MAY prescribe creating, renaming or removing symbols",
		"NEVER create an evidence requirement for a symbol this design proposes to introduce",
		"Ground a proposed new symbol through the existing code and behavior it is meant to change",
	} {
		if !strings.Contains(guidance, want) {
			t.Errorf("guidance missing %q:\n%s", want, guidance)
		}
	}
	if again := adjudicationEvidenceContractGuidance(); again != guidance {
		t.Fatal("guidance is not deterministic")
	}

	// It rides ONLY the adjudication run: every other purpose keeps exactly
	// the contract it had before checkpoint C.
	if got := executionContractFor(PurposeDesignAdjudication, nil); got != guidance {
		t.Fatalf("adjudication contract must be exactly the existing-world rule:\n%s", got)
	}
	if got := executionContractFor(PurposeCEOPlan, nil); got != "" {
		t.Errorf("ceo plan unexpectedly carries %q", got)
	}
	if got := executionContractFor(PurposeAdversarialReview, nil); strings.Contains(got, "Existing-world rule") {
		t.Errorf("the adversarial reviewer was handed an adjudication-only rule:\n%s", got)
	}
}

// One authority, two renderings: the provider-facing schema's field
// description is BUILT from the same constant as the ExecutionContract, so
// neither can silently drift from what probeAdjudicationRequirements
// enforces.
func TestAdjudicationSchemaSharesTheSameAuthority(t *testing.T) {
	schema := string(DesignAdjudicationOutputSchema())
	for _, want := range []string{
		strings.Split(adjudicationExistingWorldRule, ":")[0], // opening clause, verbatim
		"NEVER create an evidence requirement for a symbol this design proposes to introduce",
		"relations are the roles a citation must play for a symbol",
	} {
		if !strings.Contains(schema, want) {
			t.Errorf("schema description missing shared authority fragment %q", want)
		}
	}
	if !strings.Contains(schema, `"enum":["definition","application"]`) {
		t.Fatal("the relations enum vanished from the schema")
	}
	if !json.Valid([]byte(schema)) {
		t.Fatal("the rewritten schema is no longer valid JSON")
	}
}

// THE C criterion end to end: on a revise flow the adjudication command
// carries the rule BEFORE the adjudicator answers -- and like every ExecutionContract
// rider it stays out of durable instructions and out of what retrieval
// searches for.
func TestAdjudicationRunCarriesTheExistingWorldRuleBeforeAnswering(t *testing.T) {
	fixture := newWiringFixture(t, "revise", fullSupply(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition"}},
	})
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Grounded.",` +
			`"evidence_refs":["` + wiringDefRef + `"],` +
			`"evidence":[{"claim":"declared","subject":"MaxDesignRounds","relation":"definition","ref":"` + wiringDefRef + `"}]}`

	if _, err := fixture.driveUntilStopped(t, 24); err != nil {
		t.Fatalf("a revise world failed: %v", err)
	}
	// A default revise body sends the design back each round until the bound
	// closes the loop -- that terminal state is this scripted world working,
	// not a defect. What matters here is that round 1's adjudication RAN and
	// was governed.
	command, ok := fixture.commandFor(PurposeDesignAdjudication)
	if !ok {
		t.Fatal("the adjudication never ran")
	}
	guidance := adjudicationEvidenceContractGuidance()
	if !strings.Contains(command.ExecutionContract, guidance) {
		t.Errorf("adjudication ExecutionContract missing the rule:\n%s", command.ExecutionContract)
	}

	task, ok := adjudicationTaskOfToleratingTerminal(t, fixture)
	if !ok {
		t.Fatal("no design adjudication task exists")
	}
	if strings.Contains(task.Instructions, "Existing-world rule") {
		t.Fatal("the rule was baked into durable instructions")
	}
	ctxPort := fixture.harness.contexts
	request, recorded := ctxPort.requests[command.Context.ID]
	if !recorded {
		t.Fatalf("no context request recorded for snapshot %d", command.Context.ID)
	}
	if strings.Contains(request.RepositoryQuery, "Existing-world") || strings.Contains(request.RepositoryQuery, "DesignBaseSHA pin") {
		t.Fatal("the rule changed what retrieval searched for")
	}
	for _, subject := range request.RepositorySubjects {
		if strings.Contains(subject, "Existing-world") {
			t.Fatalf("the rule leaked into retrieval subjects: %v", request.RepositorySubjects)
		}
	}
}

// adjudicationTaskOfToleratingTerminal mirrors adjudicationTaskOf without
// failing when the fixture drove past the adjudication into later phases.
func adjudicationTaskOfToleratingTerminal(t *testing.T, f *wiringFixture) (TaskRecord, bool) {
	t.Helper()
	all, err := f.tasks.ListByCorrelation(context.Background(), f.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range all {
		if task.TaskClass == TaskClassCoordinationDesignAdjudication {
			return task, true
		}
	}
	return TaskRecord{}, false
}

// Reviewer guard: required_changes prescribing a NEW symbol is valid prose --
// the host refuses only unsupplyable evidence_requirements, never change
// prescriptions. A verdict that creates MaxModelCalls on paper while grounding
// its round in symbols that DO exist passes the probe and binds round 2.
func TestRequiredChangesMayPrescribeNewSymbolsWhenEvidenceIsOfTheExistingWorld(t *testing.T) {
	fixture := newWiringFixture(t, "revise", fullSupply(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition"}},
	}, WithRepositoryEvidenceSource("explorarte-organization", capabilityWorld()))
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Grounded.",` +
			`"evidence_refs":["` + wiringDefRef + `","` + wiringAppRef + `"],` +
			`"evidence":[` +
			`{"claim":"declared","subject":"MaxDesignRounds","relation":"definition","ref":"` + wiringDefRef + `"},` +
			`{"claim":"applied","subject":"MaxDesignRounds","relation":"application","ref":"` + wiringAppRef + `"}]}`
	fixture.harness.adjudicationEvidence = `[{"subject":"driveDesignFreeze","relations":["application"]}]`
	fixture.harness.adjudicationRequiredChanges = []string{
		"Create MaxModelCalls and remove per-call termination from both design loops",
	}

	driveCapability(t, fixture, 24)

	if hasRoundRequirements(t, fixture, 2) != true {
		t.Fatal("a valid revise grounded in existing symbols did not bind its next round")
	}
	allTasks, _ := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	for _, task := range allTasks {
		if task.TaskClass == TaskClassCoordinationDesignAdjudication && task.ReasonCode == "model_result_contract_rejected" {
			t.Fatalf("valid required_changes were rejected: %s", task.Reason)
		}
	}
}

// And the mirror: demanding evidence FOR MaxModelCalls -- the very failure
// that killed R14's adjudication twice -- is still refused by the unchanged
// host, with feedback naming the impossible pair for the retry.
func TestAnEvidenceRequirementForAProposedSymbolIsStillRefused(t *testing.T) {
	fixture := newWiringFixture(t, "revise", fullSupply(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition"}},
	}, WithRepositoryEvidenceSource("explorarte-organization", capabilityWorld()))
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Grounded.",` +
			`"evidence_refs":["` + wiringDefRef + `","` + wiringAppRef + `"],` +
			`"evidence":[` +
			`{"claim":"declared","subject":"MaxDesignRounds","relation":"definition","ref":"` + wiringDefRef + `"},` +
			`{"claim":"applied","subject":"MaxDesignRounds","relation":"application","ref":"` + wiringAppRef + `"}]}`
	fixture.harness.adjudicationEvidence = `[{"subject":"MaxModelCalls","relations":["definition"]}]`
	fixture.harness.adjudicationRequiredChanges = []string{
		"Create MaxModelCalls and remove per-call termination from both design loops",
	}

	driveCapability(t, fixture, 24)

	sawRejection := false
	for _, code := range fixture.tasks.failed {
		if code == "model_result_contract_rejected" {
			sawRejection = true
		}
	}
	if !sawRejection {
		t.Fatal("an evidence demand for a proposed symbol was accepted")
	}
	task := adjudicationTaskOf(t, fixture)
	if task.ReasonCode != "model_result_contract_rejected" {
		t.Fatalf("adjudication closed as %q", task.ReasonCode)
	}
	for _, want := range []string{"MaxModelCalls/definition", "CAPACITY_CONFLICT"} {
		if !strings.Contains(task.Reason, want) {
			t.Fatalf("rejection feedback missing %q, got: %q", want, task.Reason)
		}
	}
	if hasRoundRequirements(t, fixture, 2) {
		t.Fatal("an obligation for a not-yet-existing symbol was adopted")
	}
	// Retry economics untouched: the rejection is an attempt failure under
	// the SAME attempt ceiling -- nothing here raised max attempts.
	if task.MaxAttempts != fakeDefaultMaxAttempts {
		t.Fatalf("attempt ceiling drifted: %d", task.MaxAttempts)
	}
}
