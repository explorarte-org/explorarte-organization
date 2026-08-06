package improvement

import (
	"context"
	"errors"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/evaluation"
)

// Service orchestrates the candidate lifecycle. It holds no persistence:
// every method takes the current Candidate value and returns the next one,
// leaving storage to the composition root added when Branch 14 merges.
type Service struct {
	gate  ApprovalGate
	clock Clock
}

func NewService(gate ApprovalGate, clock Clock) (*Service, error) {
	if gate == nil {
		return nil, errors.New("improvement service requires an approval gate")
	}
	if clock == nil {
		clock = SystemClock{}
	}
	return &Service{gate: gate, clock: clock}, nil
}

// ProposeCandidate creates a new candidate in the proposed state.
func (s *Service) ProposeCandidate(id string, artifact ArtifactRef, lineage Lineage) (Candidate, error) {
	if id == "" {
		return Candidate{}, fmt.Errorf("%w: candidate id is required", ErrInvalidCandidate)
	}
	if err := artifact.Validate(); err != nil {
		return Candidate{}, err
	}
	if err := lineage.Validate(); err != nil {
		return Candidate{}, err
	}
	now := s.clock.Now().UTC()
	candidate := Candidate{
		ID:         id,
		Artifact:   artifact,
		Lineage:    lineage,
		State:      StateProposed,
		ProposedAt: now,
		UpdatedAt:  now,
	}
	if err := candidate.Validate(); err != nil {
		return Candidate{}, err
	}
	return candidate, nil
}

// transition validates and applies an ungated state change, then validates
// the result. The one exception is a transition to rolled_back: RollBack
// must set RollbackTarget after this returns, and Candidate.Validate
// requires that field to be set whenever State is rolled_back, so RollBack
// performs the post-mutation validation itself once the target is attached.
func (s *Service) transition(candidate Candidate, to CandidateState) (Candidate, error) {
	if err := candidate.Validate(); err != nil {
		return Candidate{}, err
	}
	if err := ValidateCandidateTransition(candidate.State, to); err != nil {
		return Candidate{}, err
	}
	candidate.State = to
	candidate.UpdatedAt = s.clock.Now().UTC()
	if to != StateRolledBack {
		if err := candidate.Validate(); err != nil {
			return Candidate{}, err
		}
	}
	return candidate, nil
}

// ValidateCandidate moves a proposed candidate to validated.
func (s *Service) ValidateCandidate(candidate Candidate) (Candidate, error) {
	return s.transition(candidate, StateValidated)
}

// BeginEvaluation moves a validated candidate to evaluating.
func (s *Service) BeginEvaluation(candidate Candidate) (Candidate, error) {
	return s.transition(candidate, StateEvaluating)
}

// RecordEvaluationVerdict moves an evaluating candidate to approved,
// rejected or inconclusive, deterministically, from the suite comparison's
// overall verdict.
func (s *Service) RecordEvaluationVerdict(candidate Candidate, comparison evaluation.SuiteComparisonResult) (Candidate, error) {
	if err := comparison.Validate(); err != nil {
		return Candidate{}, err
	}
	var to CandidateState
	switch comparison.OverallVerdict {
	case evaluation.VerdictPass:
		to = StateApproved
	case evaluation.VerdictFail:
		to = StateRejected
	case evaluation.VerdictInconclusive:
		to = StateInconclusive
	default:
		return Candidate{}, fmt.Errorf("%w: candidate %s: invalid comparison verdict %q", ErrInvalidCandidate, candidate.ID, comparison.OverallVerdict)
	}
	return s.transition(candidate, to)
}

// requestPromotion asks the ApprovalGate to authorize a forward promotion
// step and, only on an authorized decision, applies the transition.
func (s *Service) requestPromotion(ctx context.Context, candidate Candidate, to CandidateState, kind PromotionKind, requestedBy string, comparison evaluation.SuiteComparisonResult) (Candidate, PromotionDecision, error) {
	if err := candidate.Validate(); err != nil {
		return Candidate{}, PromotionDecision{}, err
	}
	if err := ValidateCandidateTransition(candidate.State, to); err != nil {
		return Candidate{}, PromotionDecision{}, err
	}
	hash, err := candidate.CanonicalHash()
	if err != nil {
		return Candidate{}, PromotionDecision{}, fmt.Errorf("hash candidate %s: %w", candidate.ID, err)
	}
	request := PromotionRequest{
		CandidateID:   candidate.ID,
		CandidateHash: hash,
		FromState:     candidate.State,
		ToState:       to,
		Kind:          kind,
		Evaluation:    comparison,
		RequestedAt:   s.clock.Now().UTC(),
		RequestedBy:   requestedBy,
	}
	if err := request.Validate(); err != nil {
		return Candidate{}, PromotionDecision{}, err
	}
	decision, err := s.gate.AuthorizePromotion(ctx, request)
	if err != nil {
		return Candidate{}, PromotionDecision{}, fmt.Errorf("authorize promotion for candidate %s: %w", candidate.ID, err)
	}
	if err := decision.Validate(); err != nil {
		return Candidate{}, PromotionDecision{}, err
	}
	if decision.CandidateID != candidate.ID || decision.Kind != kind {
		return Candidate{}, decision, fmt.Errorf("%w: decision does not match request for candidate %s", ErrInvalidPromotionDecision, candidate.ID)
	}
	if decision.Outcome != PromotionAuthorized {
		return candidate, decision, fmt.Errorf("%w: candidate %s: %s", ErrPromotionDenied, candidate.ID, decision.Reason)
	}
	updated, err := s.transition(candidate, to)
	if err != nil {
		return Candidate{}, decision, err
	}
	return updated, decision, nil
}

// PromoteToCanary asks the gate to authorize approved -> canary.
func (s *Service) PromoteToCanary(ctx context.Context, candidate Candidate, requestedBy string, comparison evaluation.SuiteComparisonResult) (Candidate, PromotionDecision, error) {
	return s.requestPromotion(ctx, candidate, StateCanary, PromotionToCanary, requestedBy, comparison)
}

// PromoteToActive asks the gate to authorize canary -> active.
func (s *Service) PromoteToActive(ctx context.Context, candidate Candidate, requestedBy string, comparison evaluation.SuiteComparisonResult) (Candidate, PromotionDecision, error) {
	return s.requestPromotion(ctx, candidate, StateActive, PromotionToActive, requestedBy, comparison)
}

// Deprecate retires an active candidate without a gate: winding down is
// never blocked by an approval step.
func (s *Service) Deprecate(candidate Candidate) (Candidate, error) {
	return s.transition(candidate, StateDeprecated)
}

// RollBack reverts a canary or active candidate. Like Deprecate, rollback is
// a safety action and is never blocked by an approval gate.
func (s *Service) RollBack(candidate Candidate, target RollbackTarget) (Candidate, error) {
	if err := target.Validate(); err != nil {
		return Candidate{}, err
	}
	if target.FromState != candidate.State {
		return Candidate{}, fmt.Errorf("%w: rollback target from_state %q does not match candidate state %q", ErrInvalidRollbackTarget, target.FromState, candidate.State)
	}
	updated, err := s.transition(candidate, StateRolledBack)
	if err != nil {
		return Candidate{}, err
	}
	updated.RollbackTarget = &target
	if err := updated.Validate(); err != nil {
		return Candidate{}, err
	}
	return updated, nil
}
