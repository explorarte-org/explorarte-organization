package retrievalfixtures

import (
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
)

func TestActivateMarksOnlyRetrievalFixturesReady(t *testing.T) {
	catalog := Activate(fixtures.CatalogR30())
	ready := 0
	for _, f := range catalog {
		_, supported := supportedFixtureIDs[f.ID]
		if supported {
			ready++
			if f.Status != fixtures.StatusRunnerReady {
				t.Fatalf("fixture %s remained %s", f.ID, f.Status)
			}
			if scenario, ok := f.Scenario.(*Scenario); !ok || scenario.FixtureID != f.ID {
				t.Fatalf("fixture %s has scenario %#v", f.ID, f.Scenario)
			}
		}
	}
	if ready != 5 {
		t.Fatalf("activated=%d want 5", ready)
	}
}
