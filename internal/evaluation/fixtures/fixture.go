// Package fixtures defines R30's canary evaluation projects: versioned,
// reproducible scenarios the evaluation harness runs to compare retrieval
// modes (lexical, Gemini-hybrid, BGE-M3-hybrid) and organizational
// machinery (budget, DAG, messaging, authorization) against fixed,
// hand-authored expectations, instead of trusting an average metric.
//
// Every Fixture in Catalog carries the fields the R30 specification
// requires: ID+version, objective, organization/roles, initial data
// (Scenario), expected result, hard invariants, expected evidence, max
// budget, max retries/replans, timeout, and a deterministic seed. Status
// records honestly whether a Runner exists for it yet in this repository
// today, or whether it is defined-only pending a later R30 phase (its
// subsystem — BGE-M3, the web-evidence harness, durable cost-ledger
// storage — has not landed yet). A fixture is never marked ready before a
// real Runner backs it.
package fixtures

import (
	"errors"
	"fmt"
	"time"
)

// Status is deliberately not a bool: a fixture can be fully specified
// (objective, invariants, expected evidence all real) while still having
// no working Runner, because the subsystem it exercises lands in a later
// R30 phase. Reporting that honestly — rather than a single "done" — is
// the whole point of this field.
type Status string

const (
	// StatusRunnerReady means a Runner in this repository can execute this
	// fixture today, against real dependencies (Postgres where relevant),
	// deterministically for a fixed Seed.
	StatusRunnerReady Status = "runner_ready"
	// StatusPending means the fixture is fully specified but no Runner
	// exists yet — PendingPhase names which R30 phase is expected to add
	// one.
	StatusPending Status = "pending"
)

// Fixture is one R30 canary evaluation project.
type Fixture struct {
	ID             string
	Version        int
	Title          string
	Objective      string
	OrganizationID string
	Roles          []string
	// Scenario is the opaque, Runner-specific initial data ("initial data"
	// in the R30 spec) — its concrete Go type depends on RunnerKind, e.g.
	// *DecisionGraphScenario for RunnerKind "decisiongraph".
	Scenario         any
	ExpectedResult   string
	HardInvariants   []string
	ExpectedEvidence []string
	MaxBudgetUSD     float64
	MaxRetries       int
	MaxReplans       int
	Timeout          time.Duration
	Seed             int64
	Status           Status
	PendingPhase     string
	RunnerKind       string
}

var (
	ErrInvalidFixture = errors.New("fixtures: invalid fixture")
)

func (f Fixture) Validate() error {
	if f.ID == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidFixture)
	}
	if f.Version <= 0 {
		return fmt.Errorf("%w: %s: version must be positive", ErrInvalidFixture, f.ID)
	}
	if f.Title == "" || f.Objective == "" {
		return fmt.Errorf("%w: %s: title and objective are required", ErrInvalidFixture, f.ID)
	}
	if f.OrganizationID == "" {
		return fmt.Errorf("%w: %s: organization id is required", ErrInvalidFixture, f.ID)
	}
	if len(f.Roles) == 0 {
		return fmt.Errorf("%w: %s: at least one role is required", ErrInvalidFixture, f.ID)
	}
	if f.Scenario == nil {
		return fmt.Errorf("%w: %s: initial data (scenario) is required", ErrInvalidFixture, f.ID)
	}
	if f.ExpectedResult == "" {
		return fmt.Errorf("%w: %s: expected result is required", ErrInvalidFixture, f.ID)
	}
	if len(f.HardInvariants) == 0 {
		return fmt.Errorf("%w: %s: at least one hard invariant is required", ErrInvalidFixture, f.ID)
	}
	if len(f.ExpectedEvidence) == 0 {
		return fmt.Errorf("%w: %s: at least one expected evidence reference is required", ErrInvalidFixture, f.ID)
	}
	if f.MaxBudgetUSD <= 0 {
		return fmt.Errorf("%w: %s: max budget must be positive", ErrInvalidFixture, f.ID)
	}
	if f.MaxRetries < 0 || f.MaxReplans < 0 {
		return fmt.Errorf("%w: %s: max retries/replans cannot be negative", ErrInvalidFixture, f.ID)
	}
	if f.Timeout <= 0 {
		return fmt.Errorf("%w: %s: timeout must be positive", ErrInvalidFixture, f.ID)
	}
	if f.Seed == 0 {
		return fmt.Errorf("%w: %s: a zero seed is not a deliberate deterministic seed", ErrInvalidFixture, f.ID)
	}
	switch f.Status {
	case StatusRunnerReady:
		if f.PendingPhase != "" {
			return fmt.Errorf("%w: %s: runner-ready fixture must not name a pending phase", ErrInvalidFixture, f.ID)
		}
	case StatusPending:
		if f.PendingPhase == "" {
			return fmt.Errorf("%w: %s: pending fixture must name which phase will add its runner", ErrInvalidFixture, f.ID)
		}
	default:
		return fmt.Errorf("%w: %s: invalid status %q", ErrInvalidFixture, f.ID, f.Status)
	}
	if f.RunnerKind == "" {
		return fmt.Errorf("%w: %s: runner kind is required", ErrInvalidFixture, f.ID)
	}
	return nil
}
