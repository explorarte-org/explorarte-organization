// Package retrievalfixtures bridges R30 evaluation fixtures to the real RAG
// and organizational-memory managers backed by PostgreSQL.
package retrievalfixtures

import "github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"

const (
	fixtureIdentifierHardNegatives = "r30-03-identifier-hard-negatives"
	fixtureSemanticParaphrase      = "r30-04-semantic-paraphrase"
	fixtureMemoryRelevance         = "r30-05-memory-old-relevant-vs-new-irrelevant"
	fixtureCrossNamespace          = "r30-06-cross-namespace-authorization-denial"
	fixtureRejectedAdmission       = "r30-07-admission-candidate-rejected-never-approved"
)

// Scenario is deliberately small: the stable catalog owns the prose and
// limits, while Runner owns the executable corpus. Keeping the fixture ID in
// the typed payload prevents an accidentally activated catalog entry from
// being dispatched as a different scenario.
type Scenario struct{ FixtureID string }

var supportedFixtureIDs = map[string]struct{}{
	fixtureIdentifierHardNegatives: {},
	fixtureSemanticParaphrase:      {},
	fixtureMemoryRelevance:         {},
	fixtureCrossNamespace:          {},
	fixtureRejectedAdmission:       {},
}

// Activate attaches typed scenarios to the five PostgreSQL retrieval
// fixtures. No database work happens here, so `evaluation seed` remains a
// read-only catalog operation.
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
