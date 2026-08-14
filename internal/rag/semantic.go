package rag

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	"github.com/Mireuz13/explorarte-organization/internal/costledger"
	"github.com/Mireuz13/explorarte-organization/internal/dataclassifier"
	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

// SemanticSearchDeps bundles everything Query/BackfillChunkEmbeddings need
// to add a vector retrieval channel on top of the always-available
// lexical/exact channels. A nil SemanticSearchDeps (the zero value for
// *SemanticSearchDeps) is a fully supported configuration — Query
// degrades to exact+lexical only, exactly as it behaved before this
// dependency existed. When non-nil, every field is required (validated in
// NewManager): there is no partial configuration, since a wallet without
// pricing or an adapter without a wallet gate would each be a way to
// spend money without control.
type SemanticSearchDeps struct {
	OnlineAdapter embeddingruntime.OnlineAdapter
	// LocalComputeOnly marks a profile that runs on local, unbilled
	// compute (BGE-M3) — Pricing/Wallet are never consulted for it, and
	// must not be set: forcing a local process through the monetary
	// ledger (even at a seeded $0 price) would distort that ledger and
	// give the local profile a wallet-insufficient-balance failure mode
	// it structurally cannot have. The adapter's own resource bounds
	// (MaxConcurrency/MaxQueueDepth — see internal/embeddingruntime/
	// adapter/bgem3) are the entire budget for this profile; its call
	// counters/latency (Adapter.Metrics) are the accounting, not a wallet
	// event.
	LocalComputeOnly      bool
	Pricing               *modelpricing.Service
	Wallet                costledger.Ledger
	Budgets               agentbudget.Ledger // independently optional — nil means budget tracking is skipped even though cost gating is not, same rule dispatch already follows for chat
	ProviderID            string
	ProviderModelID       string
	OutputDimensionality  int
	PromptTemplateVersion string
	// Identity is the full vector-space identity every row this profile
	// writes/queries carries — see EmbeddingIdentity's doc comment.
	Identity EmbeddingIdentity
}

func (d *SemanticSearchDeps) validate() error {
	if d == nil {
		return nil
	}
	if d.OnlineAdapter == nil {
		return errors.New("rag semantic search deps require OnlineAdapter")
	}
	if d.LocalComputeOnly {
		if d.Pricing != nil || d.Wallet != nil {
			return errors.New("rag semantic search deps: a local-compute profile must not set Pricing or Wallet")
		}
	} else if d.Pricing == nil || d.Wallet == nil {
		return errors.New("rag semantic search deps require Pricing and Wallet together unless LocalComputeOnly")
	}
	if strings.TrimSpace(d.ProviderID) == "" || strings.TrimSpace(d.ProviderModelID) == "" || d.OutputDimensionality <= 0 || strings.TrimSpace(d.PromptTemplateVersion) == "" {
		return errors.New("rag semantic search deps require provider id, model id, dimension, and prompt template version")
	}
	if err := d.Identity.Validate(); err != nil {
		return fmt.Errorf("rag semantic search deps: %w", err)
	}
	return nil
}

// embedQuery embeds queryText for the vector retrieval channel — a thin
// wrapper over embed with the query-specific task kind/operation. See
// embed's doc comment for the full degradation/accounting behavior.
func (m *Manager) embedQuery(ctx context.Context, organizationID, actorRoleID, queryText string, taskID *int64) []float32 {
	return m.embed(ctx, organizationID, actorRoleID, queryText, taskID, embeddingruntime.TaskQuery, costledger.EmbeddingOperationRAGQuery)
}

// embed attempts to embed text for either the query-time vector channel
// (embedQuery) or document-time backfill (BackfillChunkEmbeddings) — a
// thin wrapper over embedItem for the plain-text case. See embedItem's
// doc comment for the full degradation/accounting behavior.
func (m *Manager) embed(ctx context.Context, organizationID, actorRoleID, text string, taskID *int64, task embeddingruntime.TaskKind, operation costledger.EmbeddingOperation) []float32 {
	return m.embedItem(ctx, organizationID, actorRoleID, text, embeddingruntime.EmbedItem{Text: text, Task: task}, estimateTextTokens(text), taskID, operation)
}

