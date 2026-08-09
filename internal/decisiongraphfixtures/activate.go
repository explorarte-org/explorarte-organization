package decisiongraphfixtures

import "github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"

// Activate returns a copy of catalog where every fixture this package
// knows how to run (see scenariosByFixtureID) is given its real
// *DecisionGraphScenario and marked fixtures.StatusRunnerReady; every
// other fixture passes through unchanged. This is the only place
// internal/evaluation/fixtures.CatalogR30's decisiongraph-shaped fixtures
// go from "defined" to "runnable" — the base catalog itself stays free of
// any internal/decisiongraph import.
func Activate(catalog []fixtures.Fixture) []fixtures.Fixture {
	scenarios := scenariosByFixtureID()
	activated := make([]fixtures.Fixture, len(catalog))
	for i, f := range catalog {
		scenario, ok := scenarios[f.ID]
		if !ok {
			activated[i] = f
			continue
		}
		f.Scenario = scenario
		f.Status = fixtures.StatusRunnerReady
		f.PendingPhase = ""
		activated[i] = f
	}
	return activated
}
