package ceochat

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/costledger"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

const (
	ToolFinanceGetCostSummary        = "finance.get_cost_summary"
	financeGetCostSummaryVersion     = "v1"
	financeCallBreakdownScanPerCall  = 500
	financeCallBreakdownMaxProviders = 50
)

// finance.get_budget_status is NOT registered this round:
// NOT_IMPLEMENTED_BECAUSE_NO_CANONICAL_SOURCE. internal/costledger and its
// PostgreSQL adapter expose settled/reserved wallet ledger entries
// (CallBreakdown) but no budget/allowance/threshold concept -- no service,
// repository, or table represents "the budget for X is $Y". Registering a
// finance.get_budget_status tool without one would mean fabricating
// financial data or bypassing the canonical service to invent one, both of
// which this round's DATABASE and CANONICAL_SERVICE_RULE sections forbid.
// A future round that adds a real budget/allowance domain should register
// this tool then, against that domain's own service.

var financeGetCostSummarySchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "task_id": {"type": "integer", "minimum": 1},
    "wallet_provider_id": {"type": "string", "maxLength": 240},
    "provider_model_id": {"type": "string", "maxLength": 240},
    "since": {"type": "string", "format": "date-time"},
    "until": {"type": "string", "format": "date-time"},
    "limit": {"type": "integer", "minimum": 1, "maximum": 500}
  }
}`)

type financeGetCostSummaryArgs struct {
	TaskID           *int64  `json:"task_id,omitempty"`
	WalletProviderID *string `json:"wallet_provider_id,omitempty"`
	ProviderModelID  *string `json:"provider_model_id,omitempty"`
	Since            *string `json:"since,omitempty"`
	Until            *string `json:"until,omitempty"`
	Limit            *int    `json:"limit,omitempty"`
}

type costSummaryView struct {
	// SettledUSD is money the ledger has actually committed (charged) --
	// real spend, never an estimate.
	SettledUSD string `json:"settled_usd"`
	// EstimatedUnsettledUSD is money reserved but not yet committed
	// (Settlement=reserved): a forecast, explicitly labeled as such, never
	// merged into SettledUSD.
	EstimatedUnsettledUSD string          `json:"estimated_unsettled_usd"`
	CallsSettled          int             `json:"calls_settled"`
	CallsUnsettled        int             `json:"calls_unsettled"`
	CallsExcluded         int             `json:"calls_excluded,omitempty"`
	ByProvider            []providerSpend `json:"by_provider"`
}

type providerSpend struct {
	WalletProviderID string `json:"wallet_provider_id"`
	SettledUSD       string `json:"settled_usd"`
	EstimatedUSD     string `json:"estimated_unsettled_usd"`
}

func decodeFinanceGetCostSummaryArgs(body json.RawMessage) (financeGetCostSummaryArgs, error) {
	if len(body) == 0 {
		body = []byte("{}")
	}
	var args financeGetCostSummaryArgs
	if err := decodeStrict(body, &args); err != nil {
		return financeGetCostSummaryArgs{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if args.TaskID != nil && *args.TaskID <= 0 {
		return financeGetCostSummaryArgs{}, fmt.Errorf("%w: task_id must be positive", ErrInvalidInput)
	}
	if args.Limit != nil && (*args.Limit < 1 || *args.Limit > financeCallBreakdownScanPerCall) {
		return financeGetCostSummaryArgs{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidInput, financeCallBreakdownScanPerCall)
	}
	since, err := parseOptionalRFC3339(args.Since)
	if err != nil {
		return financeGetCostSummaryArgs{}, err
	}
	until, err := parseOptionalRFC3339(args.Until)
	if err != nil {
		return financeGetCostSummaryArgs{}, err
	}
	if since != nil && until != nil && since.After(*until) {
		return financeGetCostSummaryArgs{}, fmt.Errorf("%w: since must not be after until", ErrInvalidInput)
	}
	return args, nil
}

func parseOptionalRFC3339(raw *string) (*time.Time, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, *raw)
	if err != nil {
		return nil, fmt.Errorf("%w: timestamp %q is not RFC3339", ErrInvalidInput, *raw)
	}
	return &parsed, nil
}

// registerFinanceTools adds finance.get_cost_summary. It reads exclusively
// through costledger.CallReader.ListCallBreakdowns -- the canonical join of
// the wallet ledger to the durable model invocation -- iterating the
// organization's provisioned providers (ProvisionedProviderIDs) rather than
// scanning every provider ID that has ever existed. Every filter
// (task_id/wallet_provider_id/provider_model_id/since/until) is applied
// client-side over that canonical read, never as a second, ad-hoc query
// against the ledger tables.
func RegisterFinanceTools(registry *ToolRegistry, organizationID string, providers ProvisionedProviderLister, calls costledger.CallReader) error {
	return registry.Register(ToolDescriptor{
		ID: ToolFinanceGetCostSummary, Version: financeGetCostSummaryVersion,
		Description: "Summarize settled and estimated-unsettled provider spend, optionally filtered by task, provider, model, or time range.",
		InputSchema: financeGetCostSummarySchema, Access: AccessReadOnly, RequiredRole: CEORoleID,
		Limits:    ToolLimits{MaxResultBytes: 32 << 10, Timeout: 10 * time.Second},
		DataClass: DataClassInternal,
	}, func(body json.RawMessage) error { _, err := decodeFinanceGetCostSummaryArgs(body); return err },
		func(ctx context.Context, _ string, body json.RawMessage) (json.RawMessage, error) {
			args, err := decodeFinanceGetCostSummaryArgs(body)
			if err != nil {
				return nil, err
			}
			since, _ := parseOptionalRFC3339(args.Since)
			until, _ := parseOptionalRFC3339(args.Until)
			scanLimit := financeCallBreakdownScanPerCall
			if args.Limit != nil {
				scanLimit = *args.Limit
			}

			provisioned, err := providers.ProvisionedProviderIDs(ctx)
			if err != nil {
				return nil, err
			}
			providerIDs := make([]string, 0, len(provisioned))
			for id := range provisioned {
				providerIDs = append(providerIDs, id)
			}
			sort.Strings(providerIDs)
			if len(providerIDs) > financeCallBreakdownMaxProviders {
				providerIDs = providerIDs[:financeCallBreakdownMaxProviders]
			}

			var settled, estimated modelpricing.USDNanos
			var callsSettled, callsUnsettled, callsExcluded int
			byProvider := make(map[string]*providerSpendAccumulator)

			for _, providerID := range providerIDs {
				if args.WalletProviderID != nil && *args.WalletProviderID != providerID {
					continue
				}
				breakdowns, err := calls.ListCallBreakdowns(ctx, organizationID, providerID, scanLimit)
				if err != nil {
					return nil, err
				}
				for _, call := range breakdowns {
					if args.TaskID != nil && call.TaskID != *args.TaskID {
						callsExcluded++
						continue
					}
					if args.ProviderModelID != nil && call.ProviderModelID != *args.ProviderModelID {
						callsExcluded++
						continue
					}
					if since != nil && call.InvocationCreatedAt.Before(*since) {
						callsExcluded++
						continue
					}
					if until != nil && call.InvocationCreatedAt.After(*until) {
						callsExcluded++
						continue
					}
					accumulator, ok := byProvider[providerID]
					if !ok {
						accumulator = &providerSpendAccumulator{}
						byProvider[providerID] = accumulator
					}
					switch call.Settlement {
					case costledger.SettlementCommitted:
						settled += call.ChargedUSD
						accumulator.settled += call.ChargedUSD
						callsSettled++
					case costledger.SettlementReserved:
						estimated += call.EstimatedUSD
						accumulator.estimated += call.EstimatedUSD
						callsUnsettled++
					default:
						// Released reservations are neither settled spend
						// nor a live estimate -- they are correctly excluded
						// from both totals rather than silently counted as
						// either.
						callsExcluded++
					}
				}
			}

			providerViews := make([]providerSpend, 0, len(byProvider))
			for providerID, accumulator := range byProvider {
				providerViews = append(providerViews, providerSpend{
					WalletProviderID: providerID,
					SettledUSD:       accumulator.settled.String(),
					EstimatedUSD:     accumulator.estimated.String(),
				})
			}
			sort.Slice(providerViews, func(i, j int) bool { return providerViews[i].WalletProviderID < providerViews[j].WalletProviderID })

			return json.Marshal(costSummaryView{
				SettledUSD: settled.String(), EstimatedUnsettledUSD: estimated.String(),
				CallsSettled: callsSettled, CallsUnsettled: callsUnsettled, CallsExcluded: callsExcluded,
				ByProvider: providerViews,
			})
		})
}

type providerSpendAccumulator struct {
	settled   modelpricing.USDNanos
	estimated modelpricing.USDNanos
}

// ProvisionedProviderLister is the narrow seam finance.get_cost_summary
// needs to enumerate real, in-use providers instead of guessing at a fixed
// provider list. *costledgerpostgres.Store satisfies it directly.
type ProvisionedProviderLister interface {
	ProvisionedProviderIDs(ctx context.Context) (map[string]bool, error)
}
