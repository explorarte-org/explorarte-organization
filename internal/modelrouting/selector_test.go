package modelrouting

import (
	"errors"
	"testing"
	"time"
)

func cand(provider, model, class string, priority int) Candidate {
	return Candidate{
		ProviderID: provider, ProviderModelID: model, Transport: "http_adapter",
		CapacityClass: class, Priority: priority,
		ProfileID: provider + "~pool~0", ModelProfileVersionID: 1,
	}
}

// TestFreeCapacityV1PicksHighestRankThenPriority: Section 12.C (Canonical
// pool only) and the general ranking contract -- free capacity before
// finite credit, declared priority breaks ties within a class.
func TestFreeCapacityV1PicksHighestRankThenPriority(t *testing.T) {
	candidates := []Candidate{
		cand("mistral", "ministral-8b-2512", "credit_monthly", 20),
		cand("cloudflare_workers_ai", "@cf/zai-org/glm-4.7-flash", "free_daily", 10),
	}
	decision, err := FreeCapacityV1{}.Select(candidates, nil, Requirements{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if decision.Candidate.ProviderID != "cloudflare_workers_ai" {
		t.Fatalf("expected free_daily to rank before credit_monthly, got %s", decision.Candidate.ProviderID)
	}
}

// TestFreeCapacityV1FallsThroughWhenPrimaryDisabled proves the pool can
// select EITHER member under the same policy depending on capacity state
// (Section 12.C).
func TestFreeCapacityV1FallsThroughWhenPrimaryDisabled(t *testing.T) {
	candidates := []Candidate{
		cand("cloudflare_workers_ai", "@cf/zai-org/glm-4.7-flash", "free_daily", 10),
		cand("mistral", "ministral-8b-2512", "credit_monthly", 20),
	}
	state := map[string]CandidateState{
		"cloudflare_workers_ai|@cf/zai-org/glm-4.7-flash": {Disabled: true},
	}
	decision, err := FreeCapacityV1{}.Select(candidates, state, Requirements{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if decision.Candidate.ProviderID != "mistral" {
		t.Fatalf("expected fallback to mistral, got %s", decision.Candidate.ProviderID)
	}
}

// TestFreeCapacityV1DeniesPaidWithoutAllowPaid: Section 12.L / invariant
// #15 -- AllowPaid=false is fail-closed regardless of capacity state on
// every free/credit candidate.
func TestFreeCapacityV1DeniesPaidWithoutAllowPaid(t *testing.T) {
	candidates := []Candidate{cand("some_provider", "some-model", "paid", 1)}
	_, err := FreeCapacityV1{}.Select(candidates, nil, Requirements{AllowPaid: false}, time.Now())
	if !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("err = %v, want ErrNoCapacity", err)
	}
}

func TestFreeCapacityV1AllowsPaidWhenExplicit(t *testing.T) {
	candidates := []Candidate{cand("some_provider", "some-model", "paid", 1)}
	decision, err := FreeCapacityV1{}.Select(candidates, nil, Requirements{AllowPaid: true}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if decision.Candidate.ProviderID != "some_provider" {
		t.Fatalf("unexpected candidate: %+v", decision.Candidate)
	}
}

// TestFreeCapacityV1CooldownExcludesCandidate proves a candidate in
// cooldown is skipped even when otherwise highest-ranked.
func TestFreeCapacityV1CooldownExcludesCandidate(t *testing.T) {
	now := time.Now()
	candidates := []Candidate{
		cand("cloudflare_workers_ai", "@cf/zai-org/glm-4.7-flash", "free_daily", 10),
		cand("mistral", "ministral-8b-2512", "credit_monthly", 20),
	}
	future := now.Add(time.Hour)
	state := map[string]CandidateState{
		"cloudflare_workers_ai|@cf/zai-org/glm-4.7-flash": {CooldownUntil: &future},
	}
	decision, err := FreeCapacityV1{}.Select(candidates, state, Requirements{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Candidate.ProviderID != "mistral" {
		t.Fatalf("expected cooldown to exclude cloudflare, got %s", decision.Candidate.ProviderID)
	}
}

func TestFreeCapacityV1NoEligibleCandidatesReturnsErrNoCapacity(t *testing.T) {
	candidates := []Candidate{cand("cloudflare_workers_ai", "@cf/zai-org/glm-4.7-flash", "free_daily", 10)}
	state := map[string]CandidateState{"cloudflare_workers_ai|@cf/zai-org/glm-4.7-flash": {Disabled: true}}
	_, err := FreeCapacityV1{}.Select(candidates, state, Requirements{}, time.Now())
	if !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("err = %v, want ErrNoCapacity", err)
	}
}

// TestFreeCapacityV1Deterministic: Section 12.N (selector is pure) --
// same inputs, same output, across repeated calls, no time.Now() reads
// beyond the supplied now, no randomness.
func TestFreeCapacityV1Deterministic(t *testing.T) {
	candidates := []Candidate{
		cand("mistral", "ministral-8b-2512", "credit_monthly", 20),
		cand("cloudflare_workers_ai", "@cf/zai-org/glm-4.7-flash", "free_daily", 10),
	}
	now := time.Now()
	first, err := FreeCapacityV1{}.Select(candidates, nil, Requirements{}, now)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		again, err := FreeCapacityV1{}.Select(candidates, nil, Requirements{}, now)
		if err != nil {
			t.Fatal(err)
		}
		if again.Candidate != first.Candidate {
			t.Fatalf("selection not deterministic: run %d got %+v, want %+v", i, again.Candidate, first.Candidate)
		}
	}
}
