package fixtures

import "testing"

func TestCatalogR30HasFourteenValidUniqueFixtures(t *testing.T) {
	catalog := CatalogR30()
	if len(catalog) != 14 {
		t.Fatalf("catalog has %d fixtures, want 14", len(catalog))
	}
	seen := make(map[string]struct{}, len(catalog))
	for _, f := range catalog {
		if err := f.Validate(); err != nil {
			t.Fatalf("fixture %s failed validation: %v", f.ID, err)
		}
		if _, dup := seen[f.ID]; dup {
			t.Fatalf("duplicate fixture id %s", f.ID)
		}
		seen[f.ID] = struct{}{}
	}
}

// TestCatalogR30BaseFixturesStartPending documents this package's own
// boundary: internal/evaluation/fixtures never imports
// internal/decisiongraph (see scripts/check-improvement-fitness.sh), so
// CatalogR30 alone cannot mark any decisiongraph-backed fixture runner-
// ready — only internal/decisiongraphfixtures.Activate can, by attaching
// a real scenario from outside this package. A fixture flipping to
// StatusRunnerReady here without an Activate call would be exactly the
// architectural drift that check enforces.
func TestCatalogR30BaseFixturesStartPending(t *testing.T) {
	for _, f := range CatalogR30() {
		if f.Status != StatusPending {
			t.Fatalf("fixture %s has status %q before any Activate call, want %q", f.ID, f.Status, StatusPending)
		}
	}
}
