package executive

import (
	"context"
	"testing"
)

type fakeIncorporation struct{ status string }

func (f fakeIncorporation) MissionPromotionStatus(context.Context, int64) (string, error) {
	return f.status, nil
}

// External audit A6: a completed root's state did not distinguish a verified candidate from an accepted
// or an applied change. The run and the closure record now say how far the change has come.
func TestTheRunSaysHowFarItsChangeHasCome(t *testing.T) {
	for status, want := range map[string]IncorporationState{
		"":               IncorporationCandidateVerified,
		"awaiting_gates": IncorporationPendingOwnerReview,
		"approved":       IncorporationAccepted,
		"applied":        IncorporationApplied,
		"rejected":       IncorporationRejected,
	} {
		o, root := reviewFixture(codeRunnerTaskForTest(), nil)
		WithMissionIncorporationReader(fakeIncorporation{status: status})(o)
		got, err := o.runIncorporation(context.Background(), root)
		if err != nil || got != want {
			t.Errorf("promotion %q: incorporation %q (%v), want %q", status, got, err, want)
		}
	}
	if _, err := incorporationFromPromotion("teleported"); err == nil {
		t.Error("an unknown promotion status was mapped")
	}
}

func TestARunWithoutAMissionHasNoIncorporation(t *testing.T) {
	o := &Orchestrator{tasks: newMemoryTasks()}
	WithMissionIncorporationReader(fakeIncorporation{status: "applied"})(o)
	if got, err := o.runIncorporation(context.Background(), TaskRecord{ID: 1}); err != nil || got != "" {
		t.Fatalf("a root without a code-runner requirement reported %q (%v)", got, err)
	}
}

// The closure is handed the same state, so it cannot report a verified candidate as a change made.
func TestTheClosureRecordCarriesTheIncorporation(t *testing.T) {
	o, root := reviewFixture(codeRunnerTaskForTest(), nil)
	WithMissionIncorporationReader(fakeIncorporation{status: "awaiting_gates"})(o)
	execution, err := o.closureEngineeringExecution(context.Background(), root)
	if err != nil || execution == nil {
		t.Fatalf("closure execution: %+v, %v", execution, err)
	}
	if execution.Incorporation != IncorporationPendingOwnerReview {
		t.Fatalf("closure incorporation %q, want %q", execution.Incorporation, IncorporationPendingOwnerReview)
	}
}
