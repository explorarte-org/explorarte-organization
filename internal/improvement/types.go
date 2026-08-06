package improvement

import (
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/evaluation"
)

// ArtifactRef is an immutable, content-addressed reference to the artifact a
// candidate proposes (a model config, prompt bundle, policy or code diff).
// This package treats the artifact bytes themselves as opaque.
type ArtifactRef struct {
	ArtifactID    string
	ContentHash   string
	SchemaVersion string
}

func (a ArtifactRef) Validate() error {
	if a.ArtifactID == "" {
		return fmt.Errorf("%w: artifact id is required", ErrInvalidArtifactRef)
	}
	if a.ContentHash == "" {
		return fmt.Errorf("%w: content hash is required", ErrInvalidArtifactRef)
	}
	if a.SchemaVersion == "" {
		return fmt.Errorf("%w: schema version is required", ErrInvalidArtifactRef)
	}
	return nil
}

// Lineage records what a candidate was derived from, if anything. A root
// candidate has no parent.
type Lineage struct {
	ParentCandidateID  string
	ParentArtifactHash string
	DerivedFrom        string
}

func (l Lineage) Validate() error {
	if (l.ParentCandidateID == "") != (l.ParentArtifactHash == "") {
		return fmt.Errorf("%w: parent candidate id and parent artifact hash must be set together", ErrInvalidLineage)
	}
	if l.ParentCandidateID != "" && l.DerivedFrom == "" {
		return fmt.Errorf("%w: derived_from is required when a parent is set", ErrInvalidLineage)
	}
	return nil
}

func (l Lineage) IsRoot() bool { return l.ParentCandidateID == "" }

// CandidateState is a step in the bounded self-improvement promotion state
// machine.
type CandidateState string

const (
	StateProposed     CandidateState = "proposed"
	StateValidated    CandidateState = "validated"
	StateEvaluating   CandidateState = "evaluating"
	StateRejected     CandidateState = "rejected"
	StateInconclusive CandidateState = "inconclusive"
	StateApproved     CandidateState = "approved"
	StateCanary       CandidateState = "canary"
	StateActive       CandidateState = "active"
	StateDeprecated   CandidateState = "deprecated"
	StateRolledBack   CandidateState = "rolled_back"
)

func (s CandidateState) Valid() bool {
	switch s {
	case StateProposed, StateValidated, StateEvaluating, StateRejected, StateInconclusive,
		StateApproved, StateCanary, StateActive, StateDeprecated, StateRolledBack:
		return true
	default:
		return false
	}
}

// Terminal reports whether no further transition is ever allowed out of this
// state.
func (s CandidateState) Terminal() bool {
	switch s {
	case StateRejected, StateInconclusive, StateDeprecated, StateRolledBack:
		return true
	default:
		return false
	}
}

// RollbackTarget is recorded on a candidate the moment it is rolled back: it
// pins exactly which state it was rolled back from, for audit purposes. The
// candidate it should be rolled back *to* is resolved via Lineage by the
// caller; this package does not track a live "currently active" pointer.
type RollbackTarget struct {
	CandidateID  string
	ArtifactHash string
	FromState    CandidateState
}

func (r RollbackTarget) Validate() error {
	if r.CandidateID == "" {
		return fmt.Errorf("%w: candidate id is required", ErrInvalidRollbackTarget)
	}
	if r.ArtifactHash == "" {
		return fmt.Errorf("%w: artifact hash is required", ErrInvalidRollbackTarget)
	}
	if r.FromState != StateCanary && r.FromState != StateActive {
		return fmt.Errorf("%w: rollback is only valid from canary or active, got %q", ErrInvalidRollbackTarget, r.FromState)
	}
	return nil
}

// Candidate is a single proposed self-improvement, tracked through its full
// lifecycle. Persistence (PostgreSQL) is added later; this package operates
// on Candidate values directly.
type Candidate struct {
	ID             string
	Artifact       ArtifactRef
	Lineage        Lineage
	State          CandidateState
	ProposedAt     time.Time
	UpdatedAt      time.Time
	RollbackTarget *RollbackTarget
}

func (c Candidate) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("%w: candidate id is required", ErrInvalidCandidate)
	}
	if err := c.Artifact.Validate(); err != nil {
		return fmt.Errorf("%w: candidate %s: %v", ErrInvalidCandidate, c.ID, err)
	}
	if err := c.Lineage.Validate(); err != nil {
		return fmt.Errorf("%w: candidate %s: %v", ErrInvalidCandidate, c.ID, err)
	}
	if !c.State.Valid() {
		return fmt.Errorf("%w: candidate %s: invalid state %q", ErrInvalidCandidate, c.ID, c.State)
	}
	if c.ProposedAt.IsZero() {
		return fmt.Errorf("%w: candidate %s: proposed_at is required", ErrInvalidCandidate, c.ID)
	}
	if c.UpdatedAt.IsZero() || c.UpdatedAt.Before(c.ProposedAt) {
		return fmt.Errorf("%w: candidate %s: updated_at must be at or after proposed_at", ErrInvalidCandidate, c.ID)
	}
	if c.RollbackTarget != nil {
		if c.State != StateRolledBack {
			return fmt.Errorf("%w: candidate %s: rollback target set without rolled_back state", ErrInvalidCandidate, c.ID)
		}
		if err := c.RollbackTarget.Validate(); err != nil {
			return fmt.Errorf("%w: candidate %s: %v", ErrInvalidCandidate, c.ID, err)
		}
	}
	if c.State == StateRolledBack && c.RollbackTarget == nil {
		return fmt.Errorf("%w: candidate %s: rolled_back state requires a rollback target", ErrInvalidCandidate, c.ID)
	}
	return nil
}

