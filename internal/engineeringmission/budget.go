package engineeringmission

import (
	"context"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/costledger"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	"github.com/Mireuz13/explorarte-organization/internal/programbudget"
)

// BudgetAdmission decides whether the program that owns a failed task can
// still absorb one more episode of the work that task was doing.
type BudgetAdmission interface {
	Admit(ctx context.Context, failedTaskID int64) (BudgetVerdict, error)
}

// BudgetVerdict is the admission answer, with enough detail that a refusal is
// as inspectable as an approval.
type BudgetVerdict struct {
	Admitted bool
	// Family, Remaining and Estimated describe the binding constraint: on
	// a refusal, the family that ran out; on an approval, the family with
	// the least room left.
	Family    string
	Remaining modelpricing.USDNanos
	Estimated modelpricing.USDNanos
	// Reason is set when Admitted is false.
	Reason string
}

// ProgramBudgetAdmission admits a recovery episode against what remains of the
// program's own ceiling.
//
// It deliberately does NOT hold a per-episode allowance. A fixed allowance
// would either strand budget the campaign still had, or keep admitting
// episodes after the campaign could no longer pay for them. The question worth
// asking is whether the remaining total can still absorb an episode the size
// of the one that just failed -- and the size of that episode is measured, not
// assumed: it is what the failed task actually spent, per family.
type ProgramBudgetAdmission struct {
	Programs programbudget.Resolver
	Spend    costledger.SpendReader
}

func (a ProgramBudgetAdmission) Admit(ctx context.Context, failedTaskID int64) (BudgetVerdict, error) {
	if a.Spend == nil {
		return BudgetVerdict{}, fmt.Errorf("budget admission requires a spend reader")
	}
	_, correlation, policy, err := a.Programs.Program(ctx, failedTaskID)
	if err != nil {
		return BudgetVerdict{}, err
	}
	// No program, or no policy on its root, means no authority has
	// budgeted this work. Recovery mints new autonomous work, so absence
	// of a ceiling is not permission -- it is a missing ceiling.
	if correlation == "" || policy.SchemaVersion == "" {
		return BudgetVerdict{Reason: "no program budget policy governs this task"}, nil
	}

	verdict := BudgetVerdict{Admitted: true}
	first := true
	for _, family := range policy.Families {
		if family.Unavailable || len(family.ProviderIDs) == 0 || len(family.ModelIDs) == 0 {
			continue
		}
		provider := family.ProviderIDs[0]
		used, err := a.Spend.ProgramFamilySpend(ctx, correlation, provider, family.ModelIDs)
		if err != nil {
			return BudgetVerdict{}, err
		}
		estimated, err := a.Spend.TaskFamilySpend(ctx, failedTaskID, provider, family.ModelIDs)
		if err != nil {
			return BudgetVerdict{}, err
		}
		remaining := family.MaxUSD - used
		if remaining < 0 {
			remaining = 0
		}
		// A family the failed episode never touched cannot constrain its
		// successor: charging it an estimate of zero is the truth, and
		// treating it as binding would refuse recovery over a budget line
		// this work does not use.
		if estimated == 0 {
			continue
		}
		if first || remaining-estimated < verdict.Remaining-verdict.Estimated {
			verdict.Family, verdict.Remaining, verdict.Estimated = family.Key, remaining, estimated
			first = false
		}
		if estimated > remaining {
			return BudgetVerdict{
				Family: family.Key, Remaining: remaining, Estimated: estimated,
				Reason: fmt.Sprintf("family %s has %s left and the failed episode cost %s",
					family.Key, remaining, estimated),
			}, nil
		}
	}
	if first {
		// The failed episode spent nothing in any family the policy
		// governs. Either it never reached a model, or its spend is not
		// attributable here; in both cases there is no measured episode
		// size to admit against.
		return BudgetVerdict{Reason: "the failed episode has no attributable spend to estimate from"}, nil
	}
	return verdict, nil
}

var _ BudgetAdmission = ProgramBudgetAdmission{}
