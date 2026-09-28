package search

import (
	"testing"
	"time"
)

func TestTheDefaultResearchBudgetBuysTwoHundredRequestsADayInSantiago(t *testing.T) {
	budget, err := LoadDailySearchBudget(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if budget.MaxQueries() != 200 || budget.Location.String() != "America/Santiago" {
		t.Fatalf("budget %+v max %d", budget, budget.MaxQueries())
	}
	// 02:30 UTC on the 28th is still the 27th in Santiago (UTC-3 or -4).
	now := time.Date(2026, 9, 28, 2, 30, 0, 0, time.UTC)
	if start := budget.DayStart(now); start.In(budget.Location).Day() != 27 || !start.Before(now) {
		t.Fatalf("day start %v", start)
	}
}

func TestTheResearchBudgetNarrowsAndThenStopsTicks(t *testing.T) {
	budget := DailySearchBudget{BudgetUSD: 0.05, CostPerQueryUSD: 0.01, Location: time.UTC}
	cfg := DefaultSchedulerConfig()
	cfg.MaxSearchRequestsPerTick, cfg.MaxTopicsPerTick = 5, 2

	full, ok := budget.Bound(cfg, 0)
	if !ok || full.MaxSearchRequestsPerTick != 5 || full.MaxTopicsPerTick != 2 {
		t.Fatalf("with the day unspent: %+v ok=%v", full, ok)
	}
	narrowed, ok := budget.Bound(cfg, 4)
	if !ok || narrowed.MaxSearchRequestsPerTick != 1 || narrowed.MaxTopicsPerTick != 1 {
		t.Fatalf("one request left: %+v ok=%v", narrowed, ok)
	}
	narrowed.Enabled = true
	if err := narrowed.Validate(); err != nil {
		t.Fatalf("a narrowed tick must still be a valid configuration: %v", err)
	}
	if _, ok := budget.Bound(cfg, 5); ok {
		t.Fatal("a spent day must not tick")
	}
}

func TestTheResearchBudgetRejectsNonsense(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"not a number":   {"AUTONOMOUS_RESEARCH_DAILY_BUDGET_USD": "two"},
		"negative":       {"AUTONOMOUS_RESEARCH_DAILY_BUDGET_USD": "-1"},
		"free queries":   {"AUTONOMOUS_RESEARCH_COST_PER_QUERY_USD": "0"},
		"unknown zone":   {"AUTONOMOUS_RESEARCH_TIMEZONE": "Mars/Olympus"},
		"above the roof": {"AUTONOMOUS_RESEARCH_DAILY_BUDGET_USD": "1000"},
	} {
		if _, err := LoadDailySearchBudget(func(k string) (string, bool) { v, ok := env[k]; return v, ok }); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestTheResearchWorkerRunsAtTheTopOfEveryLocalHour(t *testing.T) {
	santiago, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Fatal(err)
	}
	budget := DailySearchBudget{BudgetUSD: 2, CostPerQueryUSD: 0.01, Location: santiago}
	now := time.Date(2026, 9, 28, 14, 59, 59, 0, santiago)
	if next := budget.NextHourlyRun(now); !next.Equal(time.Date(2026, 9, 28, 15, 0, 0, 0, santiago)) {
		t.Fatalf("next run %v", next)
	}
	top := time.Date(2026, 9, 28, 15, 0, 0, 0, santiago)
	if next := budget.NextHourlyRun(top); !next.Equal(top.Add(time.Hour)) {
		t.Fatalf("at the top of the hour the next run is the following hour, got %v", next)
	}
}
