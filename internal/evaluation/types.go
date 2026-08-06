package evaluation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"time"
)

// TraceRef is an opaque reference to a previously recorded run trace. It
// carries no structured content of its own; internal/decisiongraph (Branch
// 14) is the eventual source of truth for what these fields mean.
type TraceRef struct {
	RunID          int64
	TraceHash      string
	SchemaVersion  string
	OrganizationID string
}

func (r TraceRef) Validate() error {
	if r.RunID <= 0 {
		return fmt.Errorf("%w: run id must be positive", ErrInvalidTraceRef)
	}
	if r.TraceHash == "" {
		return fmt.Errorf("%w: trace hash is required", ErrInvalidTraceRef)
	}
	if r.SchemaVersion == "" {
		return fmt.Errorf("%w: schema version is required", ErrInvalidTraceRef)
	}
	if r.OrganizationID == "" {
		return fmt.Errorf("%w: organization id is required", ErrInvalidTraceRef)
	}
	return nil
}

// EvaluationTrace is the materialized trace a TraceSource resolves a TraceRef
// to. Payload is treated as opaque bytes by this package; only its hash is
// verified against the immutable reference.
type EvaluationTrace struct {
	Ref      TraceRef
	Payload  []byte
	LoadedAt time.Time
}

func (t EvaluationTrace) ContentHash() string {
	sum := sha256.Sum256(t.Payload)
	return hex.EncodeToString(sum[:])
}

func (t EvaluationTrace) Validate() error {
	if err := t.Ref.Validate(); err != nil {
		return err
	}
	if len(t.Payload) == 0 {
		return fmt.Errorf("%w: trace payload is empty", ErrInvalidTrace)
	}
	if t.LoadedAt.IsZero() {
		return fmt.Errorf("%w: loaded_at is required", ErrInvalidTrace)
	}
	if t.ContentHash() != t.Ref.TraceHash {
		return fmt.Errorf("%w: trace %d", ErrTraceHashMismatch, t.Ref.RunID)
	}
	return nil
}

// EvaluationCase is one case within an EvaluationSuite: a fixed trace to
// replay and the outcome expected of it.
type EvaluationCase struct {
	ID              string
	Description     string
	Trace           TraceRef
	Weight          float64
	ExpectedOutcome string
}

func (c EvaluationCase) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("%w: case id is required", ErrInvalidCase)
	}
	if err := c.Trace.Validate(); err != nil {
		return fmt.Errorf("%w: case %s: %v", ErrInvalidCase, c.ID, err)
	}
	if c.Weight <= 0 || math.IsNaN(c.Weight) || math.IsInf(c.Weight, 0) {
		return fmt.Errorf("%w: case %s: weight must be positive and finite", ErrInvalidCase, c.ID)
	}
	if c.ExpectedOutcome == "" {
		return fmt.Errorf("%w: case %s: expected outcome is required", ErrInvalidCase, c.ID)
	}
	return nil
}

// EvaluationSuite is an immutable, versioned set of cases run against both
// the baseline and the candidate.
type EvaluationSuite struct {
	ID            string
	Name          string
	SchemaVersion string
	Cases         []EvaluationCase
}

func (s EvaluationSuite) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("%w: suite id is required", ErrInvalidSuite)
	}
	if s.Name == "" {
		return fmt.Errorf("%w: suite name is required", ErrInvalidSuite)
	}
	if s.SchemaVersion == "" {
		return fmt.Errorf("%w: suite schema version is required", ErrInvalidSuite)
	}
	if len(s.Cases) == 0 {
		return fmt.Errorf("%w: suite %s", ErrEmptySuite, s.ID)
	}
	seen := make(map[string]struct{}, len(s.Cases))
	for _, c := range s.Cases {
		if err := c.Validate(); err != nil {
			return err
		}
		if _, dup := seen[c.ID]; dup {
			return fmt.Errorf("%w: case %s in suite %s", ErrDuplicateCase, c.ID, s.ID)
		}
		seen[c.ID] = struct{}{}
	}
	return nil
}

func (s EvaluationSuite) caseByID(id string) (EvaluationCase, bool) {
	for _, c := range s.Cases {
		if c.ID == id {
			return c, true
		}
	}
	return EvaluationCase{}, false
}

