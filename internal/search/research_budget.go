package search

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// DailySearchBudget caps the research worker's spend per local day. Every search request is charged
// CostPerQueryUSD whatever provider answers it: the academic providers are free and query
// generation is deterministic today, so the charge is a deliberate upper bound (paid web search is
// about a cent a request), not a price. The day is the organization's local day.
type DailySearchBudget struct {
	BudgetUSD       float64
	CostPerQueryUSD float64
	Location        *time.Location
}

const (
	DefaultResearchDailyBudgetUSD    = 2.0
	DefaultResearchCostPerQueryUSD   = 0.01
	DefaultResearchScheduleLocation  = "America/Santiago"
	researchBudgetMaxDailyBudgetUSD  = 100.0
	researchBudgetMinCostPerQueryUSD = 0.0001
)

// LoadDailySearchBudget reads AUTONOMOUS_RESEARCH_DAILY_BUDGET_USD,
// AUTONOMOUS_RESEARCH_COST_PER_QUERY_USD and AUTONOMOUS_RESEARCH_TIMEZONE.
func LoadDailySearchBudget(lookup LookupEnv) (DailySearchBudget, error) {
	budget := DailySearchBudget{BudgetUSD: DefaultResearchDailyBudgetUSD, CostPerQueryUSD: DefaultResearchCostPerQueryUSD}
	zone := DefaultResearchScheduleLocation
	if lookup != nil {
		var err error
		if budget.BudgetUSD, err = envFloat(lookup, "AUTONOMOUS_RESEARCH_DAILY_BUDGET_USD", budget.BudgetUSD); err != nil {
			return DailySearchBudget{}, err
		}
		if budget.CostPerQueryUSD, err = envFloat(lookup, "AUTONOMOUS_RESEARCH_COST_PER_QUERY_USD", budget.CostPerQueryUSD); err != nil {
			return DailySearchBudget{}, err
		}
		if raw, ok := lookup("AUTONOMOUS_RESEARCH_TIMEZONE"); ok && strings.TrimSpace(raw) != "" {
			zone = strings.TrimSpace(raw)
		}
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return DailySearchBudget{}, fmt.Errorf("%w: research timezone %q: %v", ErrInvalidRequest, zone, err)
	}
	budget.Location = location
	return budget, budget.Validate()
}

func (b DailySearchBudget) Validate() error {
	if b.BudgetUSD < 0 || b.BudgetUSD > researchBudgetMaxDailyBudgetUSD {
		return fmt.Errorf("%w: research daily budget must be between 0 and %.0f USD", ErrInvalidRequest, researchBudgetMaxDailyBudgetUSD)
	}
	if b.CostPerQueryUSD < researchBudgetMinCostPerQueryUSD {
		return fmt.Errorf("%w: research cost per query must be at least %.4f USD", ErrInvalidRequest, researchBudgetMinCostPerQueryUSD)
	}
	if b.Location == nil {
		return fmt.Errorf("%w: research budget needs a timezone", ErrInvalidRequest)
	}
	return nil
}

// DayStart is local midnight of now's local day.
func (b DailySearchBudget) DayStart(now time.Time) time.Time {
	local := now.In(b.Location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, b.Location)
}

// MaxQueries is the daily request cap the budget buys.
func (b DailySearchBudget) MaxQueries() int {
	return int(math.Floor(b.BudgetUSD/b.CostPerQueryUSD + 1e-9))
}

// Remaining is how many requests are left after used requests today.
func (b DailySearchBudget) Remaining(used int) int {
	return max(b.MaxQueries()-used, 0)
}

// Bound narrows a tick's configuration to the requests left today. ok is false when nothing is
// left: the tick must not run.
func (b DailySearchBudget) Bound(cfg SchedulerConfig, used int) (SchedulerConfig, bool) {
	remaining := b.Remaining(used)
	if remaining == 0 {
		return cfg, false
	}
	if cfg.MaxSearchRequestsPerTick > remaining {
		cfg.MaxSearchRequestsPerTick = remaining
	}
	if cfg.MaxTopicsPerTick > cfg.MaxSearchRequestsPerTick {
		cfg.MaxTopicsPerTick = cfg.MaxSearchRequestsPerTick
	}
	return cfg, true
}

// NextHourlyRun is the next top of the hour strictly after now, in the budget's timezone (the
// canonical research schedule is "0 * * * *" America/Santiago).
func (b DailySearchBudget) NextHourlyRun(now time.Time) time.Time {
	local := now.In(b.Location)
	top := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, b.Location)
	return top.Add(time.Hour)
}

func envFloat(lookup LookupEnv, key string, fallback float64) (float64, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("%w: %s must be a number", ErrInvalidRequest, key)
	}
	return value, nil
}