// PromotionKind identifies which forward promotion step a PromotionRequest
// is asking the ApprovalGate to authorize.
type PromotionKind string

const (
	PromotionToCanary PromotionKind = "to_canary"
	PromotionToActive PromotionKind = "to_active"
)

func (k PromotionKind) Valid() bool {
	switch k {
	case PromotionToCanary, PromotionToActive:
		return true
	default:
		return false
	}
}

// expectedTransition returns the (from, to) states a PromotionKind must
// match.
func (k PromotionKind) expectedTransition() (from, to CandidateState) {
	switch k {
	case PromotionToCanary:
		return StateApproved, StateCanary
	case PromotionToActive:
		return StateCanary, StateActive
	default:
		return "", ""
	}
}

// PromotionRequest is what an ApprovalGate evaluates to decide whether a
// candidate may advance to the next promotion state. CandidateHash is the
// candidate's CanonicalHash (artifact + lineage), not just its artifact's
// ContentHash: a promotion pins the exact (artifact, lineage) identity that
// was authorized, and two candidates can share an artifact from different
// lineages.
type PromotionRequest struct {
	CandidateID   string
	CandidateHash string
	FromState     CandidateState
	ToState       CandidateState
	Kind          PromotionKind
	Evaluation    evaluation.SuiteComparisonResult
	RequestedAt   time.Time
	RequestedBy   string
}

func (r PromotionRequest) Validate() error {
	if r.CandidateID == "" {
		return fmt.Errorf("%w: candidate id is required", ErrInvalidPromotionRequest)
	}
	if r.CandidateHash == "" {
		return fmt.Errorf("%w: candidate hash is required", ErrInvalidPromotionRequest)
	}
	if !r.Kind.Valid() {
		return fmt.Errorf("%w: invalid promotion kind %q", ErrInvalidPromotionRequest, r.Kind)
	}
	wantFrom, wantTo := r.Kind.expectedTransition()
	if r.FromState != wantFrom || r.ToState != wantTo {
		return fmt.Errorf("%w: kind %q requires %q -> %q, got %q -> %q", ErrInvalidPromotionRequest, r.Kind, wantFrom, wantTo, r.FromState, r.ToState)
	}
	if err := r.Evaluation.Validate(); err != nil {
		return fmt.Errorf("%w: invalid evaluation: %v", ErrInvalidPromotionRequest, err)
	}
	if r.Evaluation.OverallVerdict != evaluation.VerdictPass {
		return fmt.Errorf("%w: promotion requires a passing evaluation, got %q", ErrInvalidPromotionRequest, r.Evaluation.OverallVerdict)
	}
	if r.RequestedAt.IsZero() {
		return fmt.Errorf("%w: requested_at is required", ErrInvalidPromotionRequest)
	}
	if r.RequestedBy == "" {
		return fmt.Errorf("%w: requested_by is required", ErrInvalidPromotionRequest)
	}
	return nil
}

// PromotionOutcome is the ApprovalGate's decision on a PromotionRequest.
type PromotionOutcome string

const (
	PromotionAuthorized PromotionOutcome = "authorized"
	PromotionDenied     PromotionOutcome = "denied"
)

func (o PromotionOutcome) Valid() bool {
	switch o {
	case PromotionAuthorized, PromotionDenied:
		return true
	default:
		return false
	}
}

// PromotionDecision is what an ApprovalGate returns for a PromotionRequest.
type PromotionDecision struct {
	CandidateID string
	Kind        PromotionKind
	Outcome     PromotionOutcome
	Reason      string
	DecidedAt   time.Time
	DecidedBy   string
}

func (d PromotionDecision) Validate() error {
	if d.CandidateID == "" {
		return fmt.Errorf("%w: candidate id is required", ErrInvalidPromotionDecision)
	}
	if !d.Kind.Valid() {
		return fmt.Errorf("%w: invalid promotion kind %q", ErrInvalidPromotionDecision, d.Kind)
	}
	if !d.Outcome.Valid() {
		return fmt.Errorf("%w: invalid outcome %q", ErrInvalidPromotionDecision, d.Outcome)
	}
	if d.Outcome == PromotionDenied && d.Reason == "" {
		return fmt.Errorf("%w: reason is required when denying", ErrInvalidPromotionDecision)
	}
	if d.DecidedAt.IsZero() {
		return fmt.Errorf("%w: decided_at is required", ErrInvalidPromotionDecision)
	}
	if d.DecidedBy == "" {
		return fmt.Errorf("%w: decided_by is required", ErrInvalidPromotionDecision)
	}
	return nil
}
