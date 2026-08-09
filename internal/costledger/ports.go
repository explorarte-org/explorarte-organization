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
}
