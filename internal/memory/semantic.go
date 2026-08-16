package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	"github.com/Mireuz13/explorarte-organization/internal/contentpolicy"
	"github.com/Mireuz13/explorarte-organization/internal/costledger"
	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

// SemanticSearchDeps mirrors rag.SemanticSearchDeps exactly — see that
// type's doc comment for the full rationale (nil is a fully supported,
// degrade-to-recency configuration; non-nil requires every field together).
// internal/memory cannot import internal/rag (see
// scripts/check-memory-fitness.sh), so this is deliberately duplicated
// rather than shared.
type SemanticSearchDeps struct {
	// InsertVector persists the vector Review computes for a newly-approved
	// entry — unlike rag (where embeddings are filled by a separate async
	// batch process this branch never wires into Manager), memory embeds
	// synchronously and best-effort at approval time. It is a closure
	// rather than a fixed EmbeddingRepository so R30's active embedding
	// profile (Gemini vector(768) vs BGE-M3 vector(1024) — never both) can
	// select, at bootstrap time, which underlying table/metadata shape to
	// write to; this call site only ever supplies what is genuinely
	// per-entry (which entry, its input hash, its vector).
	InsertVector func(ctx context.Context, organizationID, entryID, inputHash string, vector []float32, createdAt time.Time) error
	// LocalComputeOnly mirrors rag.SemanticSearchDeps.LocalComputeOnly
	// exactly — see that field's doc comment.
	LocalComputeOnly      bool
	OnlineAdapter         embeddingruntime.OnlineAdapter
	Pricing               *modelpricing.Service
	Wallet                costledger.Ledger
	Budgets               agentbudget.Ledger
	ProviderID            string
	ProviderModelID       string
	OutputDimensionality  int
	PromptTemplateVersion string
	// Identity mirrors rag.SemanticSearchDeps.Identity exactly.
	Identity EmbeddingIdentity
}

func (d *SemanticSearchDeps) validate() error {
	if d == nil {
		return nil
	}
	if d.InsertVector == nil || d.OnlineAdapter == nil {
		return errors.New("memory semantic search deps require InsertVector and OnlineAdapter")
	}
	if d.LocalComputeOnly {
		if d.Pricing != nil || d.Wallet != nil {
			return errors.New("memory semantic search deps: a local-compute profile must not set Pricing or Wallet")
		}
	} else if d.Pricing == nil || d.Wallet == nil {
		return errors.New("memory semantic search deps require Pricing and Wallet together unless LocalComputeOnly")
	}
	if strings.TrimSpace(d.ProviderID) == "" || strings.TrimSpace(d.ProviderModelID) == "" || d.OutputDimensionality <= 0 || strings.TrimSpace(d.PromptTemplateVersion) == "" {
		return errors.New("memory semantic search deps require provider id, model id, dimension, and prompt template version")
	}
	if err := d.Identity.Validate(); err != nil {
		return fmt.Errorf("memory semantic search deps: %w", err)
	}
	return nil
}

