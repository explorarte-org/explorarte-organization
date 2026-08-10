package agentmessagingfixtures

import (
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
)

func TestActivateMarksOnlyMessagingFixtureReady(t *testing.T) {
	catalog := Activate(fixtures.CatalogR30())
	activated := 0
	for _, f := range catalog {
		if f.ID != fixtureLeaseRecovery {
			continue
		}
		activated++
		if f.Status != fixtures.StatusRunnerReady {
			t.Fatalf("fixture remained %s", f.Status)
		}
		if scenario, ok := f.Scenario.(*Scenario); !ok || scenario.FixtureID != f.ID {
			t.Fatalf("scenario=%#v", f.Scenario)
		}
	}
	if activated != 1 {
		t.Fatalf("activated=%d want 1", activated)
	}
}
