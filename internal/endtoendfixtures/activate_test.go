package endtoendfixtures

import (
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
)

func TestActivateMarksOnlyEndToEndFixtureReady(t *testing.T) {
	catalog := Activate(fixtures.CatalogR30())
	activated := 0
	for _, f := range catalog {
		if f.ID != fixtureEndToEnd {
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

func TestRunnerRejectsMissingDisposableDatabase(t *testing.T) {
	var target fixtures.Fixture
	for _, f := range Activate(fixtures.CatalogR30()) {
		if f.ID == fixtureEndToEnd {
			target = f
		}
	}
	if _, err := (Runner{}).Run(context.Background(), target, "test"); err == nil {
		t.Fatal("runner accepted a nil database")
	}
}
