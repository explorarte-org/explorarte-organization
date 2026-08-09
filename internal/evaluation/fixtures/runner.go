package fixtures

import (
	"context"
	"fmt"
	"math/rand"
)

// RunOutcome is what executing a Fixture against one subject (a retrieval
// mode, a routing profile, an organizational configuration under test)
// produces. It is deliberately invariant-first, not just pass/fail: R30's
// hard gates ("namespace leakage", "DAG terminal sin evidencia real", ...)
// must be checked and reported individually, since a suite average can
// hide exactly the failure this evaluation program exists to catch.
type RunOutcome struct {
	FixtureID          string
	SubjectID          string
	Passed             bool
	InvariantResults   map[string]bool
	ViolatedInvariants []string
	EvidenceRefs       []string
	Metrics            map[string]float64
	Notes              string
}

// Runner executes one Fixture, deterministically for a given seed,
// against one subject under test (e.g. "lexical", "gemini-hybrid",
// "bge-m3-hybrid", or an organizational configuration identifier — the
// meaning of SubjectID is defined by the caller, Runner only threads it
// through into RunOutcome for later comparison via internal/evaluation).
type Runner interface {
	// Supports reports whether this Runner knows how to execute f (matches
	// f.RunnerKind and f.Status == StatusRunnerReady). Callers should skip
	// — not fail — a fixture no Runner supports yet.
	Supports(f Fixture) bool
	Run(ctx context.Context, f Fixture, subjectID string) (RunOutcome, error)
}

// DeterministicSource returns a *rand.Rand seeded from f.Seed, mixed with
// subjectID so two different subjects run against the same fixture never
// silently share a random sequence, while the same (fixture, subject) pair
// always reproduces identically — the reproducibility R30 requires of
// every project run.
func DeterministicSource(f Fixture, subjectID string) *rand.Rand {
	mixed := f.Seed
	for _, b := range []byte(subjectID) {
		mixed = mixed*1099511628211 ^ int64(b)
	}
	return rand.New(rand.NewSource(mixed))
}

// RunSuite runs every fixture in fixturesList that r supports against
// subjectID, in ID order, and returns one RunOutcome per fixture. It stops
// and returns an error on the first Runner failure (a fixture that
// executes but fails its invariants is not an error — that is a
// RunOutcome with Passed=false — this only aborts on the Runner itself
// being unable to complete the run).
func RunSuite(ctx context.Context, r Runner, fixturesList []Fixture, subjectID string) ([]RunOutcome, error) {
	outcomes := make([]RunOutcome, 0, len(fixturesList))
	for _, f := range fixturesList {
		if err := f.Validate(); err != nil {
			return nil, fmt.Errorf("run suite: %w", err)
		}
		if !r.Supports(f) {
			continue
		}
		outcome, err := r.Run(ctx, f, subjectID)
		if err != nil {
			return nil, fmt.Errorf("run suite: fixture %s: %w", f.ID, err)
		}
		if outcome.FixtureID != f.ID {
			return nil, fmt.Errorf("run suite: runner returned outcome for %q, requested %q", outcome.FixtureID, f.ID)
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes, nil
}
