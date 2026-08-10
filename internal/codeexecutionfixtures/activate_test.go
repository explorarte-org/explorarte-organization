package codeexecutionfixtures

import (
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
)

func TestActivateMarksExactlyTwoCodeExecutionFixturesReady(t *testing.T) {
	catalog := Activate(fixtures.CatalogR30())
	ready := 0
	for _, f := range catalog {
		if _, ok := supportedFixtureIDs[f.ID]; !ok {
			continue
		}
		ready++
		if f.Status != fixtures.StatusRunnerReady {
			t.Fatalf("fixture %s remained %s", f.ID, f.Status)
		}
		if scenario, ok := f.Scenario.(*Scenario); !ok || scenario.FixtureID != f.ID {
			t.Fatalf("fixture %s scenario=%#v", f.ID, f.Scenario)
		}
	}
	if ready != 2 {
		t.Fatalf("activated=%d want 2", ready)
	}
}

func TestPostgresRunnerRejectsMissingDisposableDatabase(t *testing.T) {
	var target fixtures.Fixture
	for _, f := range Activate(fixtures.CatalogR30()) {
		if f.ID == fixturePostgresMigration {
			target = f
		}
	}
	if _, err := (Runner{}).Run(context.Background(), target, "test"); err == nil {
		t.Fatal("migration runner accepted a nil database")
	}
}
