package retrievalfixtures

import (
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
)

func TestRunnerRejectsMissingDisposableDatabase(t *testing.T) {
	var fixture fixtures.Fixture
	for _, candidate := range Activate(fixtures.CatalogR30()) {
		if candidate.ID == fixtureIdentifierHardNegatives {
			fixture = candidate
			break
		}
	}
	if _, err := (Runner{}).Run(context.Background(), fixture, "lexical"); err == nil {
		t.Fatal("runner accepted a nil database")
	}
}

func TestProfileForSubjectUsesOperationalProfileForNamedSmoke(t *testing.T) {
	profile, err := profileForSubject("deployment-smoke")
	if err != nil {
		t.Fatal(err)
	}
	if profile.name != "bge-m3-hybrid" || profile.dimension != bgeM3Dimension {
		t.Fatalf("profile=%+v", profile)
	}
	if _, err := profileForSubject(""); err == nil {
		t.Fatal("empty subject was accepted")
	}
}
