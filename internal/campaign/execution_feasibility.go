package campaign

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

// Two contracts guard a campaign budget, and they must stay distinct:
//
//	representable (ValidateExecutableBudget): the recommendation converts to
//	  agentbudget.Limits and all seven dimensions are strictly positive.
//	feasible (ValidateExecutionBudgetFeasibility): those limits can fund the
//	  canonical MINIMUM execution -- the shortest tree Executive can drive to a
//	  completed root -- under the host's current facts.
//
// Production proved representable is not enough: Finance recommended
// {max_usd 0.05, max_tokens 10000, max_model_calls 5, max_depth 2,
// max_subagents 1}, every dimension positive, and the first Executive child
// was refused before it reached the provider because its worst-case
// reservation was $0.1602536 (33,268 estimated input tokens plus the full
// 128,000-token output ceiling). Depth 2 could not reach a depth-3 worker and
// one subagent was spent by the first child, so the budget was
// structurally unable to fund the campaign it approved even before price.

// ExecutionBudgetRequirements are the host-derived MINIMUM ceilings a budget
// must reach to fund the canonical minimum execution. They are trusted host
// facts, produced by ExecutionRequirementsProvider from Executive's own
// topology declaration and Model Runtime's own routing/pricing/token rules --
// never by Finance, never by the proposal, and never by Campaign itself.
//
// A minimum is a FLOOR ON THE CEILING, not a prediction of spend: it is the
// smallest ceiling under which the canonical execution is admissible at all
// (each dispatch reserves its worst case before it runs, then settles to the
// actual). A budget may be larger; it may never be smaller.
type ExecutionBudgetRequirements struct {
	MinUSD        modelpricing.USDNanos
	MinTokens     int64
	MinModelCalls int64
	MinWallTimeMS int64
	MinDepth      int64
	MinRetries    int64
	MinSubagents  int64

	// Basis records how the minimums were derived. It is audit provenance
	// only: validation never reads it, and it can neither raise nor lower a
	// minimum.
	Basis ExecutionBudgetRequirementsBasis
}

// ExecutionBudgetRequirementsBasis is the provenance of a requirements value.
type ExecutionBudgetRequirementsBasis struct {
	OrganizationRevisionID int64
	// Stages lists the worst-case dispatch of each unconditional stage of the
	// minimal campaign; MinUSD and MinTokens are the maxima across them.
	Stages []ExecutionStageBasis
	// WallTimeConsumed and RetriesConsumed record whether the canonical
	// Executive path spends that AgentBudget dimension at all. Today neither
	// does (only child allocations consume them and Executive never
	// allocates), so their minimum is just the AgentBudget validity floor of
	// 1 -- an honest statement, not an invented operational floor.
	WallTimeConsumed bool
	RetriesConsumed  bool
}

// ExecutionStageBasis is one stage's worst-case first-dispatch facts.
type ExecutionStageBasis struct {
	Stage                string
	Purpose              string
	Depth                int64
	RoleID               string
	ProviderID           string
	ProviderModelID      string
	PriceTier            string
	InputBytes           int
	EstimatedInputTokens int64
	MaxOutputTokens      int64
	ReservationUSD       modelpricing.USDNanos
	ReservationTokens    int64
}

// ExecutionRequirementsProvider derives the CURRENT requirements for an
// organization from canonical host facts. It is read-only: no provider call,
// no wallet reservation, no AgentBudget consumption, no durable write.
type ExecutionRequirementsProvider interface {
	ExecutionBudgetRequirements(ctx context.Context, organizationID string) (ExecutionBudgetRequirements, error)
}

// FixedExecutionRequirements is an ExecutionRequirementsProvider over an
// already-resolved value, for hosts and tests that hold one.
type FixedExecutionRequirements struct{ Requirements ExecutionBudgetRequirements }

func (f FixedExecutionRequirements) ExecutionBudgetRequirements(context.Context, string) (ExecutionBudgetRequirements, error) {
	return f.Requirements, nil
}

// Validate refuses a requirements value that could not have come from a real
// derivation: every minimum must be strictly positive (a zero floor would
// silently disable that dimension's feasibility check).
func (r ExecutionBudgetRequirements) Validate() error {
	if r.MinUSD <= 0 || r.MinTokens <= 0 || r.MinModelCalls <= 0 || r.MinWallTimeMS <= 0 || r.MinDepth <= 0 || r.MinRetries <= 0 || r.MinSubagents <= 0 {
		return fmt.Errorf("%w: every requirement must be strictly positive", ErrExecutionRequirementsUnavailable)
	}
	return nil
}

// requireExecutionRequirements resolves and validates the current
// requirements, failing closed with ErrExecutionRequirementsUnavailable.
func requireExecutionRequirements(ctx context.Context, provider ExecutionRequirementsProvider, organizationID string) (ExecutionBudgetRequirements, error) {
	if provider == nil {
		return ExecutionBudgetRequirements{}, fmt.Errorf("%w: no requirements provider is configured", ErrExecutionRequirementsUnavailable)
	}
	requirements, err := provider.ExecutionBudgetRequirements(ctx, organizationID)
	if err != nil {
		return ExecutionBudgetRequirements{}, fmt.Errorf("%w: %v", ErrExecutionRequirementsUnavailable, err)
	}
	if err := requirements.Validate(); err != nil {
		return ExecutionBudgetRequirements{}, err
	}
	return requirements, nil
}