// EvaluationRole distinguishes which side of a comparison a result belongs
// to: the existing baseline or the proposed candidate.
type EvaluationRole string

const (
	RoleBaseline  EvaluationRole = "baseline"
	RoleCandidate EvaluationRole = "candidate"
)

func (r EvaluationRole) Valid() bool {
	switch r {
	case RoleBaseline, RoleCandidate:
		return true
	default:
		return false
	}
}

// EvaluationRequest is what an Evaluator needs to score a single case for
// either the baseline or the candidate.
type EvaluationRequest struct {
	Suite                 EvaluationSuite
	Case                  EvaluationCase
	Trace                 EvaluationTrace
	CandidateID           string
	CandidateArtifactHash string
	Role                  EvaluationRole
}

func (r EvaluationRequest) Validate() error {
	if err := r.Suite.Validate(); err != nil {
		return err
	}
	if err := r.Case.Validate(); err != nil {
		return err
	}
	if member, ok := r.Suite.caseByID(r.Case.ID); !ok || member.Trace != r.Case.Trace {
		return fmt.Errorf("%w: case %s is not a member of suite %s", ErrInvalidRequest, r.Case.ID, r.Suite.ID)
	}
	if err := r.Trace.Validate(); err != nil {
		return err
	}
	if r.Trace.Ref != r.Case.Trace {
		return fmt.Errorf("%w: loaded trace does not match case %s", ErrInvalidRequest, r.Case.ID)
	}
	if r.CandidateID == "" {
		return fmt.Errorf("%w: candidate id is required", ErrInvalidRequest)
	}
	if r.CandidateArtifactHash == "" {
		return fmt.Errorf("%w: candidate artifact hash is required", ErrInvalidRequest)
	}
	if !r.Role.Valid() {
		return fmt.Errorf("%w: invalid role %q", ErrInvalidRequest, r.Role)
	}
	return nil
}

// Metric is a single named, unit-labeled measurement produced by an
// Evaluator for one case.
type Metric struct {
	Name  string
	Value float64
	Unit  string
}

func (m Metric) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("%w: metric name is required", ErrInvalidResult)
	}
	if math.IsNaN(m.Value) || math.IsInf(m.Value, 0) {
		return fmt.Errorf("%w: metric %s has a non-finite value", ErrInvalidResult, m.Name)
	}
	return nil
}

// Verdict is the outcome an Evaluator (or a comparison) reaches for a case.
type Verdict string

const (
	VerdictPass         Verdict = "pass"
	VerdictFail         Verdict = "fail"
	VerdictInconclusive Verdict = "inconclusive"
)

func (v Verdict) Valid() bool {
	switch v {
	case VerdictPass, VerdictFail, VerdictInconclusive:
		return true
	default:
		return false
	}
}

// EvaluationResult is what an Evaluator returns for a single case and role.
type EvaluationResult struct {
	CaseID      string
	Role        EvaluationRole
	TraceRef    TraceRef
	Metrics     []Metric
	Verdict     Verdict
	Notes       string
	EvaluatedAt time.Time
}

func (r EvaluationResult) Validate() error {
	if r.CaseID == "" {
		return fmt.Errorf("%w: case id is required", ErrInvalidResult)
	}
	if !r.Role.Valid() {
		return fmt.Errorf("%w: invalid role %q", ErrInvalidResult, r.Role)
	}
	if err := r.TraceRef.Validate(); err != nil {
		return err
	}
	if len(r.Metrics) == 0 {
		return fmt.Errorf("%w: result for case %s has no metrics", ErrInvalidResult, r.CaseID)
	}
	seen := make(map[string]struct{}, len(r.Metrics))
	for _, m := range r.Metrics {
		if err := m.Validate(); err != nil {
			return err
		}
		if _, dup := seen[m.Name]; dup {
			return fmt.Errorf("%w: duplicate metric %s for case %s", ErrInvalidResult, m.Name, r.CaseID)
		}
		seen[m.Name] = struct{}{}
	}
	if !r.Verdict.Valid() {
		return fmt.Errorf("%w: invalid verdict %q for case %s", ErrInvalidResult, r.Verdict, r.CaseID)
	}
	if r.EvaluatedAt.IsZero() {
		return fmt.Errorf("%w: evaluated_at is required for case %s", ErrInvalidResult, r.CaseID)
	}
	return nil
}
