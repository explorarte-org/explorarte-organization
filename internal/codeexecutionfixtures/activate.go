// Package codeexecutionfixtures provides isolated, deterministic execution
// sandboxes for the two R30 code-authoring fixtures.
package codeexecutionfixtures

import "github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"

const (
	fixtureGoBugFix          = "r30-01-go-bug-fix"
	fixturePostgresMigration = "r30-02-postgres-migration"
)

// Scenario makes activation explicit without coupling the base catalog to
// filesystem or PostgreSQL execution details.
type Scenario struct{ FixtureID string }

var supportedFixtureIDs = map[string]struct{}{
	fixtureGoBugFix:          {},
	fixturePostgresMigration: {},
}

// Activate marks only fixtures backed by Runner executable.
func Activate(catalog []fixtures.Fixture) []fixtures.Fixture {
	activated := make([]fixtures.Fixture, len(catalog))
	for i, f := range catalog {
		if _, ok := supportedFixtureIDs[f.ID]; !ok {
			activated[i] = f
			continue
		}
		f.Scenario = &Scenario{FixtureID: f.ID}
		f.Status = fixtures.StatusRunnerReady
		f.PendingPhase = ""
		activated[i] = f
	}
	return activated
}
