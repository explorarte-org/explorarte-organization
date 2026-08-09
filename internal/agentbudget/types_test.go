package agentbudget

import (
	"errors"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

func testLimits() Limits {
	return Limits{
		MaxUSD: modelpricing.USDFromDollars(10), MaxTokens: 100_000, MaxModelCalls: 50,
		MaxWallTimeMS: 3_600_000, MaxDepth: 5, MaxRetries: 10, MaxSubagents: 20,
	}
}

func TestReserveAppliesEveryDimension(t *testing.T) {
	limits := testLimits()
	delta := Usage{UsedUSD: modelpricing.USDFromDollars(1), UsedTokens: 1_000, UsedModelCalls: 1, UsedWallTimeMS: 500, UsedRetries: 1, UsedSubagents: 1}
	next, err := Reserve(Usage{}, delta, limits)
	if err != nil {
		t.Fatal(err)
	}
	if next.UsedUSD != delta.UsedUSD || next.UsedTokens != delta.UsedTokens || next.UsedModelCalls != delta.UsedModelCalls ||
		next.UsedWallTimeMS != delta.UsedWallTimeMS || next.UsedRetries != delta.UsedRetries || next.UsedSubagents != delta.UsedSubagents {
		t.Fatalf("next=%+v want=%+v", next, delta)
	}
}

func TestReserveDepthIsCurrentDepthNotCumulative(t *testing.T) {
	limits := testLimits()
	current := Usage{Depth: 2}
	next, err := Reserve(current, Usage{Depth: 1}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if next.Depth != 3 {
		t.Fatalf("depth=%d want=3", next.Depth)
	}
}

func TestReserveRejectsEachDimensionAtItsOwnLimit(t *testing.T) {
	limits := Limits{MaxUSD: 100, MaxTokens: 100, MaxModelCalls: 100, MaxWallTimeMS: 100, MaxDepth: 100, MaxRetries: 100, MaxSubagents: 100}
	cases := []struct {
		name  string
		delta Usage
	}{
		{"usd", Usage{UsedUSD: 101}},
		{"tokens", Usage{UsedTokens: 101}},
		{"model calls", Usage{UsedModelCalls: 101}},
		{"wall time", Usage{UsedWallTimeMS: 101}},
		{"depth", Usage{Depth: 101}},
		{"retries", Usage{UsedRetries: 101}},
		{"subagents", Usage{UsedSubagents: 101}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Reserve(Usage{}, tc.delta, limits); !errors.Is(err, ErrBudgetExceeded) {
				t.Fatalf("%s: err=%v want ErrBudgetExceeded", tc.name, err)
			}
		})
	}
}

func TestReserveAtExactLimitSucceeds(t *testing.T) {
	limits := Limits{MaxUSD: 100, MaxTokens: 100, MaxModelCalls: 100, MaxWallTimeMS: 100, MaxDepth: 100, MaxRetries: 100, MaxSubagents: 100}
	next, err := Reserve(Usage{}, Usage{UsedUSD: 100, UsedTokens: 100, UsedModelCalls: 100, UsedWallTimeMS: 100, Depth: 100, UsedRetries: 100, UsedSubagents: 100}, limits)
	if err != nil {
		t.Fatalf("at exact limit should succeed: %v", err)
	}
	if next.UsedUSD != 100 {
		t.Fatalf("next=%+v", next)
	}
}

func TestReserveNeverPartiallyApplies(t *testing.T) {
	limits := testLimits()
	// USD is fine, but model calls blow the limit — the whole reservation
	// must be rejected, not just the calls dimension.
	delta := Usage{UsedUSD: modelpricing.USDFromDollars(1), UsedModelCalls: 999}
	if _, err := Reserve(Usage{}, delta, limits); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err=%v want ErrBudgetExceeded", err)
	}
}

func TestReserveRejectsNegativeDelta(t *testing.T) {
	limits := testLimits()
	if _, err := Reserve(Usage{}, Usage{UsedUSD: -1}, limits); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err=%v want ErrInvalidRequest", err)
	}
}

func TestLimitsValidateRequiresEveryDimensionPositive(t *testing.T) {
	full := testLimits()
	fields := []func(*Limits){
		func(l *Limits) { l.MaxUSD = 0 },
		func(l *Limits) { l.MaxTokens = 0 },
		func(l *Limits) { l.MaxModelCalls = 0 },
		func(l *Limits) { l.MaxWallTimeMS = 0 },
		func(l *Limits) { l.MaxDepth = 0 },
		func(l *Limits) { l.MaxRetries = 0 },
		func(l *Limits) { l.MaxSubagents = 0 },
	}
	for i, zero := range fields {
		limits := full
		zero(&limits)
		if err := limits.Validate(); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("case %d: err=%v want ErrInvalidRequest", i, err)
		}
	}
	if err := full.Validate(); err != nil {
		t.Fatalf("fully populated limits should validate: %v", err)
	}
}
