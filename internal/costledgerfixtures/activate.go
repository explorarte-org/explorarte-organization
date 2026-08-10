// Package costledgerfixtures bridges R30 evaluation metadata to the real
// cost ledger backed by PostgreSQL.
package costledgerfixtures

import "github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"

const fixtureOrphanedReservation = "r30-09-orphaned-ambiguous-reservation"

// Scenario makes activation explicit without duplicating the stable catalog.
type Scenario struct{ FixtureID string }

// Activate marks only the cost-ledger fixture executable. It performs no
// database work; the destructive-safety guard lives in Runner.Run.
func Activate(catalog []fixtures.Fixture) []fixtures.Fixture {
	activated := make([]fixtures.Fixture, len(catalog))
	for i, f := range catalog {
		if f.ID != fixtureOrphanedReservation {
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
