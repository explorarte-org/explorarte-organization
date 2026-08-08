package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/costledger"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func New(store *platformpostgres.Store) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("costledger store requires initialized PostgreSQL")
	}
	return &Store{pool: store.Pool()}, nil
}

var _ costledger.Ledger = (*Store)(nil)

func (s *Store) GetWallet(ctx context.Context, providerID string) (costledger.ProviderWallet, error) {
	var wallet costledger.ProviderWallet
	wallet.ProviderID = providerID
	err := s.pool.QueryRow(ctx, `
SELECT balance_usd_nanos, reserved_usd_nanos, version, updated_at
FROM provider_wallets WHERE provider_id=$1`, providerID).
		Scan(&wallet.BalanceUSD, &wallet.ReservedUSD, &wallet.Version, &wallet.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return costledger.ProviderWallet{}, costledger.ErrWalletNotFound
		}
		return costledger.ProviderWallet{}, err
	}
	wallet.UpdatedAt = wallet.UpdatedAt.UTC()
	return wallet, nil
}

func (s *Store) SetBalance(ctx context.Context, providerID string, balanceUSD modelpricing.USDNanos, now time.Time) (costledger.ProviderWallet, error) {
	now = now.UTC()
	var wallet costledger.ProviderWallet
	wallet.ProviderID = providerID
	err := s.pool.QueryRow(ctx, `
INSERT INTO provider_wallets (provider_id, balance_usd_nanos, reserved_usd_nanos, version, updated_at)
VALUES ($1,$2,0,1,$3)
ON CONFLICT (provider_id) DO UPDATE SET
    balance_usd_nanos = EXCLUDED.balance_usd_nanos,
    version = provider_wallets.version + 1,
    updated_at = EXCLUDED.updated_at
WHERE provider_wallets.reserved_usd_nanos <= EXCLUDED.balance_usd_nanos
RETURNING balance_usd_nanos, reserved_usd_nanos, version, updated_at`,
		providerID, int64(balanceUSD), now).
		Scan(&wallet.BalanceUSD, &wallet.ReservedUSD, &wallet.Version, &wallet.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return costledger.ProviderWallet{}, fmt.Errorf("%w: new balance is below the amount already reserved", costledger.ErrInvalidRequest)
		}
		return costledger.ProviderWallet{}, err
	}
	wallet.UpdatedAt = wallet.UpdatedAt.UTC()
	return wallet, nil
}

func (s *Store) Reserve(ctx context.Context, providerID string, invocationID int64, estimatedUSD modelpricing.USDNanos, now time.Time) error {
	if estimatedUSD < 0 {
		return fmt.Errorf("%w: estimated cost must be non-negative", costledger.ErrInvalidRequest)
	}
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reserve: %w", err)
	}
	defer tx.Rollback(ctx)

	var balance, reserved int64
	if err := tx.QueryRow(ctx, `SELECT balance_usd_nanos, reserved_usd_nanos FROM provider_wallets WHERE provider_id=$1 FOR UPDATE`, providerID).Scan(&balance, &reserved); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return costledger.ErrWalletNotFound
		}
		return err
	}

	tag, err := tx.Exec(ctx, `
INSERT INTO provider_wallet_events (provider_id, invocation_id, kind, amount_usd_nanos, created_at)
VALUES ($1,$2,'reserved',$3,$4)
ON CONFLICT (provider_id, invocation_id, kind) DO NOTHING`,
		providerID, invocationID, int64(estimatedUSD), now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Already reserved by an earlier attempt of this same invocation —
		// idempotent no-op, not a fresh reservation to apply again.
		return tx.Commit(ctx)
	}

	if balance-reserved < int64(estimatedUSD) {
		return costledger.ErrInsufficientBalance
	}
	if _, err := tx.Exec(ctx, `UPDATE provider_wallets SET reserved_usd_nanos = reserved_usd_nanos + $1, version = version + 1, updated_at=$2 WHERE provider_id=$3`, int64(estimatedUSD), now, providerID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Reconcile(ctx context.Context, providerID string, invocationID int64, actualUSD modelpricing.USDNanos, now time.Time) error {
	if actualUSD < 0 {
		return fmt.Errorf("%w: actual cost must be non-negative", costledger.ErrInvalidRequest)
	}
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reconcile: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, _, err := lockWalletAndReservation(ctx, tx, providerID); err != nil {
		return err
	}
	var reservedAmount int64
	if err := tx.QueryRow(ctx, `SELECT amount_usd_nanos FROM provider_wallet_events WHERE provider_id=$1 AND invocation_id=$2 AND kind='reserved'`, providerID, invocationID).Scan(&reservedAmount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return costledger.ErrReservationNotFound
		}
		return err
	}

	tag, err := tx.Exec(ctx, `
INSERT INTO provider_wallet_events (provider_id, invocation_id, kind, amount_usd_nanos, created_at)
VALUES ($1,$2,'committed',$3,$4)
ON CONFLICT (provider_id, invocation_id, kind) DO NOTHING`,
		providerID, invocationID, int64(actualUSD), now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, `
UPDATE provider_wallets SET
    balance_usd_nanos = balance_usd_nanos - $1,
    reserved_usd_nanos = reserved_usd_nanos - $2,
    version = version + 1, updated_at=$3
WHERE provider_id=$4`, int64(actualUSD), reservedAmount, now, providerID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Release(ctx context.Context, providerID string, invocationID int64, now time.Time) error {
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin release: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, _, err := lockWalletAndReservation(ctx, tx, providerID); err != nil {
		return err
	}
	var reservedAmount int64
	if err := tx.QueryRow(ctx, `SELECT amount_usd_nanos FROM provider_wallet_events WHERE provider_id=$1 AND invocation_id=$2 AND kind='reserved'`, providerID, invocationID).Scan(&reservedAmount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return costledger.ErrReservationNotFound
		}
		return err
	}

	tag, err := tx.Exec(ctx, `
INSERT INTO provider_wallet_events (provider_id, invocation_id, kind, amount_usd_nanos, created_at)
VALUES ($1,$2,'released',0,$3)
ON CONFLICT (provider_id, invocation_id, kind) DO NOTHING`,
		providerID, invocationID, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, `UPDATE provider_wallets SET reserved_usd_nanos = reserved_usd_nanos - $1, version = version + 1, updated_at=$2 WHERE provider_id=$3`, reservedAmount, now, providerID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func lockWalletAndReservation(ctx context.Context, tx pgx.Tx, providerID string) (int64, int64, error) {
	var balance, reserved int64
	if err := tx.QueryRow(ctx, `SELECT balance_usd_nanos, reserved_usd_nanos FROM provider_wallets WHERE provider_id=$1 FOR UPDATE`, providerID).Scan(&balance, &reserved); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, 0, costledger.ErrWalletNotFound
		}
		return 0, 0, err
	}
	return balance, reserved, nil
}