// embedMedia embeds inline binary content (a single PDF page, an image,
// ...) for a media-backed chunk (see rag_knowledge_chunks.media_source_ref)
// — a thin wrapper over embedItem for the multimodal case. classifierText
// is the SAME page's already-extracted text (chunk.Content) even though it
// is not what gets sent to the provider: dataclassifier.Detect only
// understands text patterns, so scanning the extracted text is the only
// secret defense-in-depth check available for a media item —
// there is no equivalent scan of the raw bytes. This is a real, documented
// gap, not a silently-assumed equivalence: a secret that exists only in a
// PDF's visual content (e.g. a screenshot of a credential) with no
// corresponding extractable text would not be caught here.
func (m *Manager) embedMedia(ctx context.Context, organizationID, actorRoleID, classifierText, mimeType string, data []byte, taskID *int64, task embeddingruntime.TaskKind, operation costledger.EmbeddingOperation) []float32 {
	return m.embedItem(ctx, organizationID, actorRoleID, classifierText, embeddingruntime.EmbedItem{MimeType: mimeType, Data: data, Task: task}, estimateMediaTokens(mimeType, classifierText), taskID, operation)
}

// estimateTextTokens is the same rough token/USD-reservation proxy this
// package always used for text (~4 bytes/token) — only ever a pre-call
// reservation estimate, reconciled against the provider's own reported
// InputTokens after the call succeeds (see the ReconcileEmbedding call
// below), so overestimating costs nothing but a slightly larger transient
// wallet hold.
func estimateTextTokens(text string) int64 { return int64(len(text)/4) + 1 }

// estimateMediaTokens is a conservative, documented-approximate proxy for
// gemini-embedding-2's multimodal token cost: Google's docs state a
// rendered PDF page or image consumes 258 tokens; a PDF page also carries
// its extracted text alongside that rendering, so this adds the same
// text-based estimate on top for that case. Audio/video duration cannot be
// derived from byte length alone without parsing the container format
// this package does not implement, so those two modalities use a flat,
// deliberately generous placeholder — safe for the same reason
// estimateTextTokens's approximation is safe: reconciled to the real
// figure afterward, never the final billed amount.
func estimateMediaTokens(mimeType, extractedText string) int64 {
	const renderedPageTokens = 258
	const audioVideoPlaceholderTokens = 1000
	switch mimeType {
	case "application/pdf", "image/png", "image/jpeg":
		return renderedPageTokens + estimateTextTokens(extractedText)
	case "audio/mpeg", "audio/wav", "video/mp4", "video/quicktime":
		return audioVideoPlaceholderTokens
	default:
		return renderedPageTokens
	}
}

