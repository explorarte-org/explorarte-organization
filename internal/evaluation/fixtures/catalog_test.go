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

func TestCatalogR30RunnerReadyFixturesAreSupportedByDecisionGraphRunner(t *testing.T) {
	runner := DecisionGraphRunner{}
	found := 0
	for _, f := range CatalogR30() {
		if f.Status != StatusRunnerReady {
			continue
		}
		found++
		if !runner.Supports(f) {
			t.Fatalf("fixture %s is marked runner_ready but DecisionGraphRunner.Supports returned false", f.ID)
		}
	}
	if found == 0 {
		t.Fatal("expected at least one runner_ready fixture in the R30 catalog")
	}
}

func TestCatalogR30PendingFixturesAreNotSupportedByAnyRunnerYet(t *testing.T) {
	runner := DecisionGraphRunner{}
	for _, f := range CatalogR30() {
		if f.Status != StatusPending {
			continue
		}
		if runner.Supports(f) {
			t.Fatalf("fixture %s is marked pending but a runner claims to support it — Status is now a lie", f.ID)
		}
	}
}
