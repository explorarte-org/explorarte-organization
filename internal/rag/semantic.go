package rag

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	"github.com/Mireuz13/explorarte-organization/internal/costledger"
	"github.com/Mireuz13/explorarte-organization/internal/dataclassifier"
	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

// SemanticSearchDeps bundles everything Query needs to add a vector
// retrieval channel on top of the always-available lexical/exact channels.
// A nil SemanticSearchDeps (the zero value for *SemanticSearchDeps) is a
// fully supported configuration — Query degrades to exact+lexical only,
// exactly as it behaved before this dependency existed. When non-nil,
// every field is required (validated in NewManager): there is no partial
// configuration, since a wallet without pricing or an adapter without a
// wallet gate would each be a way to spend money without control.
type SemanticSearchDeps struct {
	OnlineAdapter         embeddingruntime.OnlineAdapter
	Pricing               *modelpricing.Service
	Wallet                costledger.Ledger
	Budgets               agentbudget.Ledger // independently optional — nil means budget tracking is skipped even though cost gating is not, same rule dispatch already follows for chat
	ProviderID            string
	ProviderModelID       string
	OutputDimensionality  int
	PromptTemplateVersion string
}

func (d *SemanticSearchDeps) validate() error {
	if d == nil {
		return nil
	}
	if d.OnlineAdapter == nil || d.Pricing == nil || d.Wallet == nil {
		return errors.New("rag semantic search deps require OnlineAdapter, Pricing, and Wallet together")
	}
	if strings.TrimSpace(d.ProviderID) == "" || strings.TrimSpace(d.ProviderModelID) == "" || d.OutputDimensionality <= 0 || strings.TrimSpace(d.PromptTemplateVersion) == "" {
		return errors.New("rag semantic search deps require provider id, model id, dimension, and prompt template version")
	}
	return nil
}

// embedQuery attempts to embed queryText for the vector retrieval channel.
// Every failure mode — content classified as secret/clinical, insufficient
// wallet balance, exceeded agent budget, or a provider/adapter error —
// degrades to "no vector channel this time" rather than failing Query
// outright: embeddings enrich retrieval, they are not a hard dependency of
// it, and Query worked without them before this package existed. Each skip
// is logged at Warn so the condition stays observable without breaking
// retrieval for a caller who has no way to fix a transient provider issue.
func (m *Manager) embedQuery(ctx context.Context, organizationID, actorRoleID, queryText string, taskID *int64) []float32 {
	deps := m.semantic
	if deps == nil {
		return nil
	}
	if finding := dataclassifier.Detect(queryText); finding.Any() {
		slog.Default().Warn("rag query embedding skipped: query text matched a forbidden data pattern", "organization_id", organizationID)
		return nil
	}

	now := time.Now().UTC()
	estimatedTokens := int64(len(queryText)/4) + 1
	tier, err := deps.Pricing.Resolve(ctx, deps.ProviderID, deps.ProviderModelID, estimatedTokens, modelpricing.BillingOnline, now)
	if err != nil {
		slog.Default().Warn("rag query embedding skipped: price resolution failed", "error", err)
		return nil
	}
	estimatedUSD, err := tier.EstimateCost(estimatedTokens, 0, 0, 0)
	if err != nil {
		slog.Default().Warn("rag query embedding skipped: cost estimation failed", "error", err)
		return nil
	}

	invocation, err := deps.Wallet.CreateEmbeddingInvocation(ctx, costledger.EmbeddingInvocation{
		OrganizationID: organizationID, ActorRoleID: actorRoleID, TaskID: taskID,
		ProviderID: deps.ProviderID, ProviderModelID: deps.ProviderModelID,
		BillingMode: modelpricing.BillingOnline, Operation: costledger.EmbeddingOperationRAGQuery,
	})
	if err != nil {
		slog.Default().Warn("rag query embedding skipped: could not create embedding invocation", "error", err)
		return nil
	}

	if err := deps.Wallet.ReserveEmbedding(ctx, deps.ProviderID, invocation.ID, estimatedUSD, now); err != nil {
		if errors.Is(err, costledger.ErrInsufficientBalance) {
			slog.Default().Warn("rag query embedding skipped: insufficient provider wallet balance", "provider_id", deps.ProviderID)
		} else {
			slog.Default().Warn("rag query embedding skipped: wallet reservation failed", "error", err)
		}
		return nil
	}

	if taskID != nil && deps.Budgets != nil {
		budget, err := deps.Budgets.ResolveBudgetForTask(ctx, *taskID)
		if err == nil {
			delta := agentbudget.Usage{UsedUSD: estimatedUSD, UsedTokens: estimatedTokens, UsedModelCalls: 1}
			if err := deps.Budgets.ConsumeModelCall(ctx, budget.ID, invocation.ID, delta, now); err != nil {
				slog.Default().Warn("rag query embedding skipped: agent budget exceeded", "error", err)
				if releaseErr := deps.Wallet.ReleaseEmbedding(ctx, deps.ProviderID, invocation.ID, now); releaseErr != nil {
					slog.Default().Error("rag query embedding: failed to release wallet reservation after budget rejection", "error", releaseErr)
				}
				return nil
			}
		} else if !errors.Is(err, agentbudget.ErrBudgetNotFound) {
			slog.Default().Warn("rag query embedding: budget resolution failed, proceeding without budget tracking", "error", err)
		}
	}

	response, err := deps.OnlineAdapter.Embed(ctx, embeddingruntime.EmbedRequest{
		ProviderID: deps.ProviderID, ProviderModelID: deps.ProviderModelID,
		OutputDimensionality: deps.OutputDimensionality, PromptTemplateVersion: deps.PromptTemplateVersion,
		Items: []embeddingruntime.EmbedItem{{Key: "query", Text: queryText, Task: embeddingruntime.TaskQuery}},
	})
	if err != nil || len(response.Results) != 1 {
		slog.Default().Warn("rag query embedding skipped: provider call failed", "error", err)
		if releaseErr := deps.Wallet.ReleaseEmbedding(ctx, deps.ProviderID, invocation.ID, time.Now().UTC()); releaseErr != nil {
			slog.Default().Error("rag query embedding: failed to release wallet reservation after provider failure", "error", releaseErr)
		}
		return nil
	}

	actualUSD, err := tier.EstimateCost(response.InputTokens, 0, 0, 0)
	if err != nil {
		actualUSD = estimatedUSD
	}
	if err := deps.Wallet.ReconcileEmbedding(ctx, deps.ProviderID, invocation.ID, actualUSD, time.Now().UTC()); err != nil {
		// The embedding already succeeded and will still be used for this
		// query — a reconciliation failure is a ledger bookkeeping problem
		// to fix out of band (see ListOrphanedReservations), not a reason
		// to discard a vector we already paid for and already have.
		slog.Default().Error("rag query embedding: failed to reconcile wallet reservation", "error", err)
	}
	return response.Results[0].Vector
}
