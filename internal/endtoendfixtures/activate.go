// Package endtoendfixtures composes R30's individually-proven subsystems
// (retrieval evidence, executive orchestration, DAG/decision recording,
// agent messaging, agent budgets) into one real task lifecycle for
// r30-14-end-to-end — the fixture whose own hard invariant is that every
// R30 hard gate holds together, not merely in isolation.
package endtoendfixtures

import "github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"

const fixtureEndToEnd = "r30-14-end-to-end"

// Scenario makes activation explicit without duplicating catalog policy.
type Scenario struct{ FixtureID string }

// Activate marks only the end-to-end fixture executable.
func Activate(catalog []fixtures.Fixture) []fixtures.Fixture {
	activated := make([]fixtures.Fixture, len(catalog))
	for i, f := range catalog {
		if f.ID != fixtureEndToEnd {
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