// embedItem is the shared core behind embed (text) and embedMedia (inline
// binary): every failure mode — content classified as secret,
// insufficient wallet balance, exceeded agent budget, or a provider/
// adapter error — degrades to "no vector this time" rather than failing
// the caller outright: embeddings enrich retrieval, they are not a hard
// dependency of it. Each skip is logged at Warn so the condition stays
// observable without breaking the caller for a transient provider issue.
// A single structured status line on exit (see the deferred log below) is
// R30's "degraded" metric: whichever profile is active (deps.ProviderID),
// this is the one place that ever knows, in one call, whether the vector
// channel actually ran — never silently. item's Key/Task are set by this
// function; the caller only needs to populate Text or MimeType+Data.
func (m *Manager) embedItem(ctx context.Context, organizationID, actorRoleID, classifierText string, item embeddingruntime.EmbedItem, estimatedTokens int64, taskID *int64, operation costledger.EmbeddingOperation) (vector []float32) {
	deps := m.semantic
	if deps == nil {
		return nil
	}
	item.Key = "item"
	defer func() {
		slog.Default().Info("rag embedding channel status", "provider_id", deps.ProviderID, "provider_model_id", deps.ProviderModelID, "operation", operation, "degraded", vector == nil)
	}()
	// ARCH-BOUNDARY-001: Organization's RAG domain has no clinical data
	// concept — see cmd/orgctl/rag.go's runRAGIngestPDF and
	// internal/rag/validation.go's KnowledgeVersion.Validate for the full
	// rationale, both already scoped to finding.Secret only. This was the
	// third call site into dataclassifier.Detect on RAG content and was
	// missed in that fix: it kept using finding.Any() (Secret OR
	// Clinical), silently degrading embeddings for any chunk whose
	// extracted text happened to contain ordinary engineering vocabulary
	// overlapping the clinical word list (RAG-EMBED-COMPLETENESS-001).
	// Secret-shaped content stays a real Organization concern and is
	// still checked; clinical vocabulary is not.
	if finding := dataclassifier.Detect(classifierText); finding.Secret {
		slog.Default().Warn("rag embedding skipped: text matched a forbidden data pattern", "organization_id", organizationID, "operation", operation)
		return nil
	}

	if deps.LocalComputeOnly {
		if item.IsMedia() {
			slog.Default().Warn("rag embedding skipped: local compute profile does not support media items", "operation", operation)
			return nil
		}
		response, err := deps.OnlineAdapter.Embed(ctx, embeddingruntime.EmbedRequest{
			ProviderID: deps.ProviderID, ProviderModelID: deps.ProviderModelID,
			OutputDimensionality: deps.OutputDimensionality, PromptTemplateVersion: deps.PromptTemplateVersion,
			Items: []embeddingruntime.EmbedItem{item},
		})
		if err != nil || len(response.Results) != 1 {
			slog.Default().Warn("rag embedding skipped: local compute provider call failed", "error", err, "operation", operation)
			return nil
		}
		return response.Results[0].Vector
	}

	now := time.Now().UTC()
	tier, err := deps.Pricing.Resolve(ctx, deps.ProviderID, deps.ProviderModelID, estimatedTokens, modelpricing.BillingOnline, now)
	if err != nil {
		slog.Default().Warn("rag embedding skipped: price resolution failed", "error", err, "operation", operation)
		return nil
	}
	estimatedUSD, err := tier.EstimateCost(estimatedTokens, 0, 0, 0)
	if err != nil {
		slog.Default().Warn("rag embedding skipped: cost estimation failed", "error", err, "operation", operation)
		return nil
	}

	invocation, err := deps.Wallet.CreateEmbeddingInvocation(ctx, costledger.EmbeddingInvocation{
		OrganizationID: organizationID, ActorRoleID: actorRoleID, TaskID: taskID,
		ProviderID: deps.ProviderID, ProviderModelID: deps.ProviderModelID,
		BillingMode: modelpricing.BillingOnline, Operation: operation,
	})
	if err != nil {
		slog.Default().Warn("rag embedding skipped: could not create embedding invocation", "error", err, "operation", operation)
		return nil
	}

	if err := deps.Wallet.ReserveEmbedding(ctx, deps.ProviderID, invocation.ID, estimatedUSD, now); err != nil {
		if errors.Is(err, costledger.ErrInsufficientBalance) {
			slog.Default().Warn("rag embedding skipped: insufficient provider wallet balance", "provider_id", deps.ProviderID, "operation", operation)
		} else {
			slog.Default().Warn("rag embedding skipped: wallet reservation failed", "error", err, "operation", operation)
		}
		return nil
	}

	if taskID != nil && deps.Budgets != nil {
		budget, err := deps.Budgets.ResolveBudgetForTask(ctx, *taskID)
		if err == nil {
			delta := agentbudget.Usage{UsedUSD: estimatedUSD, UsedTokens: estimatedTokens, UsedModelCalls: 1}
			if err := deps.Budgets.ConsumeModelCall(ctx, budget.ID, invocation.ID, delta, now); err != nil {
				slog.Default().Warn("rag embedding skipped: agent budget exceeded", "error", err, "operation", operation)
				if releaseErr := deps.Wallet.ReleaseEmbedding(ctx, deps.ProviderID, invocation.ID, now); releaseErr != nil {
					slog.Default().Error("rag embedding: failed to release wallet reservation after budget rejection", "error", releaseErr, "operation", operation)
				}
				return nil
			}
		} else if !errors.Is(err, agentbudget.ErrBudgetNotFound) {
			slog.Default().Warn("rag embedding: budget resolution failed, proceeding without budget tracking", "error", err, "operation", operation)
		}
	}

	response, err := deps.OnlineAdapter.Embed(ctx, embeddingruntime.EmbedRequest{
		ProviderID: deps.ProviderID, ProviderModelID: deps.ProviderModelID,
		OutputDimensionality: deps.OutputDimensionality, PromptTemplateVersion: deps.PromptTemplateVersion,
		Items: []embeddingruntime.EmbedItem{item},
	})
	if err != nil || len(response.Results) != 1 {
		slog.Default().Warn("rag embedding skipped: provider call failed", "error", err, "operation", operation)
		if releaseErr := deps.Wallet.ReleaseEmbedding(ctx, deps.ProviderID, invocation.ID, time.Now().UTC()); releaseErr != nil {
			slog.Default().Error("rag embedding: failed to release wallet reservation after provider failure", "error", releaseErr, "operation", operation)
		}
		return nil
	}

	actualUSD, err := tier.EstimateCost(response.InputTokens, 0, 0, 0)
	if err != nil {
		actualUSD = estimatedUSD
	}
	if err := deps.Wallet.ReconcileEmbedding(ctx, deps.ProviderID, invocation.ID, actualUSD, time.Now().UTC()); err != nil {
		// The embedding already succeeded and will still be used — a
		// reconciliation failure is a ledger bookkeeping problem to fix
		// out of band (see ListOrphanedReservations), not a reason to
		// discard a vector already paid for and already have.
		slog.Default().Error("rag embedding: failed to reconcile wallet reservation", "error", err, "operation", operation)
	}
	return response.Results[0].Vector
}
