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
}
