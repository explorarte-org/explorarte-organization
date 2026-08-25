package executive

import (
	"context"
	"strings"
	"testing"
)

// FRONTIER #4 -- joint evidence admission + mandatory slot delivery,
// validated against R15's exact death: four adjudicated subjects passed four
// independent probes, then round 2's single shared budget starved
// driveDesignFreeze/application and the preflight killed the worker before
// any model call. Under checkpoint D only two outcomes exist:
//
//	A) the set fits   -> every round-2 snapshot contains every demanded slot;
//	B) it does not fit -> admission refuses BEFORE round 2 is adopted.
//
// What may never happen again: acceptance, then a round-2 worker discovering
// evidence_insufficient.

// r15World is the pinned tree admission reads: every symbol Luna's R15 revise
// demanded, laid out so definitions and applications live in different files
// -- the layout that starved the old selection.
func r15World(withApplicationSite bool) *probeWorldSource {
	orchestrator := "package executive\n\nfunc step(o *Orchestrator) {\n\tdone, err := o.driveDesignFreeze(context.Background())\n\t_ = done\n\t_ = err\n}\n"
	if !withApplicationSite {
		// Path B: the call site does not exist anywhere -- the symbol is
		// declared but never applied, so the application slot cannot be
		// delivered by any arrangement.
		orchestrator = "package executive\n\nfunc step(o *Orchestrator) {\n\t_ = o\n}\n"
	}
	return &probeWorldSource{worlds: map[string]map[string]string{targetSHA: {
		"internal/executive/types.go": `package executive

type Limits struct {
	MaxDesignRounds int
}
`,
		"internal/executive/budget.go": `package executive

func DefaultLimits() Limits {
	return Limits{}
}
`,
		"internal/executive/freeze.go": `package executive

func (o *Orchestrator) driveDesignFreeze(ctx context.Context) (bool, error) {
	return false, nil
}
`,
		"internal/executive/orchestrator.go": orchestrator,
	}}}
}

// r15ExtraSources are the read-back records the fixture's snapshots serve for
// the slots Luna's revise demands -- the delivery half of the promise.
func r15ExtraSources() []SnapshotSource {

	freezeApp := "repository://explorarte-organization@" + targetSHA + "/internal/executive/orchestrator.go#L1-L9"
	limitsDef := "repository://explorarte-organization@" + targetSHA + "/internal/executive/budget.go#L1-L8"
	freezeDecl := "repository://explorarte-organization@" + targetSHA + "/internal/executive/freeze.go#L1-L8"
	return []SnapshotSource{
		{Kind: "repository_evidence", Reference: freezeDecl, Version: targetSHA, Included: true,
			Content: "\nfunc (o *Orchestrator) driveDesignFreeze(ctx context.Context) (bool, error) {\n\treturn false, nil\n}\n"},
		{Kind: "repository_evidence", Reference: freezeApp, Version: targetSHA, Included: true,
			Content: "\nfunc step(o *Orchestrator) {\n\tdone, err := o.driveDesignFreeze(context.Background())\n\t_ = done\n\t_ = err\n}\n"},
		{Kind: "repository_evidence", Reference: limitsDef, Version: targetSHA, Included: true,
			Content: "\nfunc DefaultLimits() Limits {\n\treturn Limits{}\n}\n"},
	}
}

// r15Fixture builds the campaign world for one outcome of the mirror: R15's
// own revise demands, a world that either carries or lacks the application
// site, and a worker body that answers every slot both rounds will demand.
func r15Fixture(t *testing.T, withApplicationSite bool) *wiringFixture {
	t.Helper()
	sources := append(fullSupply(), r15ExtraSources()...)
	fixture := newWiringFixture(t, "revise", sources, []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition"}},
	}, WithRepositoryEvidenceSource("explorarte-organization", r15World(withApplicationSite)))

	freezeApp := "repository://explorarte-organization@" + targetSHA + "/internal/executive/orchestrator.go#L1-L9"
	limitsDef := "repository://explorarte-organization@" + targetSHA + "/internal/executive/budget.go#L1-L8"
	// One superset body answering every slot both rounds will demand: the
	// owner slot of this fixture plus the two obligations Luna's revise adds.
	items := []string{
		`{"claim":"declared","subject":"MaxDesignRounds","relation":"definition","ref":"` + wiringDefRef + `"}`,
		`{"claim":"declared","subject":"driveDesignFreeze","relation":"application","ref":"` + freezeApp + `"}`,
		`{"claim":"declared","subject":"DefaultLimits","relation":"definition","ref":"` + limitsDef + `"}`,
	}
	refs := []string{wiringDefRef, freezeApp, limitsDef}
	body := `{"schema_version":"worker-result/v2","summary":"Grounded.",` +
		`"evidence_refs":[` + refsJSON(refs) + `],"evidence":[` + strings.Join(items, ",") + `]}`
	fixture.harness.bodies[PurposeDepartmentWorker] = body
	fixture.harness.adjudicationEvidence =
		`[{"subject":"driveDesignFreeze","relations":["application"]},` +
			`{"subject":"DefaultLimits","relations":["definition"]}]`
	return fixture
}

