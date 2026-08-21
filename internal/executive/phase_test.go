package executive

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
)

func TestABareStringIsRefusedBecauseItCarriesNoPhase(t *testing.T) {
	var criterion AcceptanceCriterion
	err := json.Unmarshal([]byte(`"the gates pass"`), &criterion)
	if err == nil {
		t.Fatal("a criterion with no phase has no safe default and must be refused")
	}
	for _, want := range []string{"design", "implementation", "promotion"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name the phases the owner can choose; missing %q", want)
		}
	}
}

func TestAnUnknownPhaseIsRefused(t *testing.T) {
	var criterion AcceptanceCriterion
	err := json.Unmarshal([]byte(`{"text":"x","phase":"someday"}`), &criterion)
	if err == nil || !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("an invented phase must be refused: %v", err)
	}
}

func TestAValidCriterionDecodes(t *testing.T) {
	var criterion AcceptanceCriterion
	if err := json.Unmarshal([]byte(`{"text":"  the design names the path  ","phase":"design"}`), &criterion); err != nil {
		t.Fatal(err)
	}
	if criterion.Text != "the design names the path" || criterion.Phase != AcceptanceDesign {
		t.Fatalf("got %+v", criterion)
	}
}

func mixedPhaseCriteria() []AcceptanceCriterion {
	return []AcceptanceCriterion{
		{Text: "the design names the exact file it will create", Phase: AcceptanceDesign},
		{Text: "the host gates pass", Phase: AcceptanceImplementation},
		{Text: "the change reaches the target only through the governed flow", Phase: AcceptancePromotion},
	}
}

func bundleFixture(t *testing.T) ([]byte, error) {
	t.Helper()
	models := &bodyModels{results: map[int64]InvocationResult{
		100: {InvocationID: 100, TextOutput: "The design creates docs/x.md and changes nothing else.", ResponseHash: "aaa"},
	}}
	acceptance := newMemoryAcceptance()
	if err := acceptance.RecordAcceptance(context.Background(), 7, mixedPhaseCriteria()); err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{models: models, acceptance: acceptance, limits: Limits{MaxStringBytes: 4096}}
	root := TaskRecord{ID: 7, AcceptanceCriteria: AcceptanceTexts(mixedPhaseCriteria())}
	artifact := designArtifact{RootTaskID: 7, Round: 1, Units: []designUnitRef{
		{UnitID: "ingenieria_ia", TaskID: 10, InvocationID: 100, ResultHash: "aaa"},
	}}
	return o.reviewBundle(context.Background(), root, designfreeze.Design{ID: "design:root:7", Version: "v1", Digest: strings.Repeat("a", 64)}, artifact)
}

// The reviewer must never be handed a requirement only the built change could
// satisfy. Asking it to verify host gates before any gate exists is asking it
// to verify the future, and it refused -- correctly, and on every campaign
// that got this far.
func TestTheBundleCarriesOnlyDesignPhaseRequirements(t *testing.T) {
	encoded, err := bundleFixture(t)
	if err != nil {
		t.Fatal(err)
	}
	var bundle struct {
		OwnerRequirements       []string `json:"owner_requirements"`
		CandidateDesign         string   `json:"candidate_design"`
		ArchitectureConstraints []string `json:"architecture_constraints"`
	}
	if err := json.Unmarshal(encoded, &bundle); err != nil {
		t.Fatal(err)
	}
	if len(bundle.OwnerRequirements) != 1 || bundle.OwnerRequirements[0] != "the design names the exact file it will create" {
		t.Fatalf("only the design-phase criterion belongs here, got %v", bundle.OwnerRequirements)
	}
	// And the deferred ones appear nowhere in the encoded bundle at all.
	for _, leaked := range []string{"the host gates pass", "governed flow"} {
		if strings.Contains(string(encoded), leaked) {
			t.Errorf("a deferred criterion leaked into the reviewer's bundle: %q", leaked)
		}
	}
}

// The general form of the bug we shipped three times: a requirement the
// bundle deliberately cannot answer.
//
// This cannot check semantic sufficiency -- nothing can prove a reviewer will
// understand a sentence. What it does check is the structural half we got
// wrong: a bundle that carries design-phase requirements must also carry a
// readable design, and must not claim to withhold one.
func TestADesignRequirementIsNeverPairedWithABundleThatWithholdsTheDesign(t *testing.T) {
	encoded, err := bundleFixture(t)
	if err != nil {
		t.Fatal(err)
	}
	var bundle struct {
		OwnerRequirements       []string `json:"owner_requirements"`
		CandidateDesign         string   `json:"candidate_design"`
		ArchitectureConstraints []string `json:"architecture_constraints"`
	}
	if err := json.Unmarshal(encoded, &bundle); err != nil {
		t.Fatal(err)
	}
	if len(bundle.OwnerRequirements) == 0 {
		t.Fatal("a review with no applicable requirement is as useless as one with unsatisfiable requirements")
	}
	if !strings.Contains(bundle.CandidateDesign, "The design creates docs/x.md") {
		t.Fatalf("design-phase requirements demand a readable design: %q", bundle.CandidateDesign)
	}
	for _, constraint := range bundle.ArchitectureConstraints {
		if strings.Contains(constraint, "only durable deliverable identities") {
			t.Error("the bundle must not tell the reviewer it withholds the very thing its requirements are about")
		}
	}
}

// A root submitted before phase ownership cannot be judged, and guessing its
// phases would be the classifier this whole type exists to avoid.
func TestARootWithNoRecordedPhasesIsRefusedRatherThanGuessed(t *testing.T) {
	models := &bodyModels{results: map[int64]InvocationResult{
		100: {InvocationID: 100, TextOutput: "body", ResponseHash: "aaa"},
	}}
	o := &Orchestrator{models: models, acceptance: newMemoryAcceptance(), limits: Limits{MaxStringBytes: 4096}}
	root := TaskRecord{ID: 7, AcceptanceCriteria: []string{"legacy criterion"}}
	artifact := designArtifact{RootTaskID: 7, Round: 1, Units: []designUnitRef{
		{UnitID: "ingenieria_ia", TaskID: 10, InvocationID: 100, ResultHash: "aaa"},
	}}
	_, err := o.reviewBundle(context.Background(), root, designfreeze.Design{ID: "design:root:7", Version: "v1", Digest: strings.Repeat("a", 64)}, artifact)
	if err == nil {
		t.Fatal("a root with no recorded phases must not be judged on guessed ones")
	}
	if !strings.Contains(err.Error(), "must be resubmitted") {
		t.Fatalf("the refusal must say what to do about it: %v", err)
	}
}

func TestRecordingAcceptanceIsIdempotent(t *testing.T) {
	acceptance := newMemoryAcceptance()
	ctx := context.Background()
	if err := acceptance.RecordAcceptance(ctx, 7, mixedPhaseCriteria()); err != nil {
		t.Fatal(err)
	}
	// A resumed submit must find the first statement, not replace it.
	if err := acceptance.RecordAcceptance(ctx, 7, []AcceptanceCriterion{{Text: "different", Phase: AcceptancePromotion}}); err != nil {
		t.Fatal(err)
	}
	recorded, err := acceptance.Acceptance(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 3 || recorded[0].Text != "the design names the exact file it will create" {
		t.Fatalf("the owner's original statement must stand: %v", recorded)
	}
}
