// Package agentmessagingfixtures bridges R30 evaluation metadata to the real
// durable agent inbox backed by PostgreSQL.
package agentmessagingfixtures

import "github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"

const fixtureLeaseRecovery = "r30-10-messaging-lease-recovery"

// Scenario makes activation explicit without duplicating catalog policy.
type Scenario struct{ FixtureID string }

// Activate marks only the messaging lease fixture executable.
func Activate(catalog []fixtures.Fixture) []fixtures.Fixture {
	activated := make([]fixtures.Fixture, len(catalog))
	for i, f := range catalog {
		if f.ID != fixtureLeaseRecovery {
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