func refsJSON(refs []string) string {
	out := ""
	for index, ref := range refs {
		if index > 0 {
			out += ","
		}
		out += `"` + ref + `"`
	}
	return out
}

// OUTCOME A: the set fits. Admission accepts, round 2 opens and plans, the
// round-2 worker's build receives the normative slots, and the campaign never
// blocks on an evidence shortfall.
func TestR15MirrorFittingSetAdmitsAndDelivers(t *testing.T) {
	fixture := r15Fixture(t, true)

	driveCapability(t, fixture, 24)

	root := fixture.rootRecord(t)
	for _, reason := range []string{ReasonEvidenceInsufficient, ReasonEvidenceDeliveryViolation} {
		if root.ReasonCode == reason {
			t.Fatalf("OUTCOME violated: a fitting set still ended %q (%s)", reason, root.Reason)
		}
	}
	if !hasRoundRequirements(t, fixture, 2) {
		t.Fatal("an accepted revise never bound its next round")
	}
	// Delivery: some round-2 worker execution received the normative slots in
	// its context request -- the relation survived the journey.
	seenSlot := false
	for _, request := range fixture.harness.contexts.requests {
		for _, slot := range request.RepositorySlots {
			if slot.Subject == "driveDesignFreeze" && slot.Relation == "application" {
				seenSlot = true
			}
		}
	}
	if !seenSlot {
		t.Fatal("no round-2 build ever received the driveDesignFreeze/application slot")
	}
}

// OUTCOME B: the set does not fit. Admission refuses at the adjudicator's own
// boundary, naming the impossible slot, and round 2 is never born.
func TestR15MirrorUnfitSetIsRefusedBeforeRoundTwoExists(t *testing.T) {
	fixture := r15Fixture(t, false)

	driveCapability(t, fixture, 24)

	task := adjudicationTaskOf(t, fixture)
	if task.ReasonCode != "model_result_contract_rejected" {
		t.Fatalf("adjudication closed as %q; the unfit set must be refused at admission", task.ReasonCode)
	}
	for _, want := range []string{"joint evidence capacity cannot deliver", "driveDesignFreeze/application"} {
		if !strings.Contains(task.Reason, want) {
			t.Fatalf("rejection feedback missing %q, got: %q", want, task.Reason)
		}
	}
	all, err := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, other := range all {
		if strings.Contains(other.IdempotencyKey, "design-round:2") && other.Status != "" {
			t.Fatalf("round 2 was born beside an unadmitted set: %s %s", other.IdempotencyKey, other.Status)
		}
	}
	if hasRoundRequirements(t, fixture, 2) {
		t.Fatal("an unadmitted set was adopted as round obligations")
	}
}

// Whose promise broke decides the record: owner-goal shortfalls stay
// evidence_insufficient; a shortfall under an ADMITTED adjudication plan is
// the host's own broken promise.
func TestDeliveryShortfallClassificationFollowsThePromise(t *testing.T) {
	owner := []EvidenceRequirement{{Subject: "MaxDesignRounds", Relations: []string{"definition"}, Source: EvidenceFromOwnerAcceptance}}
	if got := evidenceFailureReason(owner); got != ReasonEvidenceInsufficient {
		t.Fatalf("owner shortfall classified %q, want evidence_insufficient", got)
	}
	mixed := []EvidenceRequirement{
		{Subject: "MaxDesignRounds", Relations: []string{"definition"}, Source: EvidenceFromOwnerAcceptance},
		{Subject: "driveDesignFreeze", Relations: []string{"application"}, Source: EvidenceFromAdjudication},
	}
	if got := evidenceFailureReason(mixed); got != ReasonEvidenceDeliveryViolation {
		t.Fatalf("admitted-plan shortfall classified %q, want evidence_delivery_violation", got)
	}
}
