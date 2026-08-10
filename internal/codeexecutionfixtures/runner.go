package codeexecutionfixtures

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
)

// Runner executes R30 fixtures 01-02 inside isolated, disposable sandboxes:
// r30-01 in a temporary filesystem directory with its own go.mod, r30-02
// against a dedicated, dropped-on-exit PostgreSQL schema. Store is only
// used by the PostgreSQL fixture; the Go sandbox needs no database.
type Runner struct{ Store *platformpostgres.Store }

var _ fixtures.Runner = Runner{}

func (Runner) Supports(f fixtures.Fixture) bool {
	_, ok := supportedFixtureIDs[f.ID]
	return ok && f.RunnerKind == "code-execution-sandbox" && f.Status == fixtures.StatusRunnerReady
}

func (r Runner) Run(ctx context.Context, f fixtures.Fixture, subjectID string) (fixtures.RunOutcome, error) {
	scenario, ok := f.Scenario.(*Scenario)
	if !ok || scenario.FixtureID != f.ID {
		return fixtures.RunOutcome{}, fmt.Errorf("fixture %s was not activated by codeexecutionfixtures.Activate", f.ID)
	}
	switch f.ID {
	case fixtureGoBugFix:
		return r.runGoBugFix(ctx, f, subjectID)
	case fixturePostgresMigration:
		return r.runPostgresMigration(ctx, f, subjectID)
	default:
		return fixtures.RunOutcome{}, fmt.Errorf("unsupported code-execution fixture %s", f.ID)
	}
}

type recorder struct {
	outcome fixtures.RunOutcome
	passed  bool
}

func newRecorder(f fixtures.Fixture, subjectID string) *recorder {
	return &recorder{passed: true, outcome: fixtures.RunOutcome{
		FixtureID: f.ID, SubjectID: subjectID, InvariantResults: map[string]bool{}, Metrics: map[string]float64{},
		EvidenceRefs: append([]string(nil), f.ExpectedEvidence...),
	}}
}

func (r *recorder) record(name string, passed bool) {
	r.outcome.InvariantResults[name] = passed
	if !passed {
		r.passed = false
		r.outcome.ViolatedInvariants = append(r.outcome.ViolatedInvariants, name)
	}
}

func (r *recorder) finish(notes string) fixtures.RunOutcome {
	r.outcome.Passed = r.passed
	r.outcome.Notes = notes
	return r.outcome
}

func stableSuffix(subjectID string) string {
	sum := sha256.Sum256([]byte("codeexecution-runner-v1|" + subjectID))
	return hex.EncodeToString(sum[:6])
}
