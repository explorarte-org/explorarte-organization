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
	ToolFinanceGetCostSummary    = "finance.get_cost_summary"
	financeGetCostSummaryVersion = "v1"

	// financeCallBreakdownPageSize is the per-page fetch size used to walk
	// ListCallBreakdownsFiltered exhaustively. financeCallBreakdownMaxPages
	// bounds how many pages one provider is scanned for before this handler
	// gives up and reports the result as truncated=true -- a real, honest
	// signal, never a silently incomplete total. At 500 rows/page x 20
	// pages, a provider needs more than 10,000 matching calls before this
	// ever triggers.
	financeCallBreakdownPageSize     = 500
	financeCallBreakdownMaxPages     = 20
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
    "task_id": {
      "type": "integer",
      "minimum": 1,
      "description": "Positive integer task ID to filter spend by. Omit this field when not filtering by task."
    },
    "wallet_provider_id": {
      "type": "string",
      "maxLength": 240,
      "description": "Filter spend by provider identifier (e.g. openai, anthropic). Omit this field when not filtering by provider."
    },
    "provider_model_id": {
      "type": "string",
      "maxLength": 240,
      "description": "Filter spend by provider model ID. Omit this field when not filtering by model."
    },
    "since": {
      "type": "string",
      "description": "RFC3339 timestamp start of time range. Omit this field when not filtering by start time."
    },
    "until": {
      "type": "string",
      "description": "RFC3339 timestamp end of time range. Omit this field when not filtering by end time."
    }
  }
}`)

type financeGetCostSummaryArgs struct {
	TaskID           *int64  `json:"task_id,omitempty"`
	WalletProviderID *string `json:"wallet_provider_id,omitempty"`
	ProviderModelID  *string `json:"provider_model_id,omitempty"`
	Since            *string `json:"since,omitempty"`
	Until            *string `json:"until,omitempty"`
}

type costSummaryView struct {
	// SettledUSD is money the ledger has actually committed (charged) --
	// real spend, never an estimate.
	SettledUSD string `json:"settled_usd"`
	// EstimatedUnsettledUSD is money reserved but not yet committed
	// (Settlement=reserved): a forecast, explicitly labeled as such, never
	// merged into SettledUSD.
	EstimatedUnsettledUSD string `json:"estimated_unsettled_usd"`
	CallsSettled          int    `json:"calls_settled"`
	CallsUnsettled        int    `json:"calls_unsettled"`
	CallsExcluded         int    `json:"calls_excluded,omitempty"`
	// Truncated is true only when a provider had more matching calls than
	// this handler's own safety cap (financeCallBreakdownMaxPages pages)
	// allowed it to scan exhaustively. When true, SettledUSD/
	// EstimatedUnsettledUSD are a partial sum over what WAS scanned, and
	// the caller must not present them as a complete total -- this field
	// exists specifically so that never happens silently.
	// ProvidersOmitted counts real, provisioned providers this call did NOT
	// scan at all because more than financeCallBreakdownMaxProviders exist
	// and no wallet_provider_id was requested. It is always 0 when
	// wallet_provider_id IS set: that path queries the named provider
	// directly and never enumerates or caps the provider list, so a
	// provider "outside the first 50" is never silently skipped just
	// because it would not have survived that cut.
	ProvidersOmitted int             `json:"providers_omitted,omitempty"`
	Truncated        bool            `json:"truncated"`
	ByProvider       []providerSpend `json:"by_provider"`
}

type providerSpend struct {
	WalletProviderID string `json:"wallet_provider_id"`
	SettledUSD       string `json:"settled_usd"`
	EstimatedUSD     string `json:"estimated_unsettled_usd"`
	Truncated        bool   `json:"truncated,omitempty"`
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

// RegisterFinanceTools adds finance.get_cost_summary. It reads exclusively
// through costledger.CallReader.ListCallBreakdownsFiltered -- the
// canonical join of the wallet ledger to the durable model invocation,
// with every filter (task/provider model/time range) applied in SQL
// before any page boundary. When wallet_provider_id IS given, that one
// provider is queried directly. When it is NOT given, the organization's
// provisioned providers (ProvisionedProviderIDs) are enumerated and capped
// to financeCallBreakdownMaxProviders -- a cut that must never apply to a
// caller-named provider, which is exactly why that case bypasses
// enumeration entirely; the cap only ever omits providers nobody asked for
// by name, and doing so always sets providers_omitted/truncated rather
// than silently reporting an incomplete "0" for an ignored provider. Each
// scanned provider is walked page by page until hasMore==false (exhaustive
// for that filter) or a safety page cap is hit, in which case the result
// is explicitly marked truncated rather than silently presented as
// complete. This deliberately does NOT use the plain ListCallBreakdowns (a
// bounded, most-recent-N read meant for recency views like `orgctl cost
// calls`): applying a task/time filter to that method's fixed-size window
// can silently exclude older matching rows, understating real spend
// without any signal that it happened.
func RegisterFinanceTools(registry *ToolRegistry, organizationID string, providers ProvisionedProviderLister, calls costledger.CallReader) error {
	return registry.Register(ToolDescriptor{
		ID: ToolFinanceGetCostSummary, Version: financeGetCostSummaryVersion,
		Description: "Summarize settled and estimated-unsettled provider spend, optionally filtered by task, provider, model, or time range. Exhaustive over the filter unless truncated=true (see providers_omitted for why).",
		InputSchema: financeGetCostSummarySchema, Access: AccessReadOnly, Effect: ToolEffectRead, RequiredRole: CEORoleID,
		Limits:    ToolLimits{MaxResultBytes: 32 << 10, Timeout: 20 * time.Second},
		DataClass: DataClassInternal,
	}, func(body json.RawMessage) error { _, err := decodeFinanceGetCostSummaryArgs(body); return err },
		func(ctx context.Context, _ string, body json.RawMessage) (json.RawMessage, error) {
			args, err := decodeFinanceGetCostSummaryArgs(body)
			if err != nil {
				return nil, err
			}
			since, _ := parseOptionalRFC3339(args.Since)
			until, _ := parseOptionalRFC3339(args.Until)

			filter := costledger.CallBreakdownFilter{Since: since, Until: until}
			if args.TaskID != nil {
				filter.TaskID = *args.TaskID
			}
			if args.ProviderModelID != nil && strings.TrimSpace(*args.ProviderModelID) != "" {
				filter.ProviderModelID = *args.ProviderModelID
			}

			// A specific wallet_provider_id is queried DIRECTLY, never
			// through the enumerate-then-cap path below: capping the
			// provisioned-provider list to financeCallBreakdownMaxProviders
			// must never cause a named, real provider's calls to be
			// silently skipped just because it did not survive that cut.
			var providerIDs []string
			providersOmitted := 0
			if args.WalletProviderID != nil && strings.TrimSpace(*args.WalletProviderID) != "" {
				providerIDs = []string{*args.WalletProviderID}
			} else {
				provisioned, err := providers.ProvisionedProviderIDs(ctx)
				if err != nil {
					return nil, err
				}
				all := make([]string, 0, len(provisioned))
				for id := range provisioned {
					all = append(all, id)
				}
				sort.Strings(all)
				if len(all) > financeCallBreakdownMaxProviders {
					providersOmitted = len(all) - financeCallBreakdownMaxProviders
					all = all[:financeCallBreakdownMaxProviders]
				}
				providerIDs = all
			}

			var settled, estimated modelpricing.USDNanos
			var callsSettled, callsUnsettled, callsExcluded int
			anyTruncated := providersOmitted > 0
			byProvider := make(map[string]*providerSpendAccumulator)

			for _, providerID := range providerIDs {
				accumulator := &providerSpendAccumulator{}
				var cursor costledger.CallBreakdownCursor
				for page := 0; ; page++ {
					if page >= financeCallBreakdownMaxPages {
						accumulator.truncated = true
						anyTruncated = true
						break
					}
					breakdowns, next, hasMore, err := calls.ListCallBreakdownsFiltered(ctx, organizationID, providerID, filter, cursor, financeCallBreakdownPageSize)
					if err != nil {
						return nil, err
					}
					for _, call := range breakdowns {
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
							// nor a live estimate -- correctly excluded from
							// both totals rather than silently counted as
							// either.
							callsExcluded++
						}
					}
					if !hasMore {
						break
					}
					cursor = next
				}
				if accumulator.settled != 0 || accumulator.estimated != 0 || accumulator.truncated {
					byProvider[providerID] = accumulator
				}
			}

			providerViews := make([]providerSpend, 0, len(byProvider))
			for providerID, accumulator := range byProvider {
				providerViews = append(providerViews, providerSpend{
					WalletProviderID: providerID,
					SettledUSD:       accumulator.settled.String(),
					EstimatedUSD:     accumulator.estimated.String(),
					Truncated:        accumulator.truncated,
				})
			}
			sort.Slice(providerViews, func(i, j int) bool { return providerViews[i].WalletProviderID < providerViews[j].WalletProviderID })

			return json.Marshal(costSummaryView{
				SettledUSD: settled.String(), EstimatedUnsettledUSD: estimated.String(),
				CallsSettled: callsSettled, CallsUnsettled: callsUnsettled, CallsExcluded: callsExcluded,
				ProvidersOmitted: providersOmitted, Truncated: anyTruncated, ByProvider: providerViews,
			})
		})
}

type providerSpendAccumulator struct {
	settled   modelpricing.USDNanos
	estimated modelpricing.USDNanos
	truncated bool
}

// ProvisionedProviderLister is the narrow seam finance.get_cost_summary
// needs to enumerate real, in-use providers instead of guessing at a fixed
// provider list. *costledgerpostgres.Store satisfies it directly.
type ProvisionedProviderLister interface {
	ProvisionedProviderIDs(ctx context.Context) (map[string]bool, error)
}
