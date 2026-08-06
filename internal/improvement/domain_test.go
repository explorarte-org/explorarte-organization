package improvement

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/evaluation"
)

func validArtifact(id string) ArtifactRef {
	return ArtifactRef{ArtifactID: id, ContentHash: "hash-" + id, SchemaVersion: "test.v1"}
}

func passingComparison() evaluation.SuiteComparisonResult {
	return evaluation.SuiteComparisonResult{SuiteID: "suite-1", OverallVerdict: evaluation.VerdictPass}
}

func TestArtifactRefValidate(t *testing.T) {
	cases := []ArtifactRef{
		{ArtifactID: "", ContentHash: "h", SchemaVersion: "v1"},
		{ArtifactID: "a", ContentHash: "", SchemaVersion: "v1"},
		{ArtifactID: "a", ContentHash: "h", SchemaVersion: ""},
	}
	for i, c := range cases {
		if err := c.Validate(); !errors.Is(err, ErrInvalidArtifactRef) {
			t.Fatalf("case %d: expected ErrInvalidArtifactRef, got %v", i, err)
		}
	}
}

func TestCandidateValidateRolledBackRequiresTarget(t *testing.T) {
	now := time.Now().UTC()
	c := Candidate{
		ID: "cand-nil-target", Artifact: validArtifact("art-1"), Lineage: Lineage{},
		State: StateRolledBack, ProposedAt: now, UpdatedAt: now, RollbackTarget: nil,
	}
	if err := c.Validate(); !errors.Is(err, ErrInvalidCandidate) {
		t.Fatalf("expected ErrInvalidCandidate for rolled_back without a target, got %v", err)
	}
	c.RollbackTarget = &RollbackTarget{CandidateID: "prev", ArtifactHash: "h", FromState: StateActive}
	if err := c.Validate(); err != nil {
		t.Fatalf("expected valid candidate once rollback target is set, got %v", err)
	}
}

func TestLineageValidate(t *testing.T) {
	if err := (Lineage{}).Validate(); err != nil {
		t.Fatalf("empty root lineage should be valid: %v", err)
	}
	dangling := Lineage{ParentCandidateID: "p1"}
	if err := dangling.Validate(); !errors.Is(err, ErrInvalidLineage) {
		t.Fatalf("expected ErrInvalidLineage for dangling parent id, got %v", err)
	}
	missingDerivedFrom := Lineage{ParentCandidateID: "p1", ParentArtifactHash: "h1"}
	if err := missingDerivedFrom.Validate(); !errors.Is(err, ErrInvalidLineage) {
		t.Fatalf("expected ErrInvalidLineage for missing derived_from, got %v", err)
	}
	full := Lineage{ParentCandidateID: "p1", ParentArtifactHash: "h1", DerivedFrom: "manual"}
	if err := full.Validate(); err != nil {
		t.Fatalf("expected valid lineage, got %v", err)
	}
	if full.IsRoot() {
		t.Fatalf("lineage with a parent must not report IsRoot")
	}
}

// TestCandidateTransitionMatrixIsDefaultDeny exhaustively checks every pair
// of states: only the transitions explicitly wired in candidateTransitions
// may succeed. This is the guarantee that proposed -> active (and every
// other unlisted pair) can never happen.
func TestCandidateTransitionMatrixIsDefaultDeny(t *testing.T) {
	allStates := []CandidateState{
		StateProposed, StateValidated, StateEvaluating, StateRejected, StateInconclusive,
		StateApproved, StateCanary, StateActive, StateDeprecated, StateRolledBack,
	}
	allowedCount := 0
	for _, from := range allStates {
		for _, to := range allStates {
			_, wantOK := candidateTransitions[from][to]
			err := ValidateCandidateTransition(from, to)
			gotOK := err == nil
			if gotOK != wantOK {
				t.Errorf("transition %s -> %s: allowed=%v, want %v (err=%v)", from, to, gotOK, wantOK, err)
			}
			if gotOK {
				allowedCount++
			}
		}
	}
	if allowedCount == 0 {
		t.Fatal("expected at least one allowed transition")
	}
	if err := ValidateCandidateTransition(StateProposed, StateActive); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("proposed -> active must be impossible, got %v", err)
	}
}

