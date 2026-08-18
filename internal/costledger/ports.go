package costledger

import (
	"context"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

// Ledger is the transactional boundary for provider wallets. Reserve and
// Reconcile/Release are idempotent per (providerID, invocationID): a retried
// call after a crash between the reservation write and its caller observing
// success must not double-reserve or double-debit.
type Ledger interface {
	GetWallet(ctx context.Context, providerID string) (ProviderWallet, error)
	SetBalance(ctx context.Context, providerID string, balanceUSD modelpricing.USDNanos, now time.Time) (ProviderWallet, error)
	Reserve(ctx context.Context, providerID string, invocationID int64, estimatedUSD modelpricing.USDNanos, now time.Time) error
	Reconcile(ctx context.Context, providerID string, invocationID int64, actualUSD modelpricing.USDNanos, now time.Time) error
	Release(ctx context.Context, providerID string, invocationID int64, now time.Time) error
	// ListEvents returns a provider's most recent wallet events (reserved,
	// committed, released), newest first, for auditing and the CLI.
	ListEvents(ctx context.Context, providerID string, limit int) ([]WalletEvent, error)
	// ListOrphanedReservations returns "reserved" events created before
	// olderThan that never reached a terminal event (committed or
	// released), oldest first. A dispatch that crashes or is killed
	// between reserving and settling (see modelruntime's
	// AdapterFailureAmbiguous handling, which deliberately leaves an
	// ambiguous outcome's reservation parked rather than releasing it)
	// leaves exactly this kind of row behind. This is read-only: deciding
	// whether to commit or release an orphan is a financial-policy
	// judgment call for a human or an explicit reconciliation job, not
	// something this method does on its own.
	ListOrphanedReservations(ctx context.Context, olderThan time.Time, limit int) ([]WalletEvent, error)

	// CreateEmbeddingInvocation persists the identity row an embedding
	// call's wallet events (ReserveEmbedding/ReconcileEmbedding/
	// ReleaseEmbedding) attach to. It must be called before ReserveEmbedding
	// — there is no "reserve first, attribute later" path for embeddings
	// the way chat's model_invocations already exists before Reserve runs.
	CreateEmbeddingInvocation(ctx context.Context, invocation EmbeddingInvocation) (EmbeddingInvocation, error)
	// ReserveEmbedding/ReconcileEmbedding/ReleaseEmbedding mirror
	// Reserve/Reconcile/Release exactly (same idempotency, same
	// insufficient-balance/already-terminal semantics), keyed by
	// embeddingInvocationID against the same provider_wallets row instead
	// of invocationID — an embedding call and a chat call against the same
	// provider draw from one real dollar balance.
	ReserveEmbedding(ctx context.Context, providerID string, embeddingInvocationID int64, estimatedUSD modelpricing.USDNanos, now time.Time) error
	ReconcileEmbedding(ctx context.Context, providerID string, embeddingInvocationID int64, actualUSD modelpricing.USDNanos, now time.Time) error
	ReleaseEmbedding(ctx context.Context, providerID string, embeddingInvocationID int64, now time.Time) error
}

type ProgramReservation struct {
	ProviderID      string
	ProviderModelID string
	InvocationID    int64
	CorrelationID   string
	MaxUSD          modelpricing.USDNanos
	EstimatedUSD    modelpricing.USDNanos
}
type ProgramScopedReserver interface {
	ReserveWithinProgramCeiling(context.Context, ProgramReservation, time.Time) error
}

// CallReader is the read-only attribution view used by operator tooling. It is
// separate from Ledger so the dispatch path depends only on wallet mutations,
// not on reporting queries.
type CallReader interface {
	ListCallBreakdowns(ctx context.Context, organizationID, providerID string, limit int) ([]CallBreakdown, error)
}

// PendingReconciliationMarker is an optional capability of a Ledger backend:
// annotate an existing, still-open 'reserved' wallet event as
// financial_outcome=estimated_pending_reconciliation / cost_provenance=
// estimated_locally, without releasing or committing it — used when the
// provider's receipt of the call is certain or likely (an ambiguous
// transport outcome, a response was received but no real usage could be
// recovered from it) but the true cost is unknown, so the reservation must
// stay parked at its conservative estimate rather than be freed as if the
// call were free.
//
// Deliberately NOT part of Ledger itself: several existing fakes (e.g.
// internal/rag/semantic_test.go, internal/memory/semantic_test.go) only
// exercise the embedding path and implement Ledger directly — adding a
// required method there would force unrelated packages outside this
// change's scope to grow a no-op stub. Callers that need this capability
// (internal/modelruntime/costgate) type-assert for it.
//
// Idempotent: annotating an already-annotated row is a no-op success,
// matching Reconcile/Release's own idempotency. Returns ErrReservationNotFound
// if no 'reserved' event exists for (providerID, invocationID).
type PendingReconciliationMarker interface {
	MarkPendingReconciliation(ctx context.Context, providerID string, invocationID int64, now time.Time) error
}

// SubscriptionRecorder is an optional capability of a Ledger backend for
// providers billed via a fixed subscription/token-plan (e.g. mimo) rather
// than pay-as-you-go: record, once, that a call reached and was processed
// by the provider (real resource/quota consumption) WITHOUT any real per-
// call USD price to reserve/reconcile against — no PriceTier exists for
// these providers and none is fabricated (see
// modelruntime.CostReservation.Subscription's doc comment).
//
// Deliberately NOT part of Ledger itself, for the same reason
// PendingReconciliationMarker isn't: several existing fakes implement
// Ledger directly without ever exercising a subscription-billed provider,
// and a required method would force them to grow a no-op stub.
// internal/modelruntime/costgate type-asserts for it.
//
// Idempotent per (providerID, invocationID): recording an already-recorded
// call is a no-op success. amount_usd_nanos is always 0 for the row this
// writes, but never bare/unexplained — it is always written together with
// a non-null, distinct cost_provenance/financial_outcome pair that makes
// "0 because this is subscription-billed, not $0 because it was free"
// unambiguous to any reader of provider_wallet_events.
type SubscriptionRecorder interface {
	RecordSubscriptionConsumption(ctx context.Context, providerID string, invocationID int64, now time.Time) error
}
