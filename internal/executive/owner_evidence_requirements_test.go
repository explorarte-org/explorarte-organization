package executive

import (
	"context"
	"errors"
	"testing"
)

// The owner may impose the topology directly, so round 1 can be bound without
// waiting for an adjudicator to reject a design first. Requiring a revise to
// activate the mechanism would make a correct decision by the adjudicator a technical
// precondition of the system.
func TestAnOwnerCanBindTheFirstRound(t *testing.T) {
	fixture := newSubmitFixture(t)
	goal := fixture.goal()
	goal.EvidenceRequirements = []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"application", "definition"}},
		{Subject: "MaxDepartmentReplans", Relations: []string{"definition", "application"}},
	}
	run, _, err := fixture.submit(goal)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := fixture.orchestrator.evidenceRequirementsForRound(context.Background(), run.RootTaskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("round 1 opened with %d obligations, want 2", len(loaded))
	}
	// Canonical, and attributed to the only authority that could have set it.
	if loaded[0].Subject != "MaxDepartmentReplans" || loaded[1].Subject != "MaxDesignRounds" {
		t.Fatalf("obligations are not canonically ordered: %+v", loaded)
	}
	for _, requirement := range loaded {
		if requirement.Source != EvidenceFromOwnerAcceptance {
			t.Fatalf("%s was attributed to %q", requirement.Subject, requirement.Source)
		}
		if len(requirement.Relations) != 2 || requirement.Relations[0] != "application" {
			t.Fatalf("%s relations are not canonical: %v", requirement.Subject, requirement.Relations)
		}
	}
}

// A goal that names none imposes none. Not every campaign is an investigation
// of the repository.
func TestAGoalWithNoRequirementsImposesNone(t *testing.T) {
	fixture := newSubmitFixture(t)
	run, _, err := fixture.submit(fixture.goal())
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := fixture.orchestrator.evidenceRequirementsForRound(context.Background(), run.RootTaskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 0 {
		t.Fatalf("a plain goal was turned into a repository investigation: %+v", loaded)
	}
}

// The host validates the owner's vocabulary too. Authority to impose is not
// authority to invent.
func TestAnOwnerCannotInventRelations(t *testing.T) {
	fixture := newSubmitFixture(t)
	goal := fixture.goal()
	goal.EvidenceRequirements = []EvidenceRequirementProposal{{Subject: "X", Relations: []string{"vibes"}}}
	if _, _, err := fixture.submit(goal); !errors.Is(err, ErrContractRejected) {
		t.Fatalf("an owner bound a round to a vocabulary of its own: %v", err)
	}
}

// After acceptance the durable record is the truth, and reading it twice must
// not double it.
func TestReloadingObligationsDoesNotDuplicateThem(t *testing.T) {
	fixture := newSubmitFixture(t)
	goal := fixture.goal()
	goal.EvidenceRequirements = []EvidenceRequirementProposal{{Subject: "MaxDesignRounds", Relations: []string{"definition"}}}
	run, _, err := fixture.submit(goal)
	if err != nil {
		t.Fatal(err)
	}
	// A resumed submit re-runs the same adoption.
	if _, _, err = fixture.submit(goal); err != nil {
		t.Fatal(err)
	}
	loaded, err := fixture.orchestrator.evidenceRequirementsForRound(context.Background(), run.RootTaskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || len(loaded[0].Relations) != 1 {
		t.Fatalf("a resumed submit duplicated the obligation: %+v", loaded)
	}
}

// Obligations accumulate across rounds, and authority stays at the SLOT.
//
// If the owner demanded MaxDesignRounds/definition and a later adjudication
// demanded MaxDesignRounds/application, unioning the relations under one
// source would make the adjudicator's obligation look like the owner's -- and
// "who may change this" is decided by exactly that answer.
func TestALaterRoundInheritsEarlierObligations(t *testing.T) {
	fixture := newSubmitFixture(t)
	goal := fixture.goal()
	goal.EvidenceRequirements = []EvidenceRequirementProposal{{Subject: "MaxDesignRounds", Relations: []string{"definition"}}}
	run, _, err := fixture.submit(goal)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = fixture.orchestrator.recordEvidenceRequirements(ctx, run.RootTaskID, 2,
		AdoptEvidenceRequirements([]EvidenceRequirementProposal{
			{Subject: "MaxDesignRounds", Relations: []string{"application"}},
			{Subject: "MaxDepartmentReplans", Relations: []string{"definition"}},
		}, EvidenceFromAdjudication)); err != nil {
		t.Fatal(err)
	}
	loaded, err := fixture.orchestrator.evidenceRequirementsForRound(ctx, run.RootTaskID, 2)
	if err != nil {
		t.Fatal(err)
	}

	// Every slot in force, with the authority that created it.
	sourceOf := map[string]EvidenceRequirementSource{}
	for _, requirement := range loaded {
		for _, relation := range requirement.Relations {
			slot := requirement.Subject + "/" + relation
			if existing, duplicated := sourceOf[slot]; duplicated {
				t.Fatalf("%s is claimed by both %q and %q", slot, existing, requirement.Source)
			}
			sourceOf[slot] = requirement.Source
		}
	}
	for slot, want := range map[string]EvidenceRequirementSource{
		"MaxDesignRounds/definition":      EvidenceFromOwnerAcceptance,
		"MaxDesignRounds/application":     EvidenceFromAdjudication,
		"MaxDepartmentReplans/definition": EvidenceFromAdjudication,
	} {
		got, present := sourceOf[slot]
		if !present {
			t.Fatalf("%s is not in force in round 2", slot)
		}
		if got != want {
			t.Fatalf("%s is attributed to %q, want %q", slot, got, want)
		}
	}

	// Round 1 still sees only what round 1 was bound by.
	first, err := fixture.orchestrator.evidenceRequirementsForRound(ctx, run.RootTaskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Subject != "MaxDesignRounds" ||
		len(first[0].Relations) != 1 || first[0].Relations[0] != "definition" {
		t.Fatalf("a later obligation reached back into an earlier round: %+v", first)
	}
}

// A submit fixture with only what binding a round needs: somewhere to put the
// root, and somewhere to record what the round must ground.
type submitFixture struct {
	orchestrator *Orchestrator
}

func newSubmitFixture(t *testing.T) submitFixture {
	t.Helper()
	return submitFixture{orchestrator: &Orchestrator{
		tasks: newMemoryTasks(), acceptance: newMemoryAcceptance(), limits: DefaultLimits(),
	}}
}

func (f submitFixture) goal() OwnerGoal {
	return OwnerGoal{
		Goal:               "Diagnose how the two limits are governed.",
		AcceptanceCriteria: []AcceptanceCriterion{{Text: "Both limits stay independent", Phase: AcceptanceDesign}},
	}
}

func (f submitFixture) submit(goal OwnerGoal) (Run, bool, error) {
	return f.orchestrator.Submit(context.Background(), SubmitRequest{
		ActorRoleID: OwnerRoleID, IdempotencyKey: "owner-evidence-requirements", Goal: goal,
	})
}