func TestCandidateCanonicalHashDeterministic(t *testing.T) {
	c1 := Candidate{ID: "a", Artifact: validArtifact("art-1"), Lineage: Lineage{}, State: StateProposed, ProposedAt: time.Now(), UpdatedAt: time.Now()}
	c2 := c1
	c2.ID = "b" // different ID
	c2.State = StateValidated
	c2.UpdatedAt = c2.UpdatedAt.Add(time.Hour)

	h1, err := c1.CanonicalHash()
	if err != nil {
		t.Fatalf("hash c1: %v", err)
	}
	h2, err := c2.CanonicalHash()
	if err != nil {
		t.Fatalf("hash c2: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("hash should be independent of id/state/timestamps: %s != %s", h1, h2)
	}

	c3 := c1
	c3.Artifact = validArtifact("art-2")
	h3, err := c3.CanonicalHash()
	if err != nil {
		t.Fatalf("hash c3: %v", err)
	}
	if h3 == h1 {
		t.Fatalf("hash should change when the artifact changes")
	}
}

func newService(t *testing.T, gate ApprovalGate) *Service {
	t.Helper()
	svc, err := NewService(gate, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func TestServiceFullLifecycleToActiveAndDeprecate(t *testing.T) {
	ctx := context.Background()
	gate := NewFakeApprovalGate()
	svc := newService(t, gate)

	c, err := svc.ProposeCandidate("cand-1", validArtifact("art-1"), Lineage{})
	if err != nil {
		t.Fatalf("ProposeCandidate: %v", err)
	}
	if c.State != StateProposed {
		t.Fatalf("expected proposed, got %s", c.State)
	}

	c, err = svc.ValidateCandidate(c)
	if err != nil || c.State != StateValidated {
		t.Fatalf("ValidateCandidate: state=%s err=%v", c.State, err)
	}

	c, err = svc.BeginEvaluation(c)
	if err != nil || c.State != StateEvaluating {
		t.Fatalf("BeginEvaluation: state=%s err=%v", c.State, err)
	}

	c, err = svc.RecordEvaluationVerdict(c, passingComparison())
	if err != nil || c.State != StateApproved {
		t.Fatalf("RecordEvaluationVerdict: state=%s err=%v", c.State, err)
	}

	c, decision, err := svc.PromoteToCanary(ctx, c, "owner", passingComparison())
	if err != nil || c.State != StateCanary || decision.Outcome != PromotionAuthorized {
		t.Fatalf("PromoteToCanary: state=%s decision=%+v err=%v", c.State, decision, err)
	}

	c, decision, err = svc.PromoteToActive(ctx, c, "owner", passingComparison())
	if err != nil || c.State != StateActive || decision.Outcome != PromotionAuthorized {
		t.Fatalf("PromoteToActive: state=%s decision=%+v err=%v", c.State, decision, err)
	}

	c, err = svc.Deprecate(c)
	if err != nil || c.State != StateDeprecated {
		t.Fatalf("Deprecate: state=%s err=%v", c.State, err)
	}
}

func TestServiceRecordEvaluationVerdictBranches(t *testing.T) {
	table := []struct {
		verdict evaluation.Verdict
		want    CandidateState
	}{
		{evaluation.VerdictPass, StateApproved},
		{evaluation.VerdictFail, StateRejected},
		{evaluation.VerdictInconclusive, StateInconclusive},
	}
	for _, tc := range table {
		gate := NewFakeApprovalGate()
		svc := newService(t, gate)
		c, err := svc.ProposeCandidate("cand-x", validArtifact("art-x"), Lineage{})
		if err != nil {
			t.Fatalf("ProposeCandidate: %v", err)
		}
		c, _ = svc.ValidateCandidate(c)
		c, _ = svc.BeginEvaluation(c)
		c, err = svc.RecordEvaluationVerdict(c, evaluation.SuiteComparisonResult{SuiteID: "s", OverallVerdict: tc.verdict})
		if err != nil {
			t.Fatalf("RecordEvaluationVerdict(%s): %v", tc.verdict, err)
		}
		if c.State != tc.want {
			t.Fatalf("verdict %s: expected state %s, got %s", tc.verdict, tc.want, c.State)
		}
	}
}

func TestServicePromotionDeniedKeepsState(t *testing.T) {
	ctx := context.Background()
	gate := NewFakeApprovalGate()
	gate.SetDecide(func(req PromotionRequest) (PromotionDecision, error) {
		return PromotionDecision{
			CandidateID: req.CandidateID, Kind: req.Kind, Outcome: PromotionDenied,
			Reason: "manual hold", DecidedAt: time.Now().UTC(), DecidedBy: "reviewer",
		}, nil
	})
	svc := newService(t, gate)

	c, _ := svc.ProposeCandidate("cand-2", validArtifact("art-2"), Lineage{})
	c, _ = svc.ValidateCandidate(c)
	c, _ = svc.BeginEvaluation(c)
	c, err := svc.RecordEvaluationVerdict(c, passingComparison())
	if err != nil {
		t.Fatalf("RecordEvaluationVerdict: %v", err)
	}

	before := c
	c, decision, err := svc.PromoteToCanary(ctx, c, "owner", passingComparison())
	if !errors.Is(err, ErrPromotionDenied) {
		t.Fatalf("expected ErrPromotionDenied, got %v", err)
	}
	if decision.Outcome != PromotionDenied {
		t.Fatalf("expected denied decision, got %+v", decision)
	}
	if c.State != before.State {
		t.Fatalf("candidate state must be unchanged on denial: before=%s after=%s", before.State, c.State)
	}
}

func TestServiceRollBackFromCanaryAndActive(t *testing.T) {
	ctx := context.Background()

	t.Run("from canary", func(t *testing.T) {
		gate := NewFakeApprovalGate()
		svc := newService(t, gate)
		c, _ := svc.ProposeCandidate("cand-3", validArtifact("art-3"), Lineage{})
		c, _ = svc.ValidateCandidate(c)
		c, _ = svc.BeginEvaluation(c)
		c, _ = svc.RecordEvaluationVerdict(c, passingComparison())
		c, _, err := svc.PromoteToCanary(ctx, c, "owner", passingComparison())
		if err != nil {
			t.Fatalf("PromoteToCanary: %v", err)
		}
		target := RollbackTarget{CandidateID: "previous-active", ArtifactHash: "prev-hash", FromState: StateCanary}
		c, err = svc.RollBack(c, target)
		if err != nil {
			t.Fatalf("RollBack: %v", err)
		}
		if c.State != StateRolledBack || c.RollbackTarget == nil || c.RollbackTarget.CandidateID != "previous-active" {
			t.Fatalf("unexpected rollback result: %+v", c)
		}
	})

	t.Run("mismatched from_state rejected", func(t *testing.T) {
		gate := NewFakeApprovalGate()
		svc := newService(t, gate)
		c, _ := svc.ProposeCandidate("cand-4", validArtifact("art-4"), Lineage{})
		c, _ = svc.ValidateCandidate(c)
		c, _ = svc.BeginEvaluation(c)
		c, _ = svc.RecordEvaluationVerdict(c, passingComparison())
		// candidate is "approved", not canary/active
		target := RollbackTarget{CandidateID: "x", ArtifactHash: "h", FromState: StateCanary}
		if _, err := svc.RollBack(c, target); !errors.Is(err, ErrInvalidRollbackTarget) {
			t.Fatalf("expected ErrInvalidRollbackTarget, got %v", err)
		}
	})
}

func TestServiceConcurrentPromotionRace(t *testing.T) {
	ctx := context.Background()
	gate := NewFakeApprovalGate()
	svc := newService(t, gate)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c, err := svc.ProposeCandidate("cand-race", validArtifact("art-race"), Lineage{})
			if err != nil {
				t.Errorf("ProposeCandidate: %v", err)
				return
			}
			c, err = svc.ValidateCandidate(c)
			if err != nil {
				t.Errorf("ValidateCandidate: %v", err)
				return
			}
			c, err = svc.BeginEvaluation(c)
			if err != nil {
				t.Errorf("BeginEvaluation: %v", err)
				return
			}
			c, err = svc.RecordEvaluationVerdict(c, passingComparison())
			if err != nil {
				t.Errorf("RecordEvaluationVerdict: %v", err)
				return
			}
			if _, _, err := svc.PromoteToCanary(ctx, c, "owner", passingComparison()); err != nil {
				t.Errorf("PromoteToCanary: %v", err)
			}
		}(i)
	}
	wg.Wait()
}
