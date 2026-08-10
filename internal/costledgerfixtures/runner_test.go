package costledgerfixtures

import (
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
)

func TestRunnerRejectsMissingDisposableDatabase(t *testing.T) {
	var target fixtures.Fixture
	for _, f := range Activate(fixtures.CatalogR30()) {
		if f.ID == fixtureOrphanedReservation {
			target = f
		}
	}
	if _, err := (Runner{}).Run(context.Background(), target, "test"); err == nil {
		t.Fatal("runner accepted a nil database")
	}
}