// ValidateExecutionBudgetFeasibility reports whether rec is feasible under
// requirements. It runs the representability contract first
// (ErrInvalidExecutionBudget), then compares the EXACT agentbudget.Limits the
// recommendation converts to -- the value Executive would enforce -- against
// every minimum, so what is judged is what would run, not a rounded display
// value. Every shortfall is reported, and errors.Is(err,
// ErrInfeasibleExecutionBudget) holds for any of them.
func ValidateExecutionBudgetFeasibility(rec BudgetRecommendation, requirements ExecutionBudgetRequirements) error {
	limits, err := ToAgentBudgetLimits(rec)
	if err != nil {
		return err
	}
	return ValidateExecutionLimitsFeasibility(limits, requirements)
}

// ValidateExecutionLimitsFeasibility is ValidateExecutionBudgetFeasibility for
// limits that are already AgentBudget-representable.
func ValidateExecutionLimitsFeasibility(limits agentbudget.Limits, requirements ExecutionBudgetRequirements) error {
	if err := requirements.Validate(); err != nil {
		return err
	}
	var shortfalls []string
	if limits.MaxUSD < requirements.MinUSD {
		shortfalls = append(shortfalls, fmt.Sprintf("max_usd %s < required %s", limits.MaxUSD, requirements.MinUSD))
	}
	check := func(name string, have, need int64) {
		if have < need {
			shortfalls = append(shortfalls, fmt.Sprintf("%s %d < required %d", name, have, need))
		}
	}
	check("max_tokens", limits.MaxTokens, requirements.MinTokens)
	check("max_model_calls", limits.MaxModelCalls, requirements.MinModelCalls)
	check("max_wall_time_ms", limits.MaxWallTimeMS, requirements.MinWallTimeMS)
	check("max_depth", limits.MaxDepth, requirements.MinDepth)
	check("max_retries", limits.MaxRetries, requirements.MinRetries)
	check("max_subagents", limits.MaxSubagents, requirements.MinSubagents)
	if len(shortfalls) > 0 {
		return fmt.Errorf("%w: %s", ErrInfeasibleExecutionBudget, strings.Join(shortfalls, "; "))
	}
	return nil
}

// minimumUSDDollars returns the smallest micro-dollar-aligned decimal whose
// conversion through the SAME modelpricing.USDFromDollars every recommended
// budget passes through is not below min. The conversion truncates float64
// products, so printing min/1e9 naively can round-trip one nano short and turn
// a Finance model that copied the floor exactly into an infeasible budget.
func minimumUSDDollars(min modelpricing.USDNanos) string {
	micros := int64(min) / 1_000
	if int64(min)%1_000 != 0 {
		micros++
	}
	for ; micros < math.MaxInt64/1_000; micros++ {
		text := formatMicroDollars(micros)
		dollars, err := parseDollars(text)
		if err != nil {
			continue
		}
		if modelpricing.USDFromDollars(dollars) >= min {
			return text
		}
	}
	return formatMicroDollars(micros)
}

func parseDollars(text string) (float64, error) { return strconv.ParseFloat(text, 64) }

func formatMicroDollars(micros int64) string {
	return fmt.Sprintf("%d.%06d", micros/1_000_000, micros%1_000_000)
}

// renderExecutionBudgetRequirements states the requirements as trusted host
// facts inside the Finance contract instructions. It carries the minimums in
// the exact units and field names of recommended_budget, so there is nothing
// for the model to convert.
func renderExecutionBudgetRequirements(requirements ExecutionBudgetRequirements) string {
	return fmt.Sprintf(`HOST EXECUTION BUDGET FLOOR (TRUSTED HOST FACTS)
The host derived the minimum execution budget below from the current canonical runtime: the executive execution topology, the model routes and prices, and the runtime's own token-reservation rules. It is NOT part of the proposal and the proposal cannot change it.
For verdict "recommended", every field of recommended_budget MUST be greater than or equal to its host minimum:
  max_usd >= %s
  max_tokens >= %d
  max_model_calls >= %d
  max_wall_time_ms >= %d
  max_depth >= %d
  max_retries >= %d
  max_subagents >= %d
You MAY recommend more than a minimum; you may NOT recommend less. Each minimum is the smallest CEILING under which the canonical execution can even begin to run -- every model call reserves its worst case before it runs -- it is not a prediction of what the campaign will actually spend, and it is not a target.
A recommended_budget below any host minimum is rejected before any approval or launch. If, with this floor, you cannot recommend a budget the proposal can justify, do NOT use verdict "recommended".`,
		minimumUSDDollars(requirements.MinUSD), requirements.MinTokens, requirements.MinModelCalls,
		requirements.MinWallTimeMS, requirements.MinDepth, requirements.MinRetries, requirements.MinSubagents)
}