// embed is the shared implementation behind both embedQuery (Search) and
// embedApprovedEntry (Review) — every failure mode degrades to "no vector"
// rather than propagating an error, for the same reason
// rag.Manager.embedQuery does: an embedding is an enrichment, never a hard
// dependency of the operation it's attached to. See that function's doc
// comment for why the deferred status log below is R30's "degraded"
// signal.
func (m *Manager) embed(ctx context.Context, organizationID, actorRoleID, text string, taskID *int64, task embeddingruntime.TaskKind, operation costledger.EmbeddingOperation) (vector []float32) {
	deps := m.semantic
	if deps == nil {
		return nil
	}
	defer func() {
		slog.Default().Info("memory embedding channel status", "provider_id", deps.ProviderID, "provider_model_id", deps.ProviderModelID, "operation", operation, "degraded", vector == nil)
	}()
	assessment := contentpolicy.Analyze(text)
	if assessment.Has(contentpolicy.RiskCredential) || assessment.Has(contentpolicy.RiskClinical) {
		slog.Default().Warn("memory embedding skipped: text matched a forbidden data pattern", "organization_id", organizationID, "operation", operation)
		return nil
	}

	if deps.LocalComputeOnly {
		response, err := deps.OnlineAdapter.Embed(ctx, embeddingruntime.EmbedRequest{
			ProviderID: deps.ProviderID, ProviderModelID: deps.ProviderModelID,
			OutputDimensionality: deps.OutputDimensionality, PromptTemplateVersion: deps.PromptTemplateVersion,
			Items: []embeddingruntime.EmbedItem{{Key: "text", Text: text, Task: task}},
		})
		if err != nil || len(response.Results) != 1 {
			slog.Default().Warn("memory embedding skipped: local compute provider call failed", "error", err, "operation", operation)
			return nil
		}
		return response.Results[0].Vector
	}

	now := time.Now().UTC()
	estimatedTokens := int64(len(text)/4) + 1
	tier, err := deps.Pricing.Resolve(ctx, deps.ProviderID, deps.ProviderModelID, estimatedTokens, modelpricing.BillingOnline, now)
	if err != nil {
		slog.Default().Warn("memory embedding skipped: price resolution failed", "error", err)
		return nil
	}
	estimatedUSD, err := tier.EstimateCost(estimatedTokens, 0, 0, 0)
	if err != nil {
		slog.Default().Warn("memory embedding skipped: cost estimation failed", "error", err)
		return nil
	}

	invocation, err := deps.Wallet.CreateEmbeddingInvocation(ctx, costledger.EmbeddingInvocation{
		OrganizationID: organizationID, ActorRoleID: actorRoleID, TaskID: taskID,
		ProviderID: deps.ProviderID, ProviderModelID: deps.ProviderModelID,
		BillingMode: modelpricing.BillingOnline, Operation: operation,
	})
	if err != nil {
		slog.Default().Warn("memory embedding skipped: could not create embedding invocation", "error", err)
		return nil
	}

	if err := deps.Wallet.ReserveEmbedding(ctx, deps.ProviderID, invocation.ID, estimatedUSD, now); err != nil {
		if errors.Is(err, costledger.ErrInsufficientBalance) {
			slog.Default().Warn("memory embedding skipped: insufficient provider wallet balance", "provider_id", deps.ProviderID)
		} else {
			slog.Default().Warn("memory embedding skipped: wallet reservation failed", "error", err)
		}
		return nil
	}

	if taskID != nil && deps.Budgets != nil {
		budget, err := deps.Budgets.ResolveBudgetForTask(ctx, *taskID)
		if err == nil {
			delta := agentbudget.Usage{UsedUSD: estimatedUSD, UsedTokens: estimatedTokens, UsedModelCalls: 1}
			if err := deps.Budgets.ConsumeModelCall(ctx, budget.ID, invocation.ID, delta, now); err != nil {
				slog.Default().Warn("memory embedding skipped: agent budget exceeded", "error", err)
				if releaseErr := deps.Wallet.ReleaseEmbedding(ctx, deps.ProviderID, invocation.ID, now); releaseErr != nil {
					slog.Default().Error("memory embedding: failed to release wallet reservation after budget rejection", "error", releaseErr)
				}
				return nil
			}
		} else if !errors.Is(err, agentbudget.ErrBudgetNotFound) {
			slog.Default().Warn("memory embedding: budget resolution failed, proceeding without budget tracking", "error", err)
		}
	}

	response, err := deps.OnlineAdapter.Embed(ctx, embeddingruntime.EmbedRequest{
		ProviderID: deps.ProviderID, ProviderModelID: deps.ProviderModelID,
		OutputDimensionality: deps.OutputDimensionality, PromptTemplateVersion: deps.PromptTemplateVersion,
		Items: []embeddingruntime.EmbedItem{{Key: "text", Text: text, Task: task}},
	})
	if err != nil || len(response.Results) != 1 {
		slog.Default().Warn("memory embedding skipped: provider call failed", "error", err)
		if releaseErr := deps.Wallet.ReleaseEmbedding(ctx, deps.ProviderID, invocation.ID, time.Now().UTC()); releaseErr != nil {
			slog.Default().Error("memory embedding: failed to release wallet reservation after provider failure", "error", releaseErr)
		}
		return nil
	}

	actualUSD, err := tier.EstimateCost(response.InputTokens, 0, 0, 0)
	if err != nil {
		actualUSD = estimatedUSD
	}
	if err := deps.Wallet.ReconcileEmbedding(ctx, deps.ProviderID, invocation.ID, actualUSD, time.Now().UTC()); err != nil {
		slog.Default().Error("memory embedding: failed to reconcile wallet reservation", "error", err)
	}
	return response.Results[0].Vector
}

// embedApprovedEntry computes and persists the vector for entry, called
// only after Review has already durably saved the approval — never before,
// so a retried or failed approve never pays for an embedding of content
// that was never actually approved, and a crash between the provider call
// and this insert never leaves a paid-for vector nobody can find (the
// insert is the only thing that makes it queryable at all). Best-effort:
// entry stays approved and searchable-by-recency even if this fails.
func (m *Manager) embedApprovedEntry(ctx context.Context, entry Entry) {
	deps := m.semantic
	if deps == nil {
		return
	}
	vector := m.embed(ctx, entry.OrganizationID, entry.RoleID, entry.Problem+"\n\n"+entry.Correction, nil, embeddingruntime.TaskDocument, costledger.EmbeddingOperationMemoryPropose)
	if vector == nil {
		return
	}
	canonicalHash, err := entry.CanonicalHash()
	if err != nil {
		return
	}
	if err := deps.InsertVector(ctx, entry.OrganizationID, entry.ID, canonicalHash, vector, time.Now().UTC()); err != nil {
		slog.Default().Error("memory embedding: failed to persist entry embedding after a successful embed call", "error", err)
	}
}
